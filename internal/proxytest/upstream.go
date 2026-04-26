// Copyright 2025 Matheus Pimenta.
// SPDX-License-Identifier: AGPL-3.0

// Package proxytest contains a test-only HTTP marker server bound to
// 169.254.169.254:80 inside the daemon's host network namespace. It exists
// purely to e2e-test the eBPF-mode proxy passthrough chain:
//
//	app pod connects 169.254.169.254:80 (non-metadata path)
//	  → cgroup/connect4 rewrites destination to the daemon's listen addr
//	  → proxy.handle() sniffs the request, recognises non-metadata
//	  → daemon dials 169.254.169.254:80
//	  → cgroup/connect4 sees the daemon's cgroup, exempts the connect
//	  → connection routes via lo to this server
//	  → marker is returned and proxied back to the app pod
//
// A successful end-to-end response from the marker server thus proves that
// the redirect's self-exemption (the kernel-level half) and the userspace
// sniff/forward (the userspace half) are both wired up correctly.
package proxytest

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"syscall"

	"github.com/vishvananda/netlink"
)

const (
	// HeaderName is the response header carrying the marker.
	HeaderName = "X-Proxy-Test-Marker"
	// Marker is the value asserted by the e2e test.
	Marker = "gke-metadata-server-proxy-passthrough-ok"
	// Path is the URL path the test pod requests. Anything not starting with
	// "/computeMetadata/v1" causes proxy.handle() to take the forward branch.
	Path = "/_proxy_test"

	listenAddr = "169.254.169.254:80"
	addrCIDR   = "169.254.169.254/32"
)

// Start binds 169.254.169.254 to the loopback interface and serves the
// marker on port 80. It returns once the listener is up so callers can use
// /readyz to gate readiness on the full chain being live. The HTTP server
// runs in a background goroutine and is left running for the daemon's
// lifetime; the test cluster is torn down between runs so we don't bother
// with a teardown path.
func Start() error {
	lo, err := netlink.LinkByName("lo")
	if err != nil {
		return fmt.Errorf("looking up lo: %w", err)
	}
	a, err := netlink.ParseAddr(addrCIDR)
	if err != nil {
		return fmt.Errorf("parsing addr: %w", err)
	}
	a.Scope = int(netlink.SCOPE_HOST)
	if err := netlink.AddrAdd(lo, a); err != nil && !errors.Is(err, syscall.EEXIST) {
		return fmt.Errorf("adding %s to lo: %w", addrCIDR, err)
	}

	lis, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", listenAddr, err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(HeaderName, Marker)
		w.WriteHeader(http.StatusNoContent)
	})
	go http.Serve(lis, mux)
	return nil
}
