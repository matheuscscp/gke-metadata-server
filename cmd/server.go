// MIT License
//
// Copyright (c) 2023 Matheus Pimenta
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package main

import (
	"fmt"
	"os"
	"time"

	"github.com/matheuscscp/gke-metadata-server/internal/googlecredentials"
	"github.com/matheuscscp/gke-metadata-server/internal/logging"
	"github.com/matheuscscp/gke-metadata-server/internal/metrics"
	listpods "github.com/matheuscscp/gke-metadata-server/internal/pods/list"
	watchpods "github.com/matheuscscp/gke-metadata-server/internal/pods/watch"
	"github.com/matheuscscp/gke-metadata-server/internal/server"
	getserviceaccount "github.com/matheuscscp/gke-metadata-server/internal/serviceaccounts/get"
	watchserviceaccounts "github.com/matheuscscp/gke-metadata-server/internal/serviceaccounts/watch"
	cacheserviceaccounttokens "github.com/matheuscscp/gke-metadata-server/internal/serviceaccounttokens/cache"
	createserviceaccounttoken "github.com/matheuscscp/gke-metadata-server/internal/serviceaccounttokens/create"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

const (
	defaultServerPort = "8080"
)

func newServerCommand() *cobra.Command {
	const (
		metricsSubsystem       = ""   // empty since metrics.Namespace already ends with "server"
		tokenExpirationSeconds = 3600 // 1h
	)

	var (
		serverAddr                          string
		workloadIdentityProvider            string
		watchPods                           bool
		watchPodsResyncPeriod               time.Duration
		watchPodsDisableFallback            bool
		watchServiceAccounts                bool
		watchServiceAccountsResyncPeriod    time.Duration
		watchServiceAccountsDisableFallback bool
		cacheTokens                         bool
		cacheTokensConcurrency              int
	)

	cmd := &cobra.Command{
		Use:   "server",
		Short: "Start the GKE Workload Identity emulator",
		Long:  "Start the GKE Workload Identity emulator for serving requests locally inside each node of the Kubernes cluster",
		RunE: func(cmd *cobra.Command, args []string) (err error) {
			googleCredentialsConfig, err := googlecredentials.NewConfig(googlecredentials.ConfigOptions{
				TokenExpirationSeconds:   tokenExpirationSeconds,
				WorkloadIdentityProvider: workloadIdentityProvider,
			})
			if err != nil { // this only returns input errors
				return err
			}

			nodeName := os.Getenv("NODE_NAME")
			ctx := logging.IntoContext(cmd.Context(), logging.FromContext(cmd.Context()).WithFields(logrus.Fields{
				"node":                       nodeName,
				"workload_identity_provider": workloadIdentityProvider,
			}))
			l := logging.FromContext(ctx)
			defer func() {
				if runtimeErr := err; err != nil {
					err = nil
					l.WithError(runtimeErr).Fatal("runtime error")
				}
			}()

			kubeClient, _, err := createKubernetesClient(ctx)
			if err != nil {
				return fmt.Errorf("error creating k8s client: %w", err)
			}

			metricsRegistry := metrics.NewRegistry()

			// create pod provider
			pods := listpods.NewProvider(listpods.ProviderOptions{
				NodeName:   nodeName,
				KubeClient: kubeClient,
			})
			var wp *watchpods.Provider
			if watchPods {
				opts := watchpods.ProviderOptions{
					FallbackSource:   pods,
					NodeName:         nodeName,
					MetricsSubsystem: metricsSubsystem,
					KubeClient:       kubeClient,
					MetricsRegistry:  metricsRegistry,
					ResyncPeriod:     watchPodsResyncPeriod,
				}
				if watchPodsDisableFallback {
					opts.FallbackSource = nil
				}
				wp = watchpods.NewProvider(opts)
				defer wp.Close()
				pods = wp
			}

			// create service account provider
			serviceAccounts := getserviceaccount.NewProvider(getserviceaccount.ProviderOptions{
				KubeClient: kubeClient,
			})
			var wsa *watchserviceaccounts.Provider
			if watchServiceAccounts {
				opts := watchserviceaccounts.ProviderOptions{
					FallbackSource:   serviceAccounts,
					MetricsSubsystem: metricsSubsystem,
					KubeClient:       kubeClient,
					MetricsRegistry:  metricsRegistry,
					ResyncPeriod:     watchServiceAccountsResyncPeriod,
				}
				if watchServiceAccountsDisableFallback {
					opts.FallbackSource = nil
				}
				wsa = watchserviceaccounts.NewProvider(ctx, opts)
				defer wsa.Close()
				serviceAccounts = wsa
			}

			// create service account token provider
			serviceAccountTokens := createserviceaccounttoken.NewProvider(createserviceaccounttoken.ProviderOptions{
				GoogleCredentialsConfig: googleCredentialsConfig,
				KubeClient:              kubeClient,
			})
			if cacheTokens {
				p := cacheserviceaccounttokens.NewProvider(ctx, cacheserviceaccounttokens.ProviderOptions{
					Source:           serviceAccountTokens,
					ServiceAccounts:  serviceAccounts,
					MetricsSubsystem: metricsSubsystem,
					MetricsRegistry:  metricsRegistry,
					Concurrency:      cacheTokensConcurrency,
				})
				defer p.Close()
				if wp != nil {
					wp.AddListener(p)
				}
				if wsa != nil {
					wsa.AddListener(p)
				}
				serviceAccountTokens = p
			}

			// start server
			if wp != nil {
				wp.Start(ctx)
			}
			if wsa != nil {
				wsa.Start(ctx)
			}
			s := server.New(ctx, server.ServerOptions{
				NodeName:                nodeName,
				ServerAddr:              serverAddr,
				MetricsSubsystem:        metricsSubsystem,
				Pods:                    pods,
				ServiceAccounts:         serviceAccounts,
				ServiceAccountTokens:    serviceAccountTokens,
				GoogleCredentialsConfig: googleCredentialsConfig,
				MetricsRegistry:         metricsRegistry,
			})

			ctx, cancel := waitForShutdown(ctx)
			defer cancel()
			if err := s.Shutdown(ctx); err != nil {
				return fmt.Errorf("error in graceful shutdown: %w", err)
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&serverAddr, "server-addr", ":"+defaultServerPort,
		"Network address where the metadata server must listen on")
	cmd.Flags().StringVar(&workloadIdentityProvider, "workload-identity-provider", "",
		"Fully qualified resource name (projects/<project_number>/locations/global/workloadIdentityPools/<pool_name>/providers/<provider_name>)")
	cmd.Flags().BoolVar(&watchPods, "watch-pods", false,
		"Whether or not to watch the pods running on the same node")
	cmd.Flags().BoolVar(&watchPodsDisableFallback, "watch-pods-disable-fallback", false,
		"When watching the pods running on the same node, whether or not to disable the use of a simple fallback method for retrieving pods upon cache misses")
	cmd.Flags().DurationVar(&watchPodsResyncPeriod, "watch-pods-resync-period", 10*time.Minute,
		"When watching the pods running on the same node, how often to fully resync")
	cmd.Flags().BoolVar(&watchServiceAccounts, "watch-service-accounts", false,
		"Whether or not to watch all the service accounts of the cluster")
	cmd.Flags().BoolVar(&watchServiceAccountsDisableFallback, "watch-service-accounts-disable-fallback", false,
		"When watching service accounts, whether or not to disable the use of a simple fallback method for retrieving service accounts upon cache misses")
	cmd.Flags().DurationVar(&watchServiceAccountsResyncPeriod, "watch-service-accounts-resync-period", time.Hour,
		"When watching service accounts, how often to fully resync")
	cmd.Flags().BoolVar(&cacheTokens, "cache-tokens", false,
		"Whether or not to proactively cache tokens for the service accounts used by the pods running on the same node")
	cmd.Flags().IntVar(&cacheTokensConcurrency, "cache-tokens-concurrency", 10,
		"When proactively caching service account tokens, what is the maximum amount of caching operations that can happen in parallel")

	return cmd
}
