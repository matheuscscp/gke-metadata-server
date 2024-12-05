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

package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"

	pkghttp "github.com/matheuscscp/gke-metadata-server/internal/http"
	"github.com/matheuscscp/gke-metadata-server/internal/logging"
	"github.com/matheuscscp/gke-metadata-server/internal/metrics"
	"github.com/matheuscscp/gke-metadata-server/internal/node"
	"github.com/matheuscscp/gke-metadata-server/internal/pods"
	"github.com/matheuscscp/gke-metadata-server/internal/serviceaccounts"
	"github.com/matheuscscp/gke-metadata-server/internal/serviceaccounttokens"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
)

type (
	Server struct {
		opts       ServerOptions
		httpServer *http.Server
		metrics    serverMetrics
	}

	ServerOptions struct {
		NodeName                  string
		ServerAddr                string
		Pods                      pods.Provider
		Node                      node.Provider
		ServiceAccounts           serviceaccounts.Provider
		ServiceAccountTokens      serviceaccounttokens.Provider
		MetricsRegistry           *prometheus.Registry
		DefaultNodeServiceAccount *serviceaccounts.Reference
		WorkloadIdentityPool      string
	}

	serverMetrics struct {
		lookupPodFailures *prometheus.CounterVec
		getNodeFailures   prometheus.Counter
	}
)

const (
	gkeNodeNameAPI               = "/computeMetadata/v1/instance/name"
	gkeServiceAccountAliasesAPI  = "/computeMetadata/v1/instance/service-accounts/$service_account/aliases"
	gkeServiceAccountEmailAPI    = "/computeMetadata/v1/instance/service-accounts/$service_account/email"
	gkeServiceAccountIdentityAPI = "/computeMetadata/v1/instance/service-accounts/$service_account/identity"
	gkeServiceAccountScopesAPI   = "/computeMetadata/v1/instance/service-accounts/$service_account/scopes"
	gkeServiceAccountTokenAPI    = "/computeMetadata/v1/instance/service-accounts/$service_account/token"
)

func New(ctx context.Context, opts ServerOptions) *Server {
	const subsystem = "" // "server" would stutter with the "gke_metadata_server" namespace
	labelNames := []string{"method", "path", "status"}
	latencyMillis := metrics.NewLatencyMillis(subsystem, labelNames...)
	opts.MetricsRegistry.MustRegister(latencyMillis)
	observeLatencyMillis := func(r *http.Request, statusCode int, latencyMs float64) {
		latencyMillis.
			WithLabelValues(r.Method, r.URL.Path, fmt.Sprint(statusCode)).
			Observe(latencyMs)
	}

	lookupPodFailures := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metrics.Namespace,
		Name:      "lookup_pod_failures_total",
		Help:      "Total failures when looking up Pod objects by IP to serve requests.",
	}, []string{"client_ip"})
	opts.MetricsRegistry.MustRegister(lookupPodFailures)

	getNodeFailures := prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: metrics.Namespace,
		Name:      "get_node_failures_total",
		Help:      "Total failures when getting the current Node object to serve requests.",
	})
	opts.MetricsRegistry.MustRegister(getNodeFailures)

	// create server
	l := logging.FromContext(ctx).WithField("server_addr", opts.ServerAddr)
	metadataDirectory := &pkghttp.DirectoryHandler{}
	internalServeMux := http.NewServeMux()
	s := &Server{
		opts: opts,
		metrics: serverMetrics{
			lookupPodFailures: lookupPodFailures,
			getNodeFailures:   getNodeFailures,
		},
		httpServer: &http.Server{
			Addr: opts.ServerAddr,
			BaseContext: func(net.Listener) context.Context {
				return logging.IntoContext(context.Background(), l)
			},
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				r = pkghttp.InitRequest(r, observeLatencyMillis)

				r = logging.IntoRequest(r, logging.FromRequest(r).WithField("http_request", logrus.Fields{
					"method": r.Method,
					"path":   r.URL.Path,
					"query": logrus.Fields{
						"pretty":   pkghttp.Pretty(r),
						"audience": r.URL.Query().Get("audience"),
					},
				}))

				// internalServeMux path?
				routeDetector := httptest.NewRecorder()
				internalServeMux.ServeHTTP(routeDetector, r)
				if statusCode := routeDetector.Result().StatusCode; 200 <= statusCode && statusCode < 300 {
					internalServeMux.ServeHTTP(w, r)
					return
				}

				// metadataDirectory path
				metadataDirectory.ServeHTTP(w, r)
			}),
		},
	}

	// gke apis
	metadataFlavorMiddleware := func(next http.HandlerFunc) http.Handler {
		const (
			metadataFlavorHeader = "Metadata-Flavor"
			metadataFlavorGoogle = "Google"
		)

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			metadataFlavor := r.Header.Get(metadataFlavorHeader)
			if metadataFlavor == metadataFlavorGoogle {
				w.Header().Set(metadataFlavorHeader, metadataFlavorGoogle)
				w.Header().Set("Server", "GKE Metadata Server")
				next.ServeHTTP(w, r)
				return
			}

			msg := fmt.Sprintf("Missing required header %q: %q\n", metadataFlavorHeader, metadataFlavorGoogle)
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(msg))
		})
	}
	metadataDirectory.Handle(gkeNodeNameAPI, metadataFlavorMiddleware(s.gkeNodeNameAPI))
	metadataDirectory.Handle(gkeServiceAccountAliasesAPI, metadataFlavorMiddleware(s.gkeServiceAccountAliasesAPI),
		s.listPodGoogleServiceAccounts)
	metadataDirectory.Handle(gkeServiceAccountEmailAPI, metadataFlavorMiddleware(s.gkeServiceAccountEmailAPI),
		s.listPodGoogleServiceAccounts)
	metadataDirectory.Handle(gkeServiceAccountIdentityAPI, metadataFlavorMiddleware(s.gkeServiceAccountIdentityAPI),
		s.listPodGoogleServiceAccounts)
	metadataDirectory.Handle(gkeServiceAccountScopesAPI, metadataFlavorMiddleware(s.gkeServiceAccountScopesAPI),
		s.listPodGoogleServiceAccounts)
	metadataDirectory.Handle(gkeServiceAccountTokenAPI, metadataFlavorMiddleware(s.gkeServiceAccountTokenAPI),
		s.listPodGoogleServiceAccounts)

	l.WithField("metadata_directory", metadataDirectory).Info("metadata directory")

	// internal endpoints
	health := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pkghttp.RespondJSON(w, r, http.StatusOK, metadataDirectory)
	})
	internalServeMux.Handle("/schema", health)
	internalServeMux.Handle("/healthz", health)
	internalServeMux.Handle("/health", health)
	internalServeMux.Handle("/readyz", health)
	internalServeMux.Handle("/ready", health)
	internalServeMux.Handle("/metrics", metrics.HandlerFor(opts.MetricsRegistry, l))

	// start server
	go func() {
		l.Info("starting server...")
		if err := s.httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			l.WithError(err).Fatal("error listening and serving server")
		}
	}()

	return s
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}
