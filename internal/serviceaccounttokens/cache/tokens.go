// Copyright 2025 Matheus Pimenta.
// SPDX-License-Identifier: AGPL-3.0

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
		serviceAccountToken: newToken(saToken, saTokenExpiration, p.opts.MaxTokenDuration),
		googleAccessTokens:  newToken(accessTokens, accessTokenExpiration, p.opts.MaxTokenDuration),
	}, email, nil
}

func newToken[T any](token T, monotonicExpiration time.Time, maxTokenDuration time.Duration) *tokenAndExpiration[T] {
	now := time.Now()
	duration := monotonicExpiration.Sub(now)

	// Apply 80% rule first (similar to kubelet for ServiceAccount token rotation)
	effectiveDuration := (duration * 8) / 10

	// Apply maximum token duration if specified (capped at 1 hour)
	maxDuration := maxTokenDuration
	if maxDuration > time.Hour {
		maxDuration = time.Hour
	}
	if maxDuration > 0 && effectiveDuration > maxDuration {
		effectiveDuration = maxDuration
	}

	effectiveExpiration := now.Add(effectiveDuration)

	return &tokenAndExpiration[T]{
		token:               token,
		monotonicExpiration: effectiveExpiration,
		wallClockExpiration: time.Unix(effectiveExpiration.Unix(), 0),
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
