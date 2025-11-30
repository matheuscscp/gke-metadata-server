// Copyright 2025 Matheus Pimenta.
// SPDX-License-Identifier: AGPL-3.0

package metrics

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const namespace = "gke_metadata_server"

var processStartTime = time.Now()

func NewRegistry() *prometheus.Registry {
	r := prometheus.NewRegistry()
	r.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	r.MustRegister(collectors.NewGoCollector())
	return r
}

func HandlerFor(registry *prometheus.Registry, l promhttp.Logger) http.Handler {
	return promhttp.HandlerFor(registry, promhttp.HandlerOpts{
		ErrorLog:          l,
		EnableOpenMetrics: true,
		ProcessStartTime:  processStartTime,
	})
}

func NewRequestLatencyMillis() *prometheus.HistogramVec {
	return prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "request_latency_millis",
		Buckets:   prometheus.ExponentialBuckets(0.2, 5, 7),
	}, []string{"method", "path", "status"})
}

func NewLookupPodFailuresCounter() *prometheus.CounterVec {
	return prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "lookup_pod_failures_total",
		Help:      "Total failures when looking up Pod objects by IP to serve requests.",
	}, []string{"client_ip"})
}

func NewGetNodeFailuresCounter() prometheus.Counter {
	return prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "get_node_failures_total",
		Help:      "Total failures when getting the current Node object to serve requests.",
	})
}

func NewRemoveTaintsFailuresCounter() *prometheus.CounterVec {
	return prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "remove_taints_failures_total",
		Help:      "Total failures when removing taints from the Node object.",
	}, []string{"node_name"})
}

func NewProxyDialLantencyMillis() *prometheus.HistogramVec {
	return prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Subsystem: "proxy",
		Name:      "dial_latency_millis",
		Buckets:   prometheus.ExponentialBuckets(0.2, 5, 7),
	}, []string{"client_ip"})
}

func NewProxyActiveConnectionsGauge() prometheus.Gauge {
	return prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Subsystem: "proxy",
		Name:      "active_connections",
		Help:      "Current number of active connections being proxied.",
	})
}

func NewCachedPodsGauge() prometheus.Gauge {
	return prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "pods",
		Help:      "Amount of Pod objects currently cached.",
	})
}

func NewPodCacheMissesCounter() prometheus.Counter {
	return prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "pod_cache_misses_total",
		Help:      "Total amount cache misses when looking up Pod objects.",
	})
}

func NewCachedServiceAccountsGauge() prometheus.Gauge {
	return prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "service_accounts",
		Help:      "Amount of ServiceAccount objects currently cached.",
	})
}

func NewServiceAccountCacheMissesCounter() prometheus.Counter {
	return prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "service_account_cache_misses_total",
		Help:      "Total amount cache misses when looking up ServiceAccount objects.",
	})
}

func NewCachedServiceAccountTokensGauge() prometheus.Gauge {
	return prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "service_account_tokens",
		Help:      "Amount of ServiceAccount tokens currently cached.",
	})
}

func NewServiceAccountTokenCacheMissesCounter() prometheus.Counter {
	return prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "service_account_token_cache_misses_total",
		Help:      "Total amount cache misses when fetching ServiceAccount tokens.",
	})
}
