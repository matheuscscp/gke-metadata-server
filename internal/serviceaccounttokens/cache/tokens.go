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
	"fmt"
	"time"

	"github.com/matheuscscp/gke-metadata-server/internal/serviceaccounts"
	"github.com/matheuscscp/gke-metadata-server/internal/serviceaccounttokens"
)

type tokenAndExpiration[T any] struct {
	token               T
	monotonicExpiration time.Time
	wallClockExpiration time.Time
}

type tokens struct {
	serviceAccountToken *tokenAndExpiration[string]
	googleAccessTokens  *tokenAndExpiration[*serviceaccounttokens.AccessTokens]
}

type tokensAndError struct {
	tokens *tokens
	err    error
}

type googleIDTokenReference struct {
	serviceAccountRefernce serviceaccounts.Reference
	email                  string
	audience               string
}

type googleScopedAccessTokenReference struct {
	serviceAccountRefernce serviceaccounts.Reference
	email                  string
	scopes                 string
}

func (p *Provider) createTokens(ctx context.Context, saRef *serviceaccounts.Reference) (*tokens, *string, error) {
	sa, err := p.opts.ServiceAccounts.Get(ctx, saRef)
	if err != nil {
		return nil, nil, fmt.Errorf("error getting kubernetes service account: %w", err)
	}

	email, err := serviceaccounts.GoogleServiceAccountEmail(sa)
	if err != nil {
		return nil, nil, fmt.Errorf("error getting google service account email from kubernetes service account: %w", err)
	}

	saToken, saTokenExpiration, err := p.opts.Source.GetServiceAccountToken(ctx, saRef)
	if err != nil {
		return nil, nil, fmt.Errorf("error creating token for kubernetes service account: %w", err)
	}

	accessTokens, accessTokenExpiration, err := p.opts.Source.GetGoogleAccessTokens(ctx, saToken, email, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("error creating google access token: %w", err)
	}

	return &tokens{
		serviceAccountToken: newToken(saToken, saTokenExpiration),
		googleAccessTokens:  newToken(accessTokens, accessTokenExpiration),
	}, email, nil
}

func newToken[T any](token T, monotonicExpiration time.Time) *tokenAndExpiration[T] {
	return &tokenAndExpiration[T]{
		token:               token,
		monotonicExpiration: monotonicExpiration,
		wallClockExpiration: time.Unix(monotonicExpiration.Unix(), 0),
	}
}

func (t *tokenAndExpiration[T]) expiration() time.Time {
	if time.Until(t.monotonicExpiration) < time.Until(t.wallClockExpiration) {
		return t.monotonicExpiration
	}
	return t.wallClockExpiration
}

func (t *tokenAndExpiration[T]) timeUntilExpiration() time.Duration {
	return time.Until(t.expiration())
}

func (t *tokenAndExpiration[T]) isExpired() bool {
	return t.timeUntilExpiration() <= 0
}

func (t *tokens) timeUntilExpiration() time.Duration {
	d := t.serviceAccountToken.timeUntilExpiration()
	if google := t.googleAccessTokens.timeUntilExpiration(); google < d {
		d = google
	}
	return d
}
