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
	"time"

	pkghttp "github.com/matheuscscp/gke-metadata-server/internal/http"
	"github.com/matheuscscp/gke-metadata-server/internal/logging"
	"github.com/matheuscscp/gke-metadata-server/internal/metrics"
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
		TokenExpirationSeconds           int
		NodeName                         string
		DaemonName                       string
		DaemonNamespace                  string
		WorkloadIdentityProviderAudience string
		ServerAddr                       string
		MetricsSubsystem                 string
		Pods                             pods.Provider
		ServiceAccounts                  serviceaccounts.Provider
		ServiceAccountTokens             serviceaccounttokens.Provider
		MetricsRegistry                  *prometheus.Registry
	}

	serverMetrics struct {
		getPodFailures *prometheus.CounterVec
	}
)

const (
	// GCE/GKE-compatible APIs. Enfortunately, not all APIs of the
	// gke-metadata-server can be emulated for non-GKE clusters. below
	// are the ones that can.
	gkeNodeNameAPI               = "/computeMetadata/v1/instance/name"
	gkeServiceAccountAliasesAPI  = "/computeMetadata/v1/instance/service-accounts/$service_account/aliases"
	gkeServiceAccountEmailAPI    = "/computeMetadata/v1/instance/service-accounts/$service_account/email"
	gkeServiceAccountIdentityAPI = "/computeMetadata/v1/instance/service-accounts/$service_account/identity"
	gkeServiceAccountScopesAPI   = "/computeMetadata/v1/instance/service-accounts/$service_account/scopes"
	gkeServiceAccountTokenAPI    = "/computeMetadata/v1/instance/service-accounts/$service_account/token"

	// Emulator-only APIs. Even though Google tools are not aware of the two APIs below,
	// together they make it very easy for a Pod to use the gcloud CLI. All that is
	// required is "curl"ing the first API, storing the returned JSON in a file, then
	// run "gcloud auth login --cred-file=path/to/config.json". This is how the server
	// internally runs authenticated commands, e.g. for implementing the GCP Access and
	// ID Token APIs.
	emuPodGoogleCredConfigAPI    = "/gkeMetadataEmulator/v1/pod/service-account/google-cred-config"
	emuPodServiceAccountTokenAPI = "/gkeMetadataEmulator/v1/pod/service-account/token"
)

func New(ctx context.Context, opts ServerOptions) *Server {
	latencyMillis := metrics.NewLatencyMillis(opts.MetricsSubsystem, []string{"method", "path", "status"})
	opts.MetricsRegistry.MustRegister(latencyMillis)
	getPodFailures := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metrics.Namespace,
		Subsystem: opts.MetricsSubsystem,
		Name:      "get_pod_failures_total",
		Help:      "Total failures when looking up Pod objects to serve requests.",
	}, []string{"client_ip"})
	opts.MetricsRegistry.MustRegister(getPodFailures)

	// create server
	l := logging.FromContext(ctx).WithField("server_addr", opts.ServerAddr)
	metadataDirectory := &pkghttp.DirectoryHandler{}
	internalServeMux := http.NewServeMux()
	s := &Server{
		opts: opts,
		metrics: serverMetrics{
			getPodFailures: getPodFailures,
		},
		httpServer: &http.Server{
			Addr: opts.ServerAddr,
			BaseContext: func(net.Listener) context.Context {
				return logging.IntoContext(context.Background(), l)
			},
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t0 := time.Now()
				r = pkghttp.StartTimeIntoRequest(r, t0)

				statusRecorder := &pkghttp.StatusRecorder{ResponseWriter: w}
				defer func() {
					statusCode := fmt.Sprint(statusRecorder.StatusCode())
					delta := time.Since(t0).Seconds() * 1000
					latencyMillis.WithLabelValues(r.Method, r.URL.Path, statusCode).Observe(delta)
				}()

				w = statusRecorder
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
				if statusCode := statusRecorder.StatusCode(); 200 <= statusCode && statusCode < 300 {
					logging.
						FromRequest(r).
						WithFields(logrus.Fields{
							"latency":       time.Since(t0).String(),
							"http_response": pkghttp.ResponseLogFields(statusCode),
						}).
						Info("request")
				}
			}),
		},
	}

	// gke apis
	metadataDirectory.Handle(gkeNodeNameAPI, googleFlavorMiddleware(s.gkeNodeNameAPI))
	metadataDirectory.Handle(gkeServiceAccountAliasesAPI, googleFlavorMiddleware(s.gkeServiceAccountAliasesAPI),
		s.listPodGoogleServiceAccounts)
	metadataDirectory.Handle(gkeServiceAccountEmailAPI, googleFlavorMiddleware(s.gkeServiceAccountEmailAPI),
		s.listPodGoogleServiceAccounts)
	metadataDirectory.Handle(gkeServiceAccountIdentityAPI, googleFlavorMiddleware(s.gkeServiceAccountIdentityAPI),
		s.listPodGoogleServiceAccounts)
	metadataDirectory.Handle(gkeServiceAccountScopesAPI, googleFlavorMiddleware(s.gkeServiceAccountScopesAPI),
		s.listPodGoogleServiceAccounts)
	metadataDirectory.Handle(gkeServiceAccountTokenAPI, googleFlavorMiddleware(s.gkeServiceAccountTokenAPI),
		s.listPodGoogleServiceAccounts)

	// emulator-only apis
	metadataDirectory.Handle(emuPodGoogleCredConfigAPI, emulatorFlavorMiddleware(s.emuPodGoogleCredConfigAPI))
	metadataDirectory.Handle(emuPodServiceAccountTokenAPI, emulatorFlavorMiddleware(s.emuPodServiceAccountTokenAPI))

	l.WithField("metadata_directory", metadataDirectory).Info("metadata directory")

	// internal endpoints
	internalServeMux.Handle("/schema", s.health(metadataDirectory))
	internalServeMux.Handle("/healthz", s.health(metadataDirectory))
	internalServeMux.Handle("/health", s.health(metadataDirectory))
	internalServeMux.Handle("/readyz", s.health(metadataDirectory))
	internalServeMux.Handle("/ready", s.health(metadataDirectory))
	internalServeMux.Handle("/metrics", metrics.HandlerFor(opts.MetricsRegistry, l))

	// start server
	go func() {
		if err := s.httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			l.WithError(err).Fatal("error listening and serving")
		}
	}()
	l.Info("server started")

	return s
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}
