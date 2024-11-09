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
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/matheuscscp/gke-metadata-server/internal/logging"
	"github.com/matheuscscp/gke-metadata-server/internal/metrics"
	"github.com/matheuscscp/gke-metadata-server/internal/serviceaccounts"
	"github.com/matheuscscp/gke-metadata-server/internal/serviceaccounttokens"

	"github.com/prometheus/client_golang/prometheus"
)

type Provider struct {
	opts                  ProviderOptions
	numTokens             prometheus.Gauge
	cacheMisses           prometheus.Counter
	serviceAccounts       map[serviceaccounts.Reference]*serviceAccount
	nodeServiceAccountRef *serviceaccounts.Reference
	ctx                   context.Context
	cancelCtx             context.CancelFunc
	mu                    sync.Mutex
	wg                    sync.WaitGroup
	semaphore             chan struct{}
}

type ProviderOptions struct {
	Source           serviceaccounttokens.Provider
	ServiceAccounts  serviceaccounts.Provider
	MetricsSubsystem string
	MetricsRegistry  *prometheus.Registry
	Concurrency      int
}

var errServiceAccountDeleted = errors.New("service account was deleted")

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
		serviceAccounts: make(map[serviceaccounts.Reference]*serviceAccount),
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

func (p *Provider) GetServiceAccountToken(ctx context.Context, ref *serviceaccounts.Reference) (string, time.Duration, error) {
	tokens, err := p.requestTokens(ctx, ref)
	if err != nil {
		return "", 0, err
	}
	return tokens.token, time.Until(tokens.tokenExpiration), nil
}

func (p *Provider) GetGoogleAccessToken(ctx context.Context, saToken, googleEmail string) (string, time.Duration, error) {
	ref := serviceaccounts.ReferenceFromToken(saToken)
	tokens, err := p.requestTokens(ctx, ref)
	if err != nil {
		return "", 0, err
	}
	return tokens.accessToken, time.Until(tokens.accessTokenExpiration), nil
}

func (p *Provider) GetGoogleIdentityToken(ctx context.Context, saToken, googleEmail, audience string) (string, time.Duration, error) {
	// we dont cache identity tokens for now since they depend on external input (the target audience)
	return p.opts.Source.GetGoogleIdentityToken(ctx, saToken, googleEmail, audience)
}

func (p *Provider) requestTokens(ctx context.Context, ref *serviceaccounts.Reference) (*tokens, error) {
	p.mu.Lock()
	sa, ok := p.serviceAccounts[*ref]
	if !ok {
		const podCount = 0
		const nodeIsUsing = false
		sa = p.addServiceAccount(ref, podCount, nodeIsUsing)
	} else if sa.deleted {
		p.mu.Unlock()
		return nil, errServiceAccountDeleted
	}
	p.mu.Unlock()

	tokens, now := sa.tokens, time.Now()
	if tokens == nil || now.After(tokens.tokenExpiration) || now.After(tokens.accessTokenExpiration) {
		p.cacheMisses.Inc()
		tokens, err := sa.requestTokens(ctx, p.ctx)
		if err != nil {
			return nil, err
		}
		return tokens, nil
	}

	return tokens, nil
}

func (p *Provider) cacheTokens(sa *serviceAccount) (retErr error) {
	l := logging.FromContext(p.ctx).WithField("service_account", sa.Reference)

	var externalRequestsChannel <-chan chan<- *tokensAndError = sa.externalRequests
	var externalRequests []chan<- *tokensAndError
	sendResponse := func(resp *tokensAndError) {
		for len(externalRequestsChannel) > 0 {
			externalRequests = append(externalRequests, <-externalRequestsChannel)
		}
		for _, req := range externalRequests {
			if req != nil {
				req <- resp
				close(req)
			}
		}
		externalRequests = nil
	}
	defer sendResponse(&tokensAndError{err: retErr})

	var retries int
	for {
		if deleted := p.checkIfMustDeleteAndDelete(sa); deleted {
			return errServiceAccountDeleted
		}

		// acquire semaphore to limit concurrency
		select {
		case p.semaphore <- struct{}{}:
		case <-p.ctx.Done():
			return fmt.Errorf("context done while acquiring semaphore: %w", p.ctx.Err())
		}

		// create tokens
		tokens, email, err := p.createTokens(p.ctx, &sa.Reference)

		// release semaphore
		<-p.semaphore

		// check if service account was deleted again since it may take some time to create tokens
		if deleted := p.checkIfMustDeleteAndDelete(sa); deleted {
			return errServiceAccountDeleted
		}

		// check error
		var sleepDuration time.Duration
		if err != nil {
			// do not retry invalid GKE annotation errors
			annotationMissing := errors.Is(err, serviceaccounts.ErrGKEAnnotationMissing)
			annotationInvalid := errors.Is(err, serviceaccounts.ErrGKEAnnotationInvalid)
			if annotationMissing || annotationInvalid {
				sleepDuration = 10 * 365 * 24 * time.Hour // infinite
				retries = 0
				sendResponse(&tokensAndError{err: err})
				if annotationMissing {
					l.Debug("service account does not have GKE annotation, sleeping for a long time...")
				} else {
					l.WithError(err).Error("service account has invalid GKE annotation, will not retry")
				}
			} else { // retry any other error
				sleepDuration = (1 << retries) * time.Second
				if retries < 5 {
					retries++
				}
				l.WithError(err).Errorf("error creating tokens for service account, will retry after %s...",
					sleepDuration.String())
			}
		} else { // success
			sleepDuration = tokens.sleepDurationUntilNextFetch()
			retries = 0
			sendResponse(&tokensAndError{tokens: tokens})
			l.WithField("google_service_account", email).Info("cached tokens for service account")
		}

		// store tokens
		sa.tokens = tokens

		// sleep
		t := time.NewTimer(sleepDuration)
		select {
		case <-t.C:
		case req := <-externalRequestsChannel:
			t.Stop()
			externalRequests = append(externalRequests, req)
		case <-p.ctx.Done():
			t.Stop()
			return fmt.Errorf("context done while waiting for next token refresh: %w", p.ctx.Err())
		}
	}
}
