// MIT License
//
// Copyright (c) 2024 Matheus Pimenta
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

package watchpods

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/matheuscscp/gke-metadata-server/internal/logging"
	"github.com/matheuscscp/gke-metadata-server/internal/metrics"
	"github.com/matheuscscp/gke-metadata-server/internal/pods"

	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	informersv1 "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

type (
	Provider struct {
		opts          ProviderOptions
		numPods       prometheus.Gauge
		cacheMisses   prometheus.Counter
		closeChannel  chan struct{}
		closedChannel chan struct{}
		informer      cache.SharedIndexInformer
		listeners     []Listener
	}

	ProviderOptions struct {
		NodeName         string
		MetricsSubsystem string
		FallbackSource   pods.Provider
		KubeClient       *kubernetes.Clientset
		MetricsRegistry  *prometheus.Registry
		ResyncPeriod     time.Duration
	}

	Listener interface {
		AddPod(pod *corev1.Pod)
		DeletePod(pod *corev1.Pod)
	}

	ListenerFuncs struct {
		AddFunc    func(pod *corev1.Pod)
		DeleteFunc func(pod *corev1.Pod)
	}

	listenerFuncs struct{ ListenerFuncs }
)

const ipIndex = "ip"

func NewProvider(opts ProviderOptions) *Provider {
	numPods := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: metrics.Namespace,
		Subsystem: opts.MetricsSubsystem,
		Name:      "pods",
		Help:      "Amount of Pod objects currently cached.",
	})
	opts.MetricsRegistry.MustRegister(numPods)
	cacheMisses := prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: metrics.Namespace,
		Subsystem: opts.MetricsSubsystem,
		Name:      "pod_cache_misses_total",
		Help:      "Total amount cache misses when looking up Pod objects.",
	})
	opts.MetricsRegistry.MustRegister(cacheMisses)

	informer := informersv1.NewFilteredPodInformer(
		opts.KubeClient,
		corev1.NamespaceAll,
		opts.ResyncPeriod,
		cache.Indexers{ // pods on the host network are not supported, see README.md
			ipIndex: func(obj interface{}) ([]string, error) {
				pod := obj.(*corev1.Pod)
				return []string{pod.Status.PodIP, fmt.Sprint(pod.Spec.HostNetwork)}, nil
			},
		},
		func(lo *metav1.ListOptions) {
			lo.FieldSelector = fmt.Sprintf("spec.nodeName=%s", opts.NodeName)
		},
	)

	p := &Provider{
		opts:          opts,
		numPods:       numPods,
		cacheMisses:   cacheMisses,
		closeChannel:  make(chan struct{}),
		closedChannel: make(chan struct{}),
		informer:      informer,
	}

	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			numPods.Inc()
			pod := obj.(*corev1.Pod)
			if pod.Spec.HostNetwork {
				return
			}
			for _, l := range p.listeners {
				l.AddPod(pod)
			}
		},
		DeleteFunc: func(obj interface{}) {
			numPods.Dec()
			pod := obj.(*corev1.Pod)
			if pod.Spec.HostNetwork {
				return
			}
			for _, l := range p.listeners {
				l.DeletePod(pod)
			}
		},
	})

	return p
}

func (p *Provider) GetByIP(ctx context.Context, ipAddr string) (*corev1.Pod, error) {
	pod, err := p.getByIP(ctx, ipAddr)
	if err == nil {
		return pod, nil
	}
	if p.opts.FallbackSource == nil {
		return nil, fmt.Errorf("error getting pod by ip '%s' from cache: %w", ipAddr, err)
	}
	p.cacheMisses.Inc()
	logging.
		FromContext(ctx).
		WithError(err).
		Error("error getting pod by ip from cache, delegating request to fallback source")
	return p.opts.FallbackSource.GetByIP(ctx, ipAddr)
}

func (p *Provider) getByIP(ctx context.Context, ipAddr string) (*corev1.Pod, error) {
	v, err := p.informer.GetIndexer().Index(ipIndex, &corev1.Pod{
		Status: corev1.PodStatus{PodIP: ipAddr},
	})
	if err != nil {
		return nil, fmt.Errorf("error getting pod from cache by ip address '%s': %w", ipAddr, err)
	}
	if n := len(v); n != 1 {
		refs := make([]string, n)
		for i, p := range v {
			pod := p.(*corev1.Pod)
			refs[i] = fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)
		}
		return nil, fmt.Errorf("error getting pod from cache by ip address '%s': %v pods found instead of 1 [%s]",
			ipAddr, n, strings.Join(refs, ", "))
	}
	return v[0].(*corev1.Pod), nil
}

func (p *Provider) Start(ctx context.Context) {
	go func() {
		p.informer.Run(p.closeChannel)
		close(p.closedChannel)
	}()
	logging.FromContext(ctx).Info("watch pods started")
}

func (p *Provider) Close() error {
	close(p.closeChannel)
	<-p.closedChannel
	return nil
}

func (p *Provider) AddListener(l Listener) {
	p.listeners = append(p.listeners, l)
}

func (p *Provider) AddListenerFuncs(lf ListenerFuncs) {
	p.AddListener(&listenerFuncs{lf})
}

func (l *listenerFuncs) AddPod(pod *corev1.Pod) {
	if l.AddFunc != nil {
		l.AddFunc(pod)
	}
}

func (l *listenerFuncs) DeletePod(pod *corev1.Pod) {
	if l.DeleteFunc != nil {
		l.DeleteFunc(pod)
	}
}
