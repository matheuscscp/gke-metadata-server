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

package cacheserviceaccounttokens

import (
	"context"
	"sync"
	"time"

	"github.com/matheuscscp/gke-metadata-server/internal/logging"
	"github.com/matheuscscp/gke-metadata-server/internal/metrics"
	"github.com/matheuscscp/gke-metadata-server/internal/serviceaccounttokens"

	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
)

type (
	Provider struct {
		opts            ProviderOptions
		numTokens       prometheus.Gauge
		cacheMisses     prometheus.Counter
		serviceAccounts map[serviceAccountName]*serviceAccount
		ctx             context.Context
		cancelCtx       context.CancelFunc
		mu              sync.Mutex
		wg              sync.WaitGroup
	}

	ProviderOptions struct {
		Source           serviceaccounttokens.Provider
		Audience         string
		MetricsSubsystem string
		MetricsRegistry  *prometheus.Registry
	}

	serviceAccountName struct {
		Namespace string `json:"namespace"`
		Name      string `json:"name"`
	}

	serviceAccount struct {
		serviceAccountName
		podCount         int
		token            string
		tokenExpiration  time.Time
		externalRequests chan chan struct{}
	}
)

func NewProvider(ctx context.Context, opts ProviderOptions) *Provider {
	numTokens := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: metrics.Namespace,
		Subsystem: opts.MetricsSubsystem,
		Name:      "service_account_tokens",
		Help:      "Amount of ServiceAccount tokens currently cached.",
	})
	opts.MetricsRegistry.MustRegister(numTokens)
	cacheMisses := prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: metrics.Namespace,
		Subsystem: opts.MetricsSubsystem,
		Name:      "service_account_token_cache_misses_total",
		Help:      "Total amount cache misses when fetching ServiceAccount tokens.",
	})
	opts.MetricsRegistry.MustRegister(cacheMisses)

	newCtxWithLogger := logging.IntoContext(context.Background(), logging.FromContext(ctx))
	ctx, cancel := context.WithCancel(newCtxWithLogger)
	return &Provider{
		opts:            opts,
		numTokens:       numTokens,
		cacheMisses:     cacheMisses,
		serviceAccounts: make(map[serviceAccountName]*serviceAccount),
		ctx:             ctx,
		cancelCtx:       cancel,
	}
}

func (p *Provider) Close() error {
	p.cancelCtx()
	p.wg.Wait()
	return nil
}

func (p *Provider) Create(ctx context.Context, namespace, name string) (string, time.Duration, error) {
	k := serviceAccountName{namespace, name}

	p.mu.Lock()
	sa, ok := p.serviceAccounts[k]
	if !ok {
		sa = p.addServiceAccount(k, 0 /*podCount*/)
	}
	p.mu.Unlock()

	if sa.token == "" || time.Now().After(sa.tokenExpiration) {
		p.cacheMisses.Inc()
		req := make(chan struct{})
		select {
		case sa.externalRequests <- req:
		case <-ctx.Done():
			return "", 0, ctx.Err()
		}
		select {
		case <-req:
		case <-ctx.Done():
			return "", 0, ctx.Err()
		}
	}

	return sa.token, time.Until(sa.tokenExpiration), nil
}

func (p *Provider) addServiceAccount(k serviceAccountName, podCount int) *serviceAccount {
	sa := &serviceAccount{
		serviceAccountName: k,
		podCount:           podCount,
		externalRequests:   make(chan chan struct{}, 1),
	}
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		sa.cache(p)
	}()
	p.serviceAccounts[k] = sa
	p.numTokens.Inc()
	return sa
}

func podServiceAccountName(pod *corev1.Pod) serviceAccountName {
	return serviceAccountName{
		Namespace: pod.Namespace,
		Name:      pod.Spec.ServiceAccountName,
	}
}

func (p *Provider) AddPod(pod *corev1.Pod) {
	k := podServiceAccountName(pod)

	p.mu.Lock()
	defer p.mu.Unlock()

	sa, ok := p.serviceAccounts[k]
	if ok {
		sa.podCount++
		return
	}

	p.addServiceAccount(k, 1 /*podCount*/)
}

func (p *Provider) DeletePod(pod *corev1.Pod) {
	k := podServiceAccountName(pod)

	p.mu.Lock()
	defer p.mu.Unlock()

	sa, ok := p.serviceAccounts[k]
	if ok && sa.podCount > 0 {
		sa.podCount--
	}
}

func (s *serviceAccount) cache(p *Provider) {
	l := logging.FromContext(p.ctx).WithField("service_account", s.serviceAccountName)

	var retries int
	var externalRequest chan struct{}
	for {
		p.mu.Lock()
		if s.podCount == 0 {
			delete(p.serviceAccounts, s.serviceAccountName)
			p.numTokens.Dec()
			p.mu.Unlock()
			return
		}
		p.mu.Unlock()

		token, sleepDuration, err := p.opts.Source.Create(p.ctx, s.Namespace, s.Name)
		if err != nil {
			sleepDuration = (1 << retries) * time.Second
			l.WithError(err).Errorf("error creating token for kubernetes service account, will retry after %s...", sleepDuration.String())

			if retries < 5 {
				retries++
			}
		} else {
			s.token, s.tokenExpiration = token, time.Now().Add(sleepDuration)
			l.WithField("token_duration", sleepDuration.String()).Info("created and cached service account token")

			if externalRequest != nil {
				close(externalRequest) // notify external request
				externalRequest = nil
			}

			retries = 0

			const safeDistance = time.Minute
			if sleepDuration >= safeDistance {
				sleepDuration -= safeDistance
			}
		}

		// sleep
		t := time.NewTimer(sleepDuration)
		select {
		case <-p.ctx.Done():
			t.Stop()
			return
		case <-t.C:
		case externalRequest = <-s.externalRequests:
		}
		t.Stop()
	}
}
