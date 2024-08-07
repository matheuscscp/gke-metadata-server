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

	"github.com/spf13/cobra"
	"k8s.io/kubernetes/pkg/util/iptables"
	"k8s.io/utils/exec"
)

func newInitNetworkCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "init-network",
		Short: "Prepare the pod network namespace for gke-metadata-server",
		Long: `Prepare the pod network namespace for gke-metadata-server,
routing traffic for the hardcoded GKE metadata server endpoint
to DAEMONSET_IP:DAEMONSET_PORT, where both DAEMONSET_IP and
DAEMONSET_PORT are provided as environment variables.`,
		RunE: func(cmd *cobra.Command, args []string) (err error) {
			emulatorIP := os.Getenv("DAEMONSET_IP")
			emulatorPort := os.Getenv("DAEMONSET_PORT")
			emulatorAddr := fmt.Sprintf("%s:%s", emulatorIP, emulatorPort)

			defer func() {
				if runtimeErr := err; runtimeErr != nil {
					err = nil
					logging.
						FromContext(cmd.Context()).
						WithField("emulator_addr", emulatorAddr).
						WithError(runtimeErr).
						Fatal("runtime error")
				}
			}()

			// Create the following iptables rules:
			// iptables -t nat -A OUTPUT -d 169.254.169.254 -p tcp --dport 80 -j DNAT --to-destination <emulatorAddr>
			// iptables -A FORWARD -d <emulatorIP> -p tcp --dport <emulatorPort> -m state --state NEW,ESTABLISHED,RELATED -j ACCEPT

			const gkeMetadataServer = "169.254.169.254"
			ipTables := iptables.New(exec.New(), iptables.ProtocolIPv4)
			_, err = ipTables.EnsureRule(
				iptables.Append,
				iptables.TableNAT,
				iptables.ChainOutput,
				"-d", gkeMetadataServer,
				"-p", "tcp",
				"--dport", "80",
				"-j", "DNAT",
				"--to-destination", emulatorAddr,
			)
			if err != nil {
				return fmt.Errorf("error adding DNAT rule: %w", err)
			}

			_, err = ipTables.EnsureRule(
				iptables.Append,
				iptables.TableFilter,
				iptables.ChainForward,
				"-d", emulatorIP,
				"-p", "tcp",
				"--dport", emulatorPort,
				"-m", "state",
				"--state", "NEW,ESTABLISHED,RELATED",
				"-j", "ACCEPT",
			)
			if err != nil {
				return fmt.Errorf("error adding forwarding rule: %w", err)
			}

			return nil
		},
	}
}
