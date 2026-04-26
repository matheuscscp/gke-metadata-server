// Copyright 2025 Matheus Pimenta.
// SPDX-License-Identifier: AGPL-3.0

package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"time"

	"github.com/matheuscscp/gke-metadata-server/api"
	pkghttp "github.com/matheuscscp/gke-metadata-server/internal/http"
	"github.com/matheuscscp/gke-metadata-server/internal/logging"
	"github.com/matheuscscp/gke-metadata-server/internal/metrics"
	"github.com/matheuscscp/gke-metadata-server/internal/pods"
	"github.com/matheuscscp/gke-metadata-server/internal/proxy"
	"github.com/matheuscscp/gke-metadata-server/internal/serviceaccounts"
	"github.com/matheuscscp/gke-metadata-server/internal/serviceaccounttokens"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
)

// localAddrContextKey is the context key under which the metadataServer's
// ConnContext stashes the accepted socket's LocalAddr.
type localAddrContextKey struct{}

// LocalAddrFromRequest returns the server-side LocalAddr of the connection
// the request arrived on, or nil if it was not captured.
func LocalAddrFromRequest(r *http.Request) *net.TCPAddr {
	v, _ := r.Context().Value(localAddrContextKey{}).(*net.TCPAddr)
	return v
}

type (
	Server struct {
		opts           ServerOptions
		metadataServer *http.Server
		healthServer   *http.Server
		metrics        serverMetrics
	}

	ServerOptions struct {
		NodeName             string
		PodIP                string
		Addr                 string
		HealthPort           int
		Pods                 pods.Provider
		ServiceAccounts      serviceaccounts.Provider
		ServiceAccountTokens serviceaccounttokens.Provider
		MetricsRegistry      *prometheus.Registry
		ProjectID            string
		NumericProjectID     string
		WorkloadIdentityPool string
		RoutingMode          string
		PodLookup            PodLookupOptions

		// Attestation resolves a connection 4-tuple to the kubernetes pod
		// UID of the connecting process. Required in eBPF mode and for
		// hostNetwork pods in Loopback or None modes; the (mode, pod-kind)
		// selection in pods.go decides whether it is consulted for a given
		// request.
		Attestation AttestationLookuper
	}

	// AttestationLookuper resolves a connection 4-tuple to the kubernetes
	// pod UID of the connecting process. Implementations differ by routing
	// mode (eBPF sockops map vs netlink SOCK_DIAG + /proc walk) but both
	// converge on a kernel-attested pod identity.
	AttestationLookuper interface {
		Lookup(srcIP, dstIP netip.Addr, srcPort, dstPort uint16) (podUID string, err error)
		// Verify checks that the attestation pipeline correctly captured the
		// connection identified by the given 4-tuple. Used by the readiness
		// probe to gate /readyz on attestation being live; in eBPF mode this
		// confirms the sockops program actually fired and recorded the entry,
		// in netlink modes it's a no-op.
		Verify(srcIP, dstIP netip.Addr, srcPort, dstPort uint16) error
	}

	PodLookupOptions struct {
		MaxAttempts       int           // default: 3
		RetryInitialDelay time.Duration // default: time.Second
		RetryMaxDelay     time.Duration // default: 30 * time.Second
	}

	serverMetrics struct {
		lookupPodFailures *prometheus.CounterVec
	}
)

const (
	gkeNodeNameAPI               = "/computeMetadata/v1/instance/name"
	gkeProjectIDAPI              = "/computeMetadata/v1/project/project-id"
	gkeNumericProjectIDAPI       = "/computeMetadata/v1/project/numeric-project-id"
	gkeServiceAccountsDirectory  = "/computeMetadata/v1/instance/service-accounts/$service_account"
	gkeServiceAccountAliasesAPI  = "/computeMetadata/v1/instance/service-accounts/$service_account/aliases"
	gkeServiceAccountEmailAPI    = "/computeMetadata/v1/instance/service-accounts/$service_account/email"
	gkeServiceAccountIdentityAPI = "/computeMetadata/v1/instance/service-accounts/$service_account/identity"
	gkeServiceAccountScopesAPI   = "/computeMetadata/v1/instance/service-accounts/$service_account/scopes"
	gkeServiceAccountTokenAPI    = "/computeMetadata/v1/instance/service-accounts/$service_account/token"
)

func New(ctx context.Context, opts ServerOptions) *Server {
	// prepare logger and base context
	healthAddr := fmt.Sprintf(":%d", opts.HealthPort)
	l := logging.FromContext(ctx).WithFields(logrus.Fields{
		"server_addr": opts.Addr,
		"health_addr": healthAddr,
	})
	ctx = logging.IntoContext(context.Background(), l)
	baseContext := func(net.Listener) context.Context { return ctx }

	// prepare metrics

	reqLatencyMillis := metrics.NewRequestLatencyMillis()
	opts.MetricsRegistry.MustRegister(reqLatencyMillis)
	observeLatencyMillis := func(r *http.Request, statusCode int, latencyMs float64) {
		reqLatencyMillis.
			WithLabelValues(r.Method, r.URL.Path, fmt.Sprint(statusCode)).
			Observe(latencyMs)
	}

	lookupPodFailures := metrics.NewLookupPodFailuresCounter()
	opts.MetricsRegistry.MustRegister(lookupPodFailures)

	proxyDialLatencyMillis := metrics.NewProxyDialLantencyMillis()
	opts.MetricsRegistry.MustRegister(proxyDialLatencyMillis)

	proxyActiveConnections := metrics.NewProxyActiveConnectionsGauge()
	opts.MetricsRegistry.MustRegister(proxyActiveConnections)

	observabilityMiddleware := func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r = pkghttp.InitRequest(r, observeLatencyMillis)

			r = logging.IntoRequest(r, logging.FromRequest(r).WithField("http_request", logrus.Fields{
				"method": r.Method,
				"path":   r.URL.Path,
				"query":  r.URL.Query(),
			}))

			h.ServeHTTP(w, r)
		})
	}

	// create server
	metadataHandler := &pkghttp.DirectoryHandler{}
	healthHandler := http.NewServeMux()
	s := &Server{
		opts: opts,
		metrics: serverMetrics{
			lookupPodFailures: lookupPodFailures,
		},
		metadataServer: &http.Server{
			Addr:        opts.Addr,
			BaseContext: baseContext,
			// ConnContext stashes the accepted socket's LocalAddr in the
			// request context so the request handler can use it as the
			// destination for kernel-attestation 4-tuple lookups. The
			// listener bind address (e.g. ":16321" wildcard) is not
			// equivalent to the actual local addr the kernel chose for
			// each connection, especially on Loopback mode where the
			// link-local 169.254.169.254 differs from PodIP.
			ConnContext: func(ctx context.Context, c net.Conn) context.Context {
				if a, ok := c.LocalAddr().(*net.TCPAddr); ok {
					ctx = context.WithValue(ctx, localAddrContextKey{}, a)
				}
				return ctx
			},
			Handler: observabilityMiddleware(metadataHandler),
		},
		healthServer: &http.Server{
			Addr:        healthAddr,
			BaseContext: baseContext,
			Handler:     observabilityMiddleware(healthHandler),
		},
	}

	// setup metadata handlers
	metadataHandler.HandleMetadata(gkeNodeNameAPI, s.gkeNodeNameAPI())
	metadataHandler.HandleMetadata(gkeProjectIDAPI, s.gkeProjectIDAPI())
	metadataHandler.HandleMetadata(gkeNumericProjectIDAPI, s.gkeNumericProjectIDAPI())
	metadataHandler.HandleDirectory(gkeServiceAccountsDirectory, s.listPodGoogleServiceAccounts)
	metadataHandler.HandleMetadata(gkeServiceAccountAliasesAPI, s.gkeServiceAccountAliasesAPI())
	metadataHandler.HandleMetadata(gkeServiceAccountEmailAPI, s.gkeServiceAccountEmailAPI())
	metadataHandler.HandleMetadata(gkeServiceAccountIdentityAPI, s.gkeServiceAccountIdentityAPI())
	metadataHandler.HandleMetadata(gkeServiceAccountScopesAPI, s.gkeServiceAccountScopesAPI())
	metadataHandler.HandleMetadata(gkeServiceAccountTokenAPI, s.gkeServiceAccountTokenAPI())

	l.WithField("metadata_directory", metadataHandler).Info("metadata directory initialized")

	// setup health handlers
	healthHandler.Handle("/metrics", metrics.HandlerFor(opts.MetricsRegistry, l))
	healthHandler.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	// /readyz exercises the full request path against the daemon itself
	// using a fresh TCP connection (DisableKeepAlives), then asks the
	// attestation lookuper whether it captured that connection's 4-tuple.
	// In eBPF mode this gates readiness on the sockops program actually
	// firing for new connects; in netlink modes the verify is a no-op.
	healthHandler.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		l := logging.FromRequest(r)
		var local, remote *net.TCPAddr
		client := &http.Client{Transport: &http.Transport{
			DisableKeepAlives: true,
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				conn, err := (&net.Dialer{}).DialContext(ctx, network, addr)
				if err == nil {
					local = conn.LocalAddr().(*net.TCPAddr)
					remote = conn.RemoteAddr().(*net.TCPAddr)
				}
				return conn, err
			},
		}}
		url := fmt.Sprintf("http://%s%s", opts.Addr, gkeNodeNameAPI)
		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, url, nil)
		if err != nil {
			l.WithError(err).Error("readiness: building self request")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		req.Header.Set(pkghttp.MetadataFlavorHeader, pkghttp.MetadataFlavorGoogle)
		resp, err := client.Do(req)
		if err != nil {
			l.WithError(err).Error("readiness: self request failed")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			l.WithField("status_code", resp.StatusCode).Error("readiness: self request returned non-200")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			l.WithError(err).Error("readiness: reading self response")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if string(body) != opts.NodeName {
			l.WithFields(logrus.Fields{"node_name": opts.NodeName, "response": string(body)}).
				Error("readiness: self response does not match expected node name")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if opts.Attestation != nil && local != nil && remote != nil {
			src, _ := netip.AddrFromSlice(local.IP.To4())
			dst, _ := netip.AddrFromSlice(remote.IP.To4())
			if err := opts.Attestation.Verify(src.Unmap(), dst.Unmap(), uint16(local.Port), uint16(remote.Port)); err != nil {
				l.WithError(err).Error("readiness: attestation pipeline did not capture self-connection")
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
		}
		w.WriteHeader(http.StatusOK)
	})

	done := make(chan struct{}, 2)

	// start metadata server
	l.Info("starting metadata server...")
	go func() {
		var lis net.Listener
		var err error
		if opts.RoutingMode != api.RoutingModeBPF {
			lis, err = net.Listen("tcp", s.metadataServer.Addr)
		} else {
			lis, err = proxy.Listen(s.metadataServer.Addr, proxyDialLatencyMillis, proxyActiveConnections)
		}
		if err != nil {
			l.WithError(err).Fatal("error listening on metadata server address")
		}

		done <- struct{}{}
		if err := s.metadataServer.Serve(lis); err != nil && !errors.Is(err, http.ErrServerClosed) {
			l.WithError(err).Fatal("error serving metadata server")
		}
	}()

	// start health server
	l.Info("starting health server...")
	go func() {
		done <- struct{}{}
		if err := s.healthServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			l.WithError(err).Fatal("error listening and serving health server")
		}
	}()

	// wait for servers to start
	<-done
	<-done
	l.Info("servers started successfully")

	return s
}

func (s *Server) Shutdown(ctx context.Context) error {
	e1 := s.metadataServer.Shutdown(ctx)
	e2 := s.healthServer.Shutdown(ctx)
	if e1 == nil {
		return e2
	}
	if e2 == nil {
		return e1
	}
	return errors.Join(e1, e2)
}
