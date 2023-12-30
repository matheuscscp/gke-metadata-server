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

	"github.com/matheuscscp/gke-metadata-server/internal/logging"

	"github.com/spf13/cobra"
	"github.com/vishvananda/netlink"
)

const gkeMetadataIPAddress = "169.254.169.254"

func newInitNetworkCommand() *cobra.Command {
	var ipAddresses []string

	cmd := &cobra.Command{
		Use:   "init-network",
		Short: "Add a list of IP addresses to the loopback interface",
		Long:  "Add a list of IP addresses to the loopback interface of the current network namespace",
		RunE: func(cmd *cobra.Command, args []string) (err error) {
			ips := map[string]*netlink.Addr{}
			for _, ip := range ipAddresses {
				ipAddr, err := netlink.ParseAddr(ip + "/32")
				if err != nil {
					return fmt.Errorf("error parsing '%s' as an ip address: %w", ip, err)
				}
				ips[ip] = ipAddr
			}

			ctx := cmd.Context()
			l := logging.FromContext(ctx)
			defer func() {
				if runtimeErr := err; err != nil {
					err = nil
					l.WithError(runtimeErr).Fatal("runtime error")
				}
			}()

			lo, err := netlink.LinkByName("lo")
			if err != nil {
				return fmt.Errorf("error getting the loopback interface: %w", err)
			}
			for ip, ipAddr := range ips {
				if err := netlink.AddrAdd(lo, ipAddr); err != nil {
					return fmt.Errorf("error adding ip address '%s' to the loopback interface: %w", ip, err)
				}
				l.WithField("loopback_ip", ip).Info("ip address added to the loopback interface")
			}
			return nil
		},
	}

	cmd.Flags().StringSliceVar(&ipAddresses, "ip-address", []string{gkeMetadataIPAddress},
		"List of IP addresses to be added")

	return cmd
}
