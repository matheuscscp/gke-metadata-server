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
