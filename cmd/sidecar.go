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
	"io"
	"net"
	"net/netip"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/matheuscscp/gke-metadata-server/internal/logging"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/vishvananda/netlink"
)

type frontendProxy struct {
	net.Listener
	addr        string
	backendAddr string

	ip     string
	close  chan struct{}
	closed chan struct{}
}

func newSidecarCommand() *cobra.Command {
	const gkeMetadataAddress = "169.254.169.254:80"

	var networkPaths []string
	var goroutinesPerFrontend int

	cmd := &cobra.Command{
		Use:   "sidecar",
		Short: "Start the sidecar for proxying requests to the GKE Workload Identity emulator",
		Long: fmt.Sprintf(`
Start the sidecar for proxying requests from inside the Pod into the
GKE Workload Identity emulator running on the same node of the Pod.

This proxy is actually a generic tool that supports listening on a list
of "frontend" network addresses, each backed by its own "backend" port.
The incoming TCP connetions are all targeted to the Kubernetes Node IP,
but the backend port is defined by which frontend listener received the
connection.

For each IP address appearing in a frontend network address, the program
adds this IP to the loopback interface of the current network namespace.
The program will bind to all the frontend ports in this namespace, which
means that there can be no collisions, i.e. the frontend ports must be
unique among all the specified "network paths" (i.e. among all specified
                   frontend_addr ---> backend_port
mappings.

The Node IP address cannot be in the set of specified frontend IP
addresses, neither be a loopback IP address. This is for avoiding
cycles. The Node IP is specified through the NODE_IP environment
variable.

The default list of network paths contains a single path: the one needed
for GKE Workload Identity, i.e. %s mapped to the default
metadata server port (%s).

`, gkeMetadataAddress, defaultServerPort),
		RunE: func(cmd *cobra.Command, args []string) (err error) {
			// parse node ip. here we prefer an env var because
			// we have to mount an env var from the status.hostIP
			// field ref in the pod container anyways, so no point
			// in adding a CLI flag for that as well
			nodeIP := strings.TrimSpace(os.Getenv("NODE_IP"))
			nodeIPAddr, err := netip.ParseAddr(nodeIP)
			if err != nil {
				return fmt.Errorf("error parsing node ip address: %w", err)
			}
			if nodeIPAddr.IsLoopback() {
				return fmt.Errorf("the specified node ip address '%s' is a loopback address", nodeIP)
			}

			// parse network paths
			frontendIPs := map[string]*netlink.Addr{} // will be added to the loopback interface
			frontendPorts := map[string]int{}         // frontend_port ---> idx in networkPaths
			networkMap := map[string]string{}         // frontend_addr ---> backend_addr
			for j, networkPath := range networkPaths {
				// parse network path
				networkPath = strings.TrimSpace(networkPath)
				networkPaths[j] = networkPath
				s := strings.Split(networkPath, "=")
				if len(s) != 2 {
					return fmt.Errorf("network path '%s' does not have the form <frontend_addr>=<backend_port>", networkPath)
				}
				frontendAddr, backendPort := strings.TrimSpace(s[0]), strings.TrimSpace(s[1])

				// parse frontend addr
				frontendHost, frontendPort, err := net.SplitHostPort(frontendAddr)
				if err != nil {
					return fmt.Errorf("error splitting frontend address '%s' from network path '%s' into host-port: %w",
						frontendAddr, networkPath, err)
				}
				frontendHost, frontendPort = strings.TrimSpace(frontendHost), strings.TrimSpace(frontendPort)

				// parse frontend ip
				frontendIPAddr, err := netlink.ParseAddr(frontendHost + "/32")
				if err != nil {
					return fmt.Errorf("error parsing frontend host '%s' from network path '%s' as an ip address: %w",
						frontendHost, networkPath, err)
				}
				frontendIP := frontendHost
				if nodeIP == frontendIP {
					return fmt.Errorf("the node ip is the same ip for the frontend address in the network path '%s'", networkPath)
				}
				frontendIPs[frontendIP] = frontendIPAddr

				// parse frontend port
				if i, ok := frontendPorts[frontendPort]; ok {
					return fmt.Errorf("frontend port %s appears in more than one network path ('%s' and '%s')",
						frontendPort, networkPaths[i], networkPaths[j])
				}
				frontendPorts[frontendPort] = j

				// parse backend port
				if _, _, err := net.SplitHostPort(":" + backendPort); err != nil {
					return fmt.Errorf("error parsing backend port '%s': %w", backendPort, err)
				}

				// add parsed network path to map
				networkMap[frontendAddr] = nodeIP + ":" + backendPort
			}

			// now errors are runtime errors, not input/CLI mis-usage errors anymore
			ctx := cmd.Context()
			l := logging.FromContext(ctx)
			defer func() {
				if runtimeErr := err; err != nil {
					err = nil
					l.WithError(runtimeErr).Fatal("runtime error")
				}
			}()

			// add frontend IPs to the loopback interface
			lo, err := netlink.LinkByName("lo")
			if err != nil {
				return fmt.Errorf("error getting the loopback interface: %w", err)
			}
			type loopbackIP struct {
				ip     string
				ipAddr *netlink.Addr
			}
			loopbackIPs := make([]*loopbackIP, 0, len(frontendIPs))
			defer func() {
				for _, loip := range loopbackIPs {
					l := l.WithField("loopback_ip", loip.ip)
					if err := netlink.AddrDel(lo, loip.ipAddr); err != nil {
						l.WithError(err).Error("error deleting ip address from loopback interface")
					} else {
						l.Info("ip address deleted from loopback interface")
					}
				}
			}()
			for ip, ipAddr := range frontendIPs {
				if err := netlink.AddrAdd(lo, ipAddr); err != nil {
					return fmt.Errorf("error adding ip address '%s' to the loopback interface: %w", ip, err)
				}
				l.WithField("loopback_ip", ip).Info("ip address added to the loopback interface")
				loopbackIPs = append(loopbackIPs, &loopbackIP{
					ip:     ip,
					ipAddr: ipAddr,
				})
			}

			// bind to frontend ports
			frontends := make([]*frontendProxy, 0, len(networkMap))
			defer func() {
				for _, frontend := range frontends {
					frontend.Listener.Close()
				}
			}()
			for frontendAddr, backendAddr := range networkMap {
				frontendIP, _, _ := net.SplitHostPort(frontendAddr)
				frontend := &frontendProxy{
					ip:          frontendIP,
					addr:        frontendAddr,
					backendAddr: backendAddr,
				}
				frontend.Listener, err = net.Listen("tcp", frontendAddr)
				if err != nil {
					return fmt.Errorf("error listening on frontend address '%s': %w", frontendAddr, err)
				}
				frontends = append(frontends, frontend)
				frontend.withFields(l).Info("started listening on frontend address")
			}

			// start frontend servers
			for _, frontend := range frontends {
				frontend.start(ctx, goroutinesPerFrontend)
			}

			// wait for shutdown and shutdown all frontend servers
			ctx, cancel := waitForShutdown(ctx)
			defer cancel()
			for _, frontend := range frontends {
				if err := frontend.shutdown(ctx); err != nil {
					return fmt.Errorf("error shutting down frontend server '%s': %w", frontend.addr, err)
				}
			}
			return nil
		},
	}

	cmd.Flags().StringSliceVar(&networkPaths, "network-path",
		[]string{gkeMetadataAddress + "=" + defaultServerPort},
		"List of network paths of the form <frontend_addr>=<backend_port>")
	cmd.Flags().IntVar(&goroutinesPerFrontend, "goroutines-per-frontend", 5,
		"Number of goroutines per frontend server (each connection being proxied eats up one goroutine)")

	return cmd
}

func (f *frontendProxy) withFields(l logrus.FieldLogger) logrus.FieldLogger {
	return l.WithFields(logrus.Fields{
		"frontend_addr": f.addr,
		"backend_addr":  f.backendAddr,
	})
}

func (f *frontendProxy) start(ctx context.Context, goroutines int) {
	l := f.withFields(logging.FromContext(ctx))
	ctx = logging.IntoContext(ctx, l)

	f.close = make(chan struct{})
	f.closed = make(chan struct{})
	f.ip, _, _ = net.SplitHostPort(f.addr)

	go func() {
		defer close(f.closed)

		// start workers
		var wg sync.WaitGroup
		defer wg.Wait()
		work := make(chan net.Conn)
		defer close(work)
		for i := 0; i < goroutines; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for conn := range work {
					l := l.WithField("client_addr", conn.RemoteAddr().String())
					if err := f.proxy(logging.IntoContext(ctx, l), conn); err != nil {
						l.WithError(err).Error("error proxying connection")
					}
				}
			}()
		}

		// accept connections and dispatch work until either
		// the context is canceled or shutdown is requested
		for {
			conn, err := f.Accept()
			if err != nil && !isUseOfClosedConn(err) {
				if !isUseOfClosedConn(err) {
					l.WithError(err).Error("error accepting connection in frontend server, will shutdown")
				}
				return
			}
			l := l.WithField("client_addr", conn.RemoteAddr().String())
			l.Debug("connection accepted")

			select {
			case <-ctx.Done():
				conn.Close()
				l.
					WithField("context_err", ctx.Err()).
					Info("context done while dispatching connection to goroutines, frontend server will shutdown")
				return
			case <-f.close:
				conn.Close()
				l.Info("frontend server shutdown requested while dispatching connection to goroutines")
				return
			case work <- conn:
				l.Debug("connection dispatched to frontend server goroutines")
			}
		}
	}()

	l.Info("frontend server started")
}

func (f *frontendProxy) shutdown(ctx context.Context) error {
	f.Listener.Close()
	close(f.close)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-f.closed:
		return nil
	}
}

func (f *frontendProxy) proxy(ctx context.Context, clientConn net.Conn) error {
	t0 := time.Now()
	defer clientConn.Close()

	// parse ip
	host, _, err := net.SplitHostPort(clientConn.RemoteAddr().String())
	if err != nil {
		return fmt.Errorf("error splitting client address into host-port: %w", err)
	}
	ipAddr, err := netip.ParseAddr(host)
	if err != nil {
		return fmt.Errorf("error parsing client address host as ip address: %w", err)
	}
	if ipAddr.String() != f.ip {
		return fmt.Errorf("client ip does not match the loopback ip")
	}
	l := logging.FromContext(ctx)
	l.Debug("connection can be trusted and will be proxied")

	// dial new connection with the backend
	dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	backendConn, err := (&net.Dialer{}).DialContext(dialCtx, "tcp", f.backendAddr)
	if err != nil {
		return fmt.Errorf("error dialing backend: %w", err)
	}
	defer backendConn.Close()

	// copy bytes in both directions in parallel
	done := [2]chan struct{}{make(chan struct{}), make(chan struct{})}
	defer func() {
		for _, c := range []net.Conn{clientConn, backendConn} {
			c.Close()
		}
		for _, d := range done {
			<-d
		}
	}()
	const clientToBackend = 0
	const backendToClient = 1
	directions := [2][2]net.Conn{}
	directions[clientToBackend] = [2]net.Conn{clientConn, backendConn}
	directions[backendToClient] = [2]net.Conn{backendConn, clientConn}
	for i, direction := range directions {
		src, dst, d := direction[0], direction[1], done[i]
		go func() {
			defer close(d)
			if _, err := io.Copy(dst, src); err != nil && !isUseOfClosedConn(err) {
				l.
					WithError(err).
					WithField("src_addr", src.RemoteAddr().String()).
					WithField("dst_addr", dst.RemoteAddr().String()).
					Error("error proxying connection direction")
			}
		}()
	}

	// block until either the context is canceled or both
	// goroutines above are finished
	var event string
	select {
	case <-ctx.Done():
		l = l.WithField("context_err", ctx.Err())
		event = "context done while proxying connection"
	case <-done[clientToBackend]:
		event = "the client closed the connection"
	case <-done[backendToClient]:
		event = "the backend closed the connection"
	}
	l.
		WithField("close_event", event).
		WithField("latency", time.Since(t0).String()).
		Info("connection proxied")
	return nil
}

func isUseOfClosedConn(err error) bool {
	return strings.Contains(err.Error(), "use of closed network connection")
}
