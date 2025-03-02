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

func (c *Config) NewToken(ctx context.Context, subjectToken string, googleServiceAccountEmail *string) (*oauth2.Token, error) {
	conf := externalaccount.Config{
		UniverseDomain:       "googleapis.com",
		Audience:             c.WorkloadIdentityProviderAudience(),
		SubjectTokenType:     "urn:ietf:params:oauth:token-type:jwt",
		TokenURL:             "https://sts.googleapis.com/v1/token",
		Scopes:               AccessScopes(),
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
