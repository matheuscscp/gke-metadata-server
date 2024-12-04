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

const Namespace = "gke_metadata_server"

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

func NewLatencyMillis(labelNames []string) *prometheus.HistogramVec {
	return prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: Namespace,
		Name:      "request_latency_millis",
		Buckets:   prometheus.ExponentialBuckets(0.2, 5, 7),
	}, labelNames)
}
