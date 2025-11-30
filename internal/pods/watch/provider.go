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
	"github.com/matheuscscp/gke-metadata-server/internal/serviceaccounts"

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
		NodeName        string
		FallbackSource  pods.Provider
		KubeClient      *kubernetes.Clientset
		MetricsRegistry *prometheus.Registry
		ResyncPeriod    time.Duration
	}

	Listener interface {
		AddPodServiceAccount(*serviceaccounts.Reference)
		DeletePodServiceAccount(*serviceaccounts.Reference)
	}
)

const ipIndex = "ip"

func NewProvider(opts ProviderOptions) *Provider {
	numPods := metrics.NewCachedPodsGauge()
	opts.MetricsRegistry.MustRegister(numPods)
	cacheMisses := metrics.NewPodCacheMissesCounter()
	opts.MetricsRegistry.MustRegister(cacheMisses)

	informer := informersv1.NewFilteredPodInformer(
		opts.KubeClient,
		corev1.NamespaceAll,
		opts.ResyncPeriod,
		cache.Indexers{
			ipIndex: func(obj any) ([]string, error) {
				if podIP := obj.(*corev1.Pod).Status.PodIP; podIP != "" {
					return []string{podIP}, nil
				}
				return nil, nil
			},
		},
		func(lo *metav1.ListOptions) {
			lo.FieldSelector = strings.Join([]string{
				"spec.nodeName=" + opts.NodeName,
				"spec.hostNetwork=false",
			}, ",")
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
		AddFunc: func(obj any) {
			numPods.Inc()
			saRef := serviceaccounts.ReferenceFromPod(obj.(*corev1.Pod))
			for _, l := range p.listeners {
				l.AddPodServiceAccount(saRef)
			}
		},
		DeleteFunc: func(obj any) {
			numPods.Dec()
			saRef := serviceaccounts.ReferenceFromPod(obj.(*corev1.Pod))
			for _, l := range p.listeners {
				l.DeletePodServiceAccount(saRef)
			}
		},
	})

	return p
}

func (p *Provider) GetByIP(ctx context.Context, ipAddr string) (*corev1.Pod, error) {
	pod, err := p.getByIP(ipAddr)
	if err == nil {
		return pod, nil
	}
	if p.opts.FallbackSource == nil {
		return nil, fmt.Errorf("error getting pod with cluster ip %s from cache: %w", ipAddr, err)
	}

	logging.
		FromContext(ctx).
		WithError(err).
		WithField("cluster_ip", ipAddr).
		Error("error getting pod by cluster ip from cache, delegating request to fallback source")

	pod, err = p.opts.FallbackSource.GetByIP(ctx, ipAddr)
	if err != nil {
		return nil, err
	}
	p.cacheMisses.Inc()
	return pod, nil
}

func (p *Provider) getByIP(ipAddr string) (*corev1.Pod, error) {
	list, err := p.informer.GetIndexer().Index(ipIndex, &corev1.Pod{
		Status: corev1.PodStatus{PodIP: ipAddr},
	})
	if err != nil {
		return nil, fmt.Errorf("cache: error listing pods in the node matching cluster ip %s: %w", ipAddr, err)
	}
	list = pods.FilterPods(list)

	if n := len(list); n != 1 {
		if n == 0 {
			return nil, fmt.Errorf("cache: no pods found in the node matching cluster ip %s", ipAddr)
		}

		refs := make([]string, n)
		for i, v := range list {
			pod := v.(*corev1.Pod)
			refs[i] = fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)
		}
		return nil, fmt.Errorf("cache: multiple pods found in the node matching cluster ip %s (%v pods): %s",
			ipAddr, n, strings.Join(refs, ", "))
	}

	return list[0].(*corev1.Pod), nil
}

func (p *Provider) Start(ctx context.Context) {
	go func() {
		logging.FromContext(ctx).Info("starting watch pods...")
		p.informer.Run(p.closeChannel)
		close(p.closedChannel)
	}()
}

func (p *Provider) Close() error {
	close(p.closeChannel)
	<-p.closedChannel
	return nil
}

func (p *Provider) AddListener(l Listener) {
	p.listeners = append(p.listeners, l)
}
