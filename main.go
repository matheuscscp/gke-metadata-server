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
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"strings"
	"time"

	"github.com/matheuscscp/gke-metadata-server/api"
	"github.com/matheuscscp/gke-metadata-server/internal/googlecredentials"
	"github.com/matheuscscp/gke-metadata-server/internal/logging"
	"github.com/matheuscscp/gke-metadata-server/internal/loopback"
	"github.com/matheuscscp/gke-metadata-server/internal/metrics"
	getnode "github.com/matheuscscp/gke-metadata-server/internal/node/get"
	watchnode "github.com/matheuscscp/gke-metadata-server/internal/node/watch"
	listpods "github.com/matheuscscp/gke-metadata-server/internal/pods/list"
	watchpods "github.com/matheuscscp/gke-metadata-server/internal/pods/watch"
	"github.com/matheuscscp/gke-metadata-server/internal/routing"
	"github.com/matheuscscp/gke-metadata-server/internal/server"
	"github.com/matheuscscp/gke-metadata-server/internal/serviceaccounts"
	getserviceaccount "github.com/matheuscscp/gke-metadata-server/internal/serviceaccounts/get"
	watchserviceaccounts "github.com/matheuscscp/gke-metadata-server/internal/serviceaccounts/watch"
	cacheserviceaccounttokens "github.com/matheuscscp/gke-metadata-server/internal/serviceaccounttokens/cache"
	createserviceaccounttoken "github.com/matheuscscp/gke-metadata-server/internal/serviceaccounttokens/create"

	"github.com/sirupsen/logrus"
	"github.com/spf13/pflag"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
)

const shutdownGracePeriod = 20 * time.Second

var acceptedLogLevels = func() string {
	logLevels := make([]string, len(logrus.AllLevels))
	for i, level := range logrus.AllLevels {
		logLevels[i] = level.String()
	}
	return strings.Join(logLevels, ", ")
}()

func main() {
	var (
		stringLogLevel                      string
		serverPort                          int
		healthPort                          int
		serviceAccountName                  string
		serviceAccountNamespace             string
		projectID                           string
		workloadIdentityProvider            string
		watchPods                           bool
		watchPodsResyncPeriod               time.Duration
		watchPodsDisableFallback            bool
		watchNode                           bool
		watchNodeResyncPeriod               time.Duration
		watchNodeDisableFallback            bool
		watchServiceAccounts                bool
		watchServiceAccountsResyncPeriod    time.Duration
		watchServiceAccountsDisableFallback bool
		cacheTokens                         bool
		cacheTokensConcurrency              int
	)

	flags := pflag.NewFlagSet(os.Args[0], pflag.ContinueOnError)

	flags.StringVar(&stringLogLevel, "log-level", logrus.InfoLevel.String(),
		"Log level. Accepted values: "+acceptedLogLevels)
	flags.IntVar(&serverPort, "server-port", 8080,
		"Network address where the metadata server must listen on. Ignored on nodes annotated/labeled with loopback routing")
	flags.IntVar(&healthPort, "health-port", 8081,
		"Network address where the health server must listen on")
	flags.StringVar(&serviceAccountName, "service-account-name", "gke-metadata-server",
		"Name of the service account of the emulator for issuing Google Identity Tokens")
	flags.StringVar(&serviceAccountNamespace, "service-account-namespace", "kube-system",
		"Namespace of the service account of the emulator for issuing Google Identity Tokens")
	flags.StringVar(&projectID, "project-id", "",
		"Project ID of the GCP project where the GCP Workload Identity Provider is configured")
	flags.StringVar(&workloadIdentityProvider, "workload-identity-provider", "",
		"Mandatory fully-qualified resource name of the GCP Workload Identity Provider (projects/<project_number>/locations/global/workloadIdentityPools/<pool_name>/providers/<provider_name>)")
	flags.BoolVar(&watchPods, "watch-pods", false,
		"Whether or not to watch the pods running on the same node (default false)")
	flags.DurationVar(&watchPodsResyncPeriod, "watch-pods-resync-period", 10*time.Minute,
		"When watching the pods running on the same node, how often to fully resync")
	flags.BoolVar(&watchPodsDisableFallback, "watch-pods-disable-fallback", false,
		"When watching the pods running on the same node, whether or not to disable the use of a simple fallback method for retrieving pods upon cache misses (default false)")
	flags.BoolVar(&watchNode, "watch-node", false,
		"Whether or not to watch the node where the metadata server is running (default false)")
	flags.DurationVar(&watchNodeResyncPeriod, "watch-node-resync-period", time.Hour,
		"When watching the node where the metadata server is running, how often to fully resync")
	flags.BoolVar(&watchNodeDisableFallback, "watch-node-disable-fallback", false,
		"When watching the node where the metadata server is running, whether or not to disable the use of a simple fallback method for retrieving the node upon cache misses (default false)")
	flags.BoolVar(&watchServiceAccounts, "watch-service-accounts", false,
		"Whether or not to watch all the service accounts of the cluster (default false)")
	flags.DurationVar(&watchServiceAccountsResyncPeriod, "watch-service-accounts-resync-period", time.Hour,
		"When watching service accounts, how often to fully resync")
	flags.BoolVar(&watchServiceAccountsDisableFallback, "watch-service-accounts-disable-fallback", false,
		"When watching service accounts, whether or not to disable the use of a simple fallback method for retrieving service accounts upon cache misses (default false)")
	flags.BoolVar(&cacheTokens, "cache-tokens", false,
		"Whether or not to proactively cache tokens for the service accounts used by the pods running on the same node (default false)")
	flags.IntVar(&cacheTokensConcurrency, "cache-tokens-concurrency", 10,
		"When proactively caching service account tokens, what is the maximum amount of caching operations that can happen in parallel")

	if err := flags.Parse(os.Args[1:]); err != nil {
		if errors.Is(err, pflag.ErrHelp) {
			return
		}
		fmt.Fprintf(os.Stderr, "error parsing flags: %v\n", err)
		os.Exit(1)
	}

	ctx := ctrl.SetupSignalHandler()

	// init logger
	logLevel, err := logrus.ParseLevel(stringLogLevel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid value for --log-level flag. the accepted values are: %s\n", acceptedLogLevels)
		os.Exit(1)
	}
	l := logging.NewLogger(logLevel)
	ctx = logging.IntoContext(ctx, l)
	logging.InitKLog(l, logLevel)

	// validate inputs
	if serviceAccountName == "" {
		l.Fatal("--service-account-name must be specified")
	}
	if serviceAccountNamespace == "" {
		l.Fatal("--service-account-namespace must be specified")
	}
	serviceAccount := serviceaccounts.Reference{
		Name:      serviceAccountName,
		Namespace: serviceAccountNamespace,
	}
	nodeName := os.Getenv("NODE_NAME")
	if nodeName == "" {
		l.Fatal("NODE_NAME environment variable must be specified")
	}
	podIP := os.Getenv("POD_IP")
	if podIP == "" {
		l.Fatal("POD_IP environment variable must be specified")
	}
	emulatorIP, err := netip.ParseAddr(podIP)
	if err != nil {
		l.WithError(err).Fatal("error parsing POD_IP environment variable")
	}
	if !emulatorIP.Is4() {
		l.Fatal("POD_IP environment variable must be an IPv4 address")
	}
	googleCredentialsConfig, numericProjectID, workloadIdentityPool, err := googlecredentials.NewConfig(googlecredentials.ConfigOptions{
		WorkloadIdentityProvider: workloadIdentityProvider,
	})
	if err != nil {
		l.WithError(err).Fatal("error creating google credentials config")
	}

	// create kube client
	kubeConfig, err := rest.InClusterConfig()
	if err != nil {
		l.WithError(err).Fatal("error creating in-cluster kubeconfig")
	}
	kubeClient, err := kubernetes.NewForConfig(kubeConfig)
	if err != nil {
		l.WithError(err).Fatal("error creating kubernetes client")
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
			FallbackSource:  pods,
			NodeName:        nodeName,
			KubeClient:      kubeClient,
			MetricsRegistry: metricsRegistry,
			ResyncPeriod:    watchPodsResyncPeriod,
		}
		if watchPodsDisableFallback {
			opts.FallbackSource = nil
		}
		wp = watchpods.NewProvider(opts)
		defer wp.Close()
		pods = wp
	}

	// create node provider
	node := getnode.NewProvider(getnode.ProviderOptions{
		NodeName:   nodeName,
		KubeClient: kubeClient,
	})
	nodeGetter := node
	var wn *watchnode.Provider
	if watchNode {
		opts := watchnode.ProviderOptions{
			FallbackSource: node,
			NodeName:       nodeName,
			KubeClient:     kubeClient,
			ResyncPeriod:   watchNodeResyncPeriod,
		}
		if watchNodeDisableFallback {
			opts.FallbackSource = nil
		}
		wn = watchnode.NewProvider(opts)
		defer wn.Close()
		node = wn
	}

	// create service account provider
	serviceAccounts := getserviceaccount.NewProvider(getserviceaccount.ProviderOptions{
		KubeClient: kubeClient,
	})
	var wsa *watchserviceaccounts.Provider
	if watchServiceAccounts {
		opts := watchserviceaccounts.ProviderOptions{
			FallbackSource:  serviceAccounts,
			KubeClient:      kubeClient,
			MetricsRegistry: metricsRegistry,
			ResyncPeriod:    watchServiceAccountsResyncPeriod,
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
			Source:          serviceAccountTokens,
			ServiceAccounts: serviceAccounts,
			MetricsRegistry: metricsRegistry,
			Concurrency:     cacheTokensConcurrency,
		})
		defer p.Close()
		p.AddPodServiceAccount(&serviceAccount)
		if wp != nil {
			wp.AddListener(p)
		}
		if wn != nil {
			wn.AddListener(p)
		}
		if wsa != nil {
			wsa.AddListener(p)
		}
		serviceAccountTokens = p
	}

	// start watches
	if wp != nil {
		wp.Start(ctx)
	}
	if wn != nil {
		wn.Start(ctx)
	}
	if wsa != nil {
		wsa.Start(ctx)
	}

	// load and attach network route based on node annotation
	curNode, err := nodeGetter.Get(ctx)
	if err != nil {
		l.WithError(err).Fatal("error getting current node")
	}
	routingMode, closeRoute, err := routing.LoadAndAttach(curNode, emulatorIP, serverPort)
	if err != nil {
		l.WithField("routing", routingMode).WithError(err).Fatal("error loading and attaching network route")
	}
	defer func() {
		if err := closeRoute(); err != nil {
			l.WithField("routing", routingMode).WithError(err).Error("error closing network route")
		}
	}()
	serverAddr := fmt.Sprintf(":%d", serverPort)
	if routingMode == api.RoutingModeLoopback {
		serverAddr = loopback.GKEMetadataServerAddr
	}

	// start server
	s := server.New(ctx, server.ServerOptions{
		ServiceAccount:       serviceAccount,
		NodeName:             nodeName,
		PodIP:                podIP,
		Addr:                 serverAddr,
		HealthPort:           healthPort,
		Pods:                 pods,
		Node:                 node,
		ServiceAccounts:      serviceAccounts,
		ServiceAccountTokens: serviceAccountTokens,
		MetricsRegistry:      metricsRegistry,
		ProjectID:            projectID,
		NumericProjectID:     numericProjectID,
		WorkloadIdentityPool: workloadIdentityPool,
	})

	<-ctx.Done()
	l.Info("signal received, shutting down server")
	ctx, cancel := context.WithTimeout(context.Background(), shutdownGracePeriod)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		l.WithError(err).Error("error shutting down server")
	}
}
