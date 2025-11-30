// Copyright 2025 Matheus Pimenta.
// SPDX-License-Identifier: AGPL-3.0

package googlecredentials

import (
	"context"
	"fmt"
	"regexp"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google/externalaccount"
)

type (
	Config struct {
		opts ConfigOptions
	}

	ConfigOptions struct {
		WorkloadIdentityProvider string
	}

	tokenSupplier string
)

var workloadIdentityProviderRegex = regexp.MustCompile(`^projects/(\d+)/locations/global/workloadIdentityPools/([^/]+)/providers/[^/]+$`)

func AccessScopes() []string {
	return []string{
		"https://www.googleapis.com/auth/cloud-platform",
		"https://www.googleapis.com/auth/userinfo.email",
	}
}

func NewConfig(opts ConfigOptions) (*Config, string, string, error) {
	if !workloadIdentityProviderRegex.MatchString(opts.WorkloadIdentityProvider) {
		return nil, "", "", fmt.Errorf("workload identity provider name does not match pattern: %s",
			workloadIdentityProviderRegex.String())
	}
	numericProjectID := workloadIdentityProviderRegex.FindStringSubmatch(opts.WorkloadIdentityProvider)[1]
	workloadIdentityPool := workloadIdentityProviderRegex.FindStringSubmatch(opts.WorkloadIdentityProvider)[2]
	return &Config{opts}, numericProjectID, workloadIdentityPool, nil
}

func (c *Config) WorkloadIdentityProviderAudience() string {
	return fmt.Sprintf("//iam.googleapis.com/%s", c.opts.WorkloadIdentityProvider)
}

func (c *Config) NewToken(ctx context.Context, subjectToken string,
	googleServiceAccountEmail *string, scopes []string) (*oauth2.Token, error) {

	if len(scopes) == 0 {
		scopes = AccessScopes()
	}

	conf := externalaccount.Config{
		UniverseDomain:       "googleapis.com",
		Audience:             c.WorkloadIdentityProviderAudience(),
		SubjectTokenType:     "urn:ietf:params:oauth:token-type:jwt",
		TokenURL:             "https://sts.googleapis.com/v1/token",
		Scopes:               scopes,
		SubjectTokenSupplier: tokenSupplier(subjectToken),
	}

	if googleServiceAccountEmail != nil {
		conf.ServiceAccountImpersonationURL = fmt.Sprintf(
			"https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/%s:generateAccessToken",
			*googleServiceAccountEmail)
	} else {
		conf.TokenInfoURL = "https://sts.googleapis.com/v1/introspect"
	}

	src, err := externalaccount.NewTokenSource(ctx, conf)
	if err != nil {
		return nil, err
	}

	token, err := src.Token()
	if err != nil {
		return nil, err
	}

	return token, nil
}

func (s tokenSupplier) SubjectToken(ctx context.Context, options externalaccount.SupplierOptions) (string, error) {
	return string(s), nil
}
