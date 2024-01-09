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
	"strings"
	"sync"
	"time"

	"github.com/matheuscscp/gke-metadata-server/internal/logging"
	"github.com/matheuscscp/gke-metadata-server/internal/metrics"
	"github.com/matheuscscp/gke-metadata-server/internal/serviceaccounts"
	"github.com/matheuscscp/gke-metadata-server/internal/serviceaccounttokens"

	"github.com/golang-jwt/jwt/v5"
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
		mu              sync.RWMutex
		wg              sync.WaitGroup
		semaphore       chan struct{}
	}

	ProviderOptions struct {
		Source           serviceaccounttokens.Provider
		ServiceAccounts  serviceaccounts.Provider
		MetricsSubsystem string
		MetricsRegistry  *prometheus.Registry
		Concurrency      int
	}

	serviceAccountName struct {
		Namespace string `json:"namespace"`
		Name      string `json:"name"`
	}

	serviceAccount struct {
		serviceAccountName
		podCount              int
		token                 string
		accessToken           string
		tokenExpiration       time.Time
		accessTokenExpiration time.Time
		externalRequests      chan chan struct{}
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
		semaphore:       make(chan struct{}, opts.Concurrency),
	}
}

func (p *Provider) Close() error {
	p.cancelCtx()
	p.wg.Wait()
	return nil
}

func (p *Provider) GetServiceAccountToken(ctx context.Context, namespace, name string) (string, time.Duration, error) {
	k := serviceAccountName{namespace, name}

	p.mu.Lock()
	sa, ok := p.serviceAccounts[k]
	if !ok {
		sa = p.addServiceAccount(k, 0 /*podCount*/)
	}
	p.mu.Unlock()

	if sa.token == "" || time.Now().After(sa.tokenExpiration) {
		p.cacheMisses.Inc()
		if err := sa.wakeUpCacher(ctx); err != nil {
			return "", 0, err
		}
	}

	return sa.token, time.Until(sa.tokenExpiration), nil
}

func (p *Provider) GetGoogleAccessToken(ctx context.Context, saToken, googleEmail string) (string, time.Duration, error) {
	// here we know the token is valid, we're only extracting the sa name
	tok, _, _ := jwt.NewParser().ParseUnverified(saToken, jwt.MapClaims{})
	sub, _ := tok.Claims.GetSubject()
	s := strings.Split(sub, ":") // system:serviceaccount:{namespace}:{name}
	k := serviceAccountName{s[2], s[3]}

	p.mu.Lock()
	sa, ok := p.serviceAccounts[k]
	if !ok {
		sa = p.addServiceAccount(k, 0 /*podCount*/)
	}
	p.mu.Unlock()

	if sa.accessToken == "" || time.Now().After(sa.accessTokenExpiration) {
		p.cacheMisses.Inc()
		if err := sa.wakeUpCacher(ctx); err != nil {
			return "", 0, err
		}
	}

	return sa.accessToken, time.Until(sa.accessTokenExpiration), nil
}

func (p *Provider) GetGoogleIdentityToken(ctx context.Context, saToken, googleEmail, audience string) (string, time.Duration, error) {
	// we dont cache identity tokens for now since they depend on external input (the target audience)
	return p.opts.Source.GetGoogleIdentityToken(ctx, saToken, googleEmail, audience)
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

func (p *Provider) UpdateServiceAccount(ksa *corev1.ServiceAccount) {
	k := serviceAccountName{ksa.Namespace, ksa.Name}

	// here we use a read lock because we are watching all the service
	// accounts of the cluster, so a bit of optimization is appreciated
	p.mu.RLock()
	defer p.mu.RUnlock()

	sa, ok := p.serviceAccounts[k]
	if !ok {
		return
	}

	select {
	case sa.externalRequests <- nil:
	default:
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

		p.semaphore <- struct{}{}

		var sleepDuration time.Duration

		// first, cache service account token
		token, tokenDuration, err := p.opts.Source.GetServiceAccountToken(p.ctx, s.Namespace, s.Name)
		if err != nil {
			sleepDuration = (1 << retries) * time.Second
			l.WithError(err).Errorf("error creating token for kubernetes service account, will retry after %s...", sleepDuration.String())

			if retries < 5 {
				retries++
			}
		} else {
			s.token, s.tokenExpiration = token, time.Now().Add(tokenDuration)
			l.WithField("token_duration", tokenDuration.String()).Info("cached service account token")

			// success, cache google access token as well
			ksa, err := p.opts.ServiceAccounts.Get(p.ctx, s.Namespace, s.Name)
			if err != nil {
				l.WithError(err).Error("error getting service account for caching google access token")
			} else if email, err := serviceaccounts.GoogleEmail(ksa); err == nil {
				l := l.WithField("google_email", email)
				accessToken, accessTokenDuration, err := p.opts.Source.GetGoogleAccessToken(p.ctx, token, email)
				if err != nil {
					l.WithError(err).Error("error creating google access token from kubernetes service account token")
				} else {
					s.accessToken, s.accessTokenExpiration = accessToken, time.Now().Add(accessTokenDuration)
					l.WithField("token_duration", accessTokenDuration.String()).Info("cached google access token")
				}
			}

			// notify external request
			if externalRequest != nil {
				close(externalRequest)
				externalRequest = nil
			}

			retries = 0

			sleepDuration = tokenDuration
			const safeDistance = time.Minute
			if sleepDuration >= safeDistance {
				sleepDuration -= safeDistance
			}
		}

		<-p.semaphore

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

func (s *serviceAccount) wakeUpCacher(ctx context.Context) error {
	req := make(chan struct{})
	select {
	case s.externalRequests <- req:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-req:
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}
