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
	"context"
	"net/http"
	"time"

	"github.com/matheuscscp/gke-metadata-server/internal/logging"
	pkgtime "github.com/matheuscscp/gke-metadata-server/internal/time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/client_golang/prometheus/push"
	"github.com/sirupsen/logrus"
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

func StartPusher(registry *prometheus.Registry, url, jobName, podName, podNamespace string) context.CancelFunc {
	ctx, cancel := context.WithCancel(context.Background())
	pusher := push.
		New(url, jobName).
		Gatherer(registry).
		Grouping("pod_name", podName).
		Grouping("pod_namespace", podNamespace)
	l := logging.
		FromContext(ctx).
		WithField("pushgateway_details", logrus.Fields{
			"url":      url,
			"job_name": jobName,
			"groupings": logrus.Fields{
				"pod_name":      podName,
				"pod_namespace": podNamespace,
			},
		})

	closed := make(chan struct{})
	go func() {
		defer func() {
			l.Info("metrics pusher stop requested, deleting metrics...")
			if err := pusher.Delete(); err != nil {
				l.WithError(err).
					Error("error deleting metrics. applications using prometheus pushgateway must delete their metrics, " +
						"otherwise they will remain frozen in the last state right before the application died until " +
						"pushgateway is restarted. please check what happened and take the appropriate action")
			}
			l.Info("metrics pusher stopped")
			close(closed)
		}()
		for {
			if pkgtime.SleepContext(ctx, 30*time.Second) != nil {
				return
			}
			if err := pusher.PushContext(ctx); err != nil {
				if ctx.Err() != nil {
					return
				}
				l.WithError(err).Error("error pushing metrics")
			}
		}
	}()
	l.Info("metrics pusher started")
	return func() {
		cancel()
		<-closed
	}
}

func NewLatencyMillis(subsystem string, labelNames []string) *prometheus.HistogramVec {
	return prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: Namespace,
		Subsystem: subsystem,
		Name:      "request_latency_millis",
		Buckets:   prometheus.ExponentialBuckets(0.2, 5, 7),
	}, labelNames)
}
