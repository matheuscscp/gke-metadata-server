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

	"github.com/matheuscscp/gke-metadata-server/internal/logging"
	"github.com/matheuscscp/gke-metadata-server/internal/server"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

const defaultServerPort = "8080"

func newServerCommand() *cobra.Command {
	var workloadIdentityProvider string
	var serverAddr string

	cmd := &cobra.Command{
		Use:   "server",
		Short: "Start the GKE Workload Identity emulator",
		Long:  "Start the GKE Workload Identity emulator for serving requests locally inside each node of the Kubernes cluster",
		RunE: func(cmd *cobra.Command, args []string) (err error) {
			ctx := cmd.Context()
			l := logging.FromContext(ctx)
			defer func() {
				if runtimeErr := err; err != nil {
					err = nil
					l.WithError(runtimeErr).Fatal("runtime error")
				}
			}()

			clientset, _, err := createKubernetesClient(ctx)
			if err != nil {
				return fmt.Errorf("error creating k8s client: %w", err)
			}

			// start server
			serverOpts := server.ServerOptions{
				NodeName:                 os.Getenv("NODE_NAME"),
				DaemonName:               os.Getenv("POD_NAME"),
				DaemonNamespace:          os.Getenv("POD_NAMESPACE"),
				WorkloadIdentityProvider: workloadIdentityProvider,
				ServerAddr:               serverAddr,
			}
			l = l.WithFields(logrus.Fields{
				"node": serverOpts.NodeName,
				"daemon": logrus.Fields{
					"name":      serverOpts.DaemonName,
					"namespace": serverOpts.DaemonNamespace,
				},
				"workload_identity_provider": serverOpts.WorkloadIdentityProvider,
			})
			ctx = logging.IntoContext(ctx, l)
			s := server.New(ctx, serverOpts, clientset)

			ctx, cancel := waitForShutdown(ctx)
			defer cancel()
			if err := s.Shutdown(ctx); err != nil {
				return fmt.Errorf("error in graceful shutdown: %w", err)
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&workloadIdentityProvider, "workload-identity-provider", "",
		"Fully qualified resource name (e.g. projects/12345/locations/global/workloadIdentityPools/on-prem-k8s/providers/this-cluster)")
	cmd.Flags().StringVar(&serverAddr, "server-addr", ":"+defaultServerPort,
		"Network address where the metadata server must listen on")

	return cmd
}
