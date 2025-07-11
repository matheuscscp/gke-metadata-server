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
	"strings"
	"sync"
	"time"

	"github.com/matheuscscp/gke-metadata-server/internal/logging"
	"github.com/matheuscscp/gke-metadata-server/internal/metrics"
	"github.com/matheuscscp/gke-metadata-server/internal/serviceaccounts"
	"github.com/matheuscscp/gke-metadata-server/internal/serviceaccounttokens"

	"github.com/prometheus/client_golang/prometheus"
)

type Provider struct {
	opts                          ProviderOptions
	numTokens                     prometheus.Gauge
	cacheMisses                   prometheus.Counter
	serviceAccounts               map[serviceaccounts.Reference]*serviceAccount
	googleIDTokens                map[googleIDTokenReference]*tokenAndExpiration[string]
	googleScopedAccessTokens      map[googleScopedAccessTokenReference]*tokenAndExpiration[string]
	nodeServiceAccountRef         *serviceaccounts.Reference
	ctx                           context.Context
	cancelCtx                     context.CancelFunc
	serviceAccountsMutex          sync.Mutex
	googleIDTokensMutex           sync.RWMutex
	googleScopedAccessTokensMutex sync.RWMutex
	wg                            sync.WaitGroup
	semaphore                     chan struct{}
}

type ProviderOptions struct {
	Source           serviceaccounttokens.Provider
	ServiceAccounts  serviceaccounts.Provider
	MetricsRegistry  *prometheus.Registry
	Concurrency      int
	MaxTokenDuration time.Duration
}

var errServiceAccountDeleted = errors.New("service account was deleted")

func NewProvider(ctx context.Context, opts ProviderOptions) *Provider {
	numTokens := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: metrics.Namespace,
		Name:      "service_account_tokens",
		Help:      "Amount of ServiceAccount tokens currently cached.",
	})
	opts.MetricsRegistry.MustRegister(numTokens)
	cacheMisses := prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: metrics.Namespace,
		Name:      "service_account_token_cache_misses_total",
		Help:      "Total amount cache misses when fetching ServiceAccount tokens.",
	})
	opts.MetricsRegistry.MustRegister(cacheMisses)

	// create a new background context for the goroutines with logging from the parent context
	backgroundCtx := logging.IntoContext(context.Background(), logging.FromContext(ctx))
	backgroundCtx, cancel := context.WithCancel(backgroundCtx)

	p := &Provider{
		opts:                     opts,
		numTokens:                numTokens,
		cacheMisses:              cacheMisses,
		serviceAccounts:          make(map[serviceaccounts.Reference]*serviceAccount),
		googleIDTokens:           make(map[googleIDTokenReference]*tokenAndExpiration[string]),
		googleScopedAccessTokens: make(map[googleScopedAccessTokenReference]*tokenAndExpiration[string]),
		ctx:                      backgroundCtx,
		cancelCtx:                cancel,
		semaphore:                make(chan struct{}, opts.Concurrency),
	}

	// start garbage collector for input-dependant tokens
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		for {
			sleep := time.NewTimer(time.Minute)
			select {
			case <-p.ctx.Done():
				sleep.Stop()
				return
			case <-sleep.C:
				p.googleIDTokensMutex.Lock()
				for ref, token := range p.googleIDTokens {
					if token.isExpired() {
						delete(p.googleIDTokens, ref)
					}
				}
				p.googleIDTokensMutex.Unlock()

				p.googleScopedAccessTokensMutex.Lock()
				for ref, token := range p.googleScopedAccessTokens {
					if token.isExpired() {
						delete(p.googleScopedAccessTokens, ref)
					}
				}
				p.googleScopedAccessTokensMutex.Unlock()
			}
		}
	}()

	return p
}

func (p *Provider) Close() error {
	p.cancelCtx()
	p.wg.Wait()
	return nil
}

func (p *Provider) GetServiceAccountToken(ctx context.Context, ref *serviceaccounts.Reference) (string, time.Time, error) {
	tokens, err := p.getTokens(ctx, ref)
	if err != nil {
		return "", time.Time{}, err
	}
	token := tokens.serviceAccountToken
	return token.token, token.expiration(), nil
}

func (p *Provider) GetGoogleAccessTokens(ctx context.Context, saToken string,
	googleEmail *string, scopes []string) (*serviceaccounttokens.AccessTokens, time.Time, error) {

	saRef := serviceaccounts.ReferenceFromToken(saToken)

	// easy case: no scopes
	if len(scopes) == 0 {
		tokens, err := p.getTokens(ctx, saRef)
		if err != nil {
			return nil, time.Time{}, err
		}
		token := tokens.googleAccessTokens
		return token.token, token.expiration(), nil
	}

	// handle case with custom scopes

	var email string
	if googleEmail != nil {
		email = *googleEmail
	}
	ref := googleScopedAccessTokenReference{*saRef, email, strings.Join(scopes, ",")}

	// check cache first
	p.googleScopedAccessTokensMutex.RLock()
	token, ok := p.googleScopedAccessTokens[ref]
	p.googleScopedAccessTokensMutex.RUnlock()
	if ok && !token.isExpired() {
		return &serviceaccounttokens.AccessTokens{DirectAccess: token.token}, token.expiration(), nil
	}

	// cache miss or token expired. need to cache a new token, so acquire semaphore to limit concurrency
	select {
	case p.semaphore <- struct{}{}:
	case <-ctx.Done():
		return nil, time.Time{}, fmt.Errorf("request context done while acquiring semaphore: %w", ctx.Err())
	case <-p.ctx.Done():
		return nil, time.Time{}, fmt.Errorf("process terminated while acquiring semaphore: %w", p.ctx.Err())
	}

	tokens, expiration, err := p.opts.Source.GetGoogleAccessTokens(ctx, saToken, googleEmail, scopes)

	// release concurrency semaphore
	<-p.semaphore

	// check error
	if err != nil {
		return nil, time.Time{}, err
	}

	// token issued successfully. cache it and return
	tokenString := tokens.DirectAccess
	if tokenString == "" {
		tokenString = tokens.Impersonated
	}
	token = newToken(tokenString, expiration, p.opts.MaxTokenDuration)
	p.googleScopedAccessTokensMutex.Lock()
	p.googleScopedAccessTokens[ref] = token
	p.googleScopedAccessTokensMutex.Unlock()
	return &serviceaccounttokens.AccessTokens{DirectAccess: token.token}, token.expiration(), nil
}

func (p *Provider) GetGoogleIdentityToken(ctx context.Context, saRef *serviceaccounts.Reference,
	accessToken, googleEmail, audience string) (string, time.Time, error) {

	ref := googleIDTokenReference{*saRef, googleEmail, audience}

	// check cache first
	p.googleIDTokensMutex.RLock()
	token, ok := p.googleIDTokens[ref]
	p.googleIDTokensMutex.RUnlock()
	if ok && !token.isExpired() {
		return token.token, token.expiration(), nil
	}

	// cache miss or token expired. need to cache a new token, so acquire semaphore to limit concurrency
	select {
	case p.semaphore <- struct{}{}:
	case <-ctx.Done():
		return "", time.Time{}, fmt.Errorf("request context done while acquiring semaphore: %w", ctx.Err())
	case <-p.ctx.Done():
		return "", time.Time{}, fmt.Errorf("process terminated while acquiring semaphore: %w", p.ctx.Err())
	}

	tokenString, expiration, err := p.opts.Source.GetGoogleIdentityToken(ctx, saRef, accessToken, googleEmail, audience)

	// release concurrency semaphore
	<-p.semaphore

	// check error
	if err != nil {
		return "", time.Time{}, err
	}

	// token issued successfully. cache it and return
	token = newToken(tokenString, expiration, p.opts.MaxTokenDuration)
	p.googleIDTokensMutex.Lock()
	p.googleIDTokens[ref] = token
	p.googleIDTokensMutex.Unlock()
	return token.token, token.expiration(), nil
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

		// enhance logging with google service account email if any
		l := l
		if email != nil {
			l = l.WithField("google_service_account", *email)
		}

		// check if service account was deleted again since it may take some time to create tokens
		if deleted := p.checkIfMustDeleteAndDelete(sa); deleted {
			return errServiceAccountDeleted
		}

		// check error
		var sleepDuration time.Duration
		if err != nil {
			// do not retry invalid GKE annotation errors
			if errors.Is(err, serviceaccounts.ErrGKEAnnotationInvalid) {
				sleepDuration = 10 * 365 * 24 * time.Hour // infinite
				retries = 0
				sendResponse(&tokensAndError{err: err})
				l.WithError(err).Error("service account has invalid GKE annotation, will not retry")
			} else { // retry any other error
				sleepDuration = (1 << retries) * time.Second
				if retries < 5 {
					retries++
				}
				l.WithError(err).Errorf("error creating tokens for service account, will retry after %s...",
					sleepDuration.String())
			}
		} else { // success
			sleepDuration = tokens.timeUntilExpiration()
			const safeDistance = time.Minute
			if sleepDuration >= safeDistance {
				sleepDuration -= safeDistance
			}
			retries = 0
			sendResponse(&tokensAndError{tokens: tokens})
			l.Info("cached tokens for service account")
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
