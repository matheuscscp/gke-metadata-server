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
)

type tokenAndExpiration struct {
	token               string
	monotonicExpiration time.Time
	wallClockExpiration time.Time
}

type tokens struct {
	serviceAccountToken *tokenAndExpiration
	googleAccessToken   *tokenAndExpiration
}

type tokensAndError struct {
	tokens *tokens
	err    error
}

func (p *Provider) createTokens(ctx context.Context, saRef *serviceaccounts.Reference) (*tokens, string, error) {
	sa, err := p.opts.ServiceAccounts.Get(ctx, saRef)
	if err != nil {
		return nil, "", fmt.Errorf("error getting kubernetes service account: %w", err)
	}

	email, err := serviceaccounts.GoogleEmail(sa)
	if err != nil {
		return nil, "", fmt.Errorf("error getting google service account from kubernetes service account: %w", err)
	}

	saToken, saTokenExpiration, err := p.opts.Source.GetServiceAccountToken(ctx, saRef)
	if err != nil {
		return nil, "", fmt.Errorf("error creating token for kubernetes service account: %w", err)
	}

	accessToken, accessTokenExpiration, err := p.opts.Source.GetGoogleAccessToken(ctx, saToken, email)
	if err != nil {
		return nil, "", fmt.Errorf("error creating access token for google service account %s: %w", email, err)
	}

	return &tokens{
		serviceAccountToken: newTokenAndExpiration(saToken, saTokenExpiration),
		googleAccessToken:   newTokenAndExpiration(accessToken, accessTokenExpiration),
	}, email, nil
}

func newTokenAndExpiration(token string, monotonicExpiration time.Time) *tokenAndExpiration {
	return &tokenAndExpiration{
		token:               token,
		monotonicExpiration: monotonicExpiration,
		wallClockExpiration: time.Unix(monotonicExpiration.Unix(), 0),
	}
}

func (t *tokenAndExpiration) expiration() time.Time {
	if time.Until(t.monotonicExpiration) < time.Until(t.wallClockExpiration) {
		return t.monotonicExpiration
	}
	return t.wallClockExpiration
}

func (t *tokenAndExpiration) timeUntilExpiration() time.Duration {
	return time.Until(t.expiration())
}

func (t *tokenAndExpiration) isExpired() bool {
	return t.timeUntilExpiration() <= 0
}

func (t *tokens) timeUntilExpiration() time.Duration {
	d := t.serviceAccountToken.timeUntilExpiration()
	if google := t.googleAccessToken.timeUntilExpiration(); google < d {
		d = google
	}
	return d
}
