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
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/matheuscscp/gke-metadata-server/internal/logging"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

const shutdownGracePeriod = 20 * time.Second

var (
	kubeconfig  string
	kubeContext string
)

func main() {
	var stringLogLevel string
	logLevels := make([]string, len(logrus.AllLevels))
	for i, level := range logrus.AllLevels {
		logLevels[i] = level.String()
	}
	acceptedLogLevels := strings.Join(logLevels, ", ")

	rootCmd := &cobra.Command{
		Use:   os.Args[0],
		Short: os.Args[0] + " is an open-source implementation of GCP Workload Identity Federation for GKE",
		Long:  os.Args[0] + " is an open-source implementation of GCP Workload Identity Federation for GKE for non-GKE Kubernetes clusters",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			logLevel, err := logrus.ParseLevel(stringLogLevel)
			if err != nil {
				return fmt.Errorf("not a valid log level. the accepted values are: %s", acceptedLogLevels)
			}

			l := logging.NewLogger(logLevel)
			cmd.SetContext(logging.IntoContext(cmd.Context(), l))

			// set also klog to use the same logger
			logging.InitKLog(l, logLevel)
			return nil
		},
	}

	rootCmd.AddCommand(newServerCommand())

	rootCmd.PersistentFlags().StringVar(&kubeconfig, "kubeconfig", filepath.Join(homedir.HomeDir(), ".kube", "config"),
		"Path to the kubeconfig file for creating the Kubernetes client")
	rootCmd.PersistentFlags().StringVar(&kubeContext, "context", "",
		"Context from kubeconfig to be used")
	rootCmd.PersistentFlags().StringVar(&stringLogLevel, "log-level", logrus.InfoLevel.String(),
		"Log level. Accepted values: "+acceptedLogLevels)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

// createKubernetesClient creates a Kubernetes client from the usual CLI flags:
// --kubeconfig and --context.
func createKubernetesClient(ctx context.Context) (*kubernetes.Clientset, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		logging.
			FromContext(ctx).
			WithError(err).
			Debug("error creating in-cluster kubeconfig, trying other options")

		overrides := &clientcmd.ConfigOverrides{CurrentContext: kubeContext}
		if kubeContext == "" {
			overrides = nil
		}
		loader := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
			&clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfig},
			overrides,
		)
		config, err = loader.ClientConfig()
		if err != nil {
			return nil, fmt.Errorf("error creating kubeconfig: %w", err)
		}
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("error creating client-go: %w", err)
	}
	return clientset, nil
}

// waitForShutdown waits for the given context to be done or for SIGINT or SIGTERM.
// The returned context and cancel function are the ones to be used for shutting
// down resources.
func waitForShutdown(ctx context.Context) (context.Context, context.CancelFunc) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	select {
	case <-ctx.Done():
		logging.FromContext(ctx).WithField("context_err", ctx.Err()).Info("command context done")
		ctx = context.Background()
	case sig := <-sigCh:
		signal.Stop(sigCh)
		logging.FromContext(ctx).WithField("signal", sig.String()).Info("signal received")
	}
	return context.WithTimeout(ctx, shutdownGracePeriod)
}
