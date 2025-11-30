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
	"io"
	"net"
	"net/http"
	"time"

	"github.com/matheuscscp/gke-metadata-server/api"
	pkghttp "github.com/matheuscscp/gke-metadata-server/internal/http"
	"github.com/matheuscscp/gke-metadata-server/internal/logging"
	"github.com/matheuscscp/gke-metadata-server/internal/metrics"
	"github.com/matheuscscp/gke-metadata-server/internal/node"
	"github.com/matheuscscp/gke-metadata-server/internal/pods"
	"github.com/matheuscscp/gke-metadata-server/internal/proxy"
	"github.com/matheuscscp/gke-metadata-server/internal/serviceaccounts"
	"github.com/matheuscscp/gke-metadata-server/internal/serviceaccounttokens"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
)

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
		Node                 node.Provider
		ServiceAccounts      serviceaccounts.Provider
		ServiceAccountTokens serviceaccounttokens.Provider
		MetricsRegistry      *prometheus.Registry
		ProjectID            string
		NumericProjectID     string
		WorkloadIdentityPool string
		RoutingMode          string
		PodLookup            PodLookupOptions
	}

	PodLookupOptions struct {
		MaxAttempts       int           // default: 3
		RetryInitialDelay time.Duration // default: time.Second
		RetryMaxDelay     time.Duration // default: 30 * time.Second
	}

	serverMetrics struct {
		lookupPodFailures *prometheus.CounterVec
		getNodeFailures   prometheus.Counter
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

	getNodeFailures := metrics.NewGetNodeFailuresCounter()
	opts.MetricsRegistry.MustRegister(getNodeFailures)

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
			getNodeFailures:   getNodeFailures,
		},
		metadataServer: &http.Server{
			Addr:        opts.Addr,
			BaseContext: baseContext,
			Handler:     observabilityMiddleware(metadataHandler),
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
	healthHandler.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		url := fmt.Sprintf("http://%s%s", opts.Addr, gkeNodeNameAPI)
		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, url, nil)
		if err != nil {
			logging.FromRequest(r).WithError(err).Error("error creating healthcheck request")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		req.Header.Set(pkghttp.MetadataFlavorHeader, pkghttp.MetadataFlavorGoogle)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			logging.FromRequest(r).WithError(err).Error("error performing healthcheck request")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			logging.FromRequest(r).WithField("status_code", resp.StatusCode).Error("healthcheck request failed")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			logging.FromRequest(r).WithError(err).Error("error reading healthcheck response")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if string(b) != opts.NodeName {
			l := logging.FromRequest(r).WithFields(logrus.Fields{
				"node_name": opts.NodeName,
				"response":  string(b),
			})
			l.Error("healthcheck failed, response does not match expected node name")
			w.WriteHeader(http.StatusInternalServerError)
			return
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
