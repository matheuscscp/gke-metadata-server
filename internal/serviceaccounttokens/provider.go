// Copyright 2025 Matheus Pimenta.
// SPDX-License-Identifier: AGPL-3.0

package serviceaccounttokens

import (
	"context"
	"time"

	"github.com/matheuscscp/gke-metadata-server/internal/serviceaccounts"
)

type AccessTokens struct {
	DirectAccess string
	Impersonated string
}

type Provider interface {
	GetServiceAccountToken(ctx context.Context, ref *serviceaccounts.Reference) (string, time.Time, error)
	GetGoogleAccessTokens(ctx context.Context, saToken string, googleEmail *string,
		scopes []string) (*AccessTokens, time.Time, error)
	GetGoogleIdentityToken(ctx context.Context, saRef *serviceaccounts.Reference,
		accessToken, googleEmail, audience string) (string, time.Time, error)
}
