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

package watchserviceaccounts

import (
	"context"
	"fmt"
	"time"

	"github.com/matheuscscp/gke-metadata-server/internal/logging"
	"github.com/matheuscscp/gke-metadata-server/internal/metrics"
	"github.com/matheuscscp/gke-metadata-server/internal/serviceaccounts"

	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	informersv1 "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

type (
	Provider struct {
		opts               ProviderOptions
		numServiceAccounts prometheus.Gauge
		cacheMisses        prometheus.Counter
		closeChannel       chan struct{}
		closedChannel      chan struct{}
		informer           cache.SharedIndexInformer
		listeners          []Listener
	}

	ProviderOptions struct {
		FallbackSource  serviceaccounts.Provider
		KubeClient      *kubernetes.Clientset
		MetricsRegistry *prometheus.Registry
		ResyncPeriod    time.Duration
	}

	Listener interface {
		UpdateServiceAccount(*serviceaccounts.Reference)
		DeleteServiceAccount(*serviceaccounts.Reference)
	}
)

func NewProvider(ctx context.Context, opts ProviderOptions) *Provider {
	numServiceAccounts := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: metrics.Namespace,
		Name:      "service_accounts",
		Help:      "Amount of ServiceAccount objects currently cached.",
	})
	opts.MetricsRegistry.MustRegister(numServiceAccounts)
	cacheMisses := prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: metrics.Namespace,
		Name:      "service_account_cache_misses_total",
		Help:      "Total amount cache misses when looking up ServiceAccount objects.",
	})
	opts.MetricsRegistry.MustRegister(cacheMisses)

	informer := informersv1.NewServiceAccountInformer(
		opts.KubeClient,
		corev1.NamespaceAll,
		opts.ResyncPeriod,
		cache.Indexers{},
	)

	p := &Provider{
		opts:               opts,
		numServiceAccounts: numServiceAccounts,
		cacheMisses:        cacheMisses,
		closeChannel:       make(chan struct{}),
		closedChannel:      make(chan struct{}),
		informer:           informer,
	}

	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			numServiceAccounts.Inc()
			saRef := serviceaccounts.ReferenceFromObject(obj.(*corev1.ServiceAccount))
			for _, l := range p.listeners {
				l.UpdateServiceAccount(saRef)
			}
		},
		UpdateFunc: func(oldObj, newObj any) {
			saRef := serviceaccounts.ReferenceFromObject(newObj.(*corev1.ServiceAccount))
			for _, l := range p.listeners {
				l.UpdateServiceAccount(saRef)
			}
		},
		DeleteFunc: func(obj any) {
			numServiceAccounts.Dec()
			saRef := serviceaccounts.ReferenceFromObject(obj.(*corev1.ServiceAccount))
			for _, l := range p.listeners {
				l.DeleteServiceAccount(saRef)
			}
		},
	})

	return p
}

func (p *Provider) Get(ctx context.Context, ref *serviceaccounts.Reference) (*corev1.ServiceAccount, error) {
	namespace, name := ref.Namespace, ref.Name

	sa, err := p.get(namespace, name)
	if err == nil {
		return sa, nil
	}
	if p.opts.FallbackSource == nil {
		return nil, fmt.Errorf("error getting service account %s/%s from cache: %w", namespace, name, err)
	}

	logging.
		FromContext(ctx).
		WithError(err).
		WithField("service_account", fmt.Sprintf("%s/%s", namespace, name)).
		Error("error getting service account from cache, delegating request to fallback source")

	sa, err = p.opts.FallbackSource.Get(ctx, ref)
	if err != nil {
		return nil, err
	}
	p.cacheMisses.Inc()
	return sa, nil
}

func (p *Provider) get(namespace, name string) (*corev1.ServiceAccount, error) {
	v, ok, err := p.informer.GetStore().GetByKey(fmt.Sprintf("%s/%s", namespace, name))
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("service account not present in cache")
	}
	return v.(*corev1.ServiceAccount), nil
}

func (p *Provider) Start(ctx context.Context) {
	go func() {
		logging.FromContext(ctx).Info("starting watch service accounts...")
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
