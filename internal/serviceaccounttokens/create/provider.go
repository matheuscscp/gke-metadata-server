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

package createserviceaccounttoken

import (
	"context"
	"time"

	"github.com/matheuscscp/gke-metadata-server/internal/googlecredentials"
	"github.com/matheuscscp/gke-metadata-server/internal/serviceaccounts"
	"github.com/matheuscscp/gke-metadata-server/internal/serviceaccounttokens"
	"golang.org/x/oauth2"

	"google.golang.org/api/impersonate"
	"google.golang.org/api/option"
	authnv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type (
	Provider struct {
		opts ProviderOptions
	}

	ProviderOptions struct {
		GoogleCredentialsConfig *googlecredentials.Config
		KubeClient              *kubernetes.Clientset
	}
)

func NewProvider(opts ProviderOptions) serviceaccounttokens.Provider {
	return &Provider{opts}
}

func (p *Provider) GetServiceAccountToken(ctx context.Context, ref *serviceaccounts.Reference) (string, time.Time, error) {
	tokenRequest, err := p.opts.
		KubeClient.
		CoreV1().
		ServiceAccounts(ref.Namespace).
		CreateToken(ctx, ref.Name, &authnv1.TokenRequest{
			Spec: authnv1.TokenRequestSpec{
				Audiences: []string{p.opts.GoogleCredentialsConfig.WorkloadIdentityProviderAudience()},
			},
		}, metav1.CreateOptions{})
	if err != nil {
		return "", time.Time{}, err
	}
	status := tokenRequest.Status
	return status.Token, status.ExpirationTimestamp.Time, nil
}

func (p *Provider) GetGoogleAccessTokens(ctx context.Context, saToken string,
	googleEmail *string, scopes []string) (*serviceaccounttokens.AccessTokens, time.Time, error) {

	expiration := time.Now().Add(365 * 24 * time.Hour)

	// Optimization: No need for a direct access token if the token was requested with custom
	// scopes and a google service account email is configured for impersonation. Tokens with
	// custom scopes are not used for fetching google identity tokens, so we only need to
	// cache the token that was requested by a client pod.
	var directAccess string
	if !(googleEmail != nil && len(scopes) > 0) {
		token, err := p.opts.GoogleCredentialsConfig.NewToken(ctx, saToken, nil, scopes)
		if err != nil {
			return nil, time.Time{}, err
		}
		directAccess = token.AccessToken
		expiration = token.Expiry
	}

	var impersonated string
	if googleEmail != nil {
		token, err := p.opts.GoogleCredentialsConfig.NewToken(ctx, saToken, googleEmail, scopes)
		if err != nil {
			return nil, time.Time{}, err
		}
		impersonated = token.AccessToken
		if token.Expiry.Before(expiration) {
			expiration = token.Expiry
		}
	}

	return &serviceaccounttokens.AccessTokens{
		DirectAccess: directAccess,
		Impersonated: impersonated,
	}, expiration, nil
}

func (p *Provider) GetGoogleIdentityToken(ctx context.Context, _ *serviceaccounts.Reference,
	accessToken, googleEmail, audience string) (string, time.Time, error) {

	conf := impersonate.IDTokenConfig{
		Audience:        audience,
		TargetPrincipal: googleEmail,
		IncludeEmail:    true,
	}
	accessTokenSource := oauth2.StaticTokenSource(&oauth2.Token{
		AccessToken: accessToken,
	})
	idTokenSource, err := impersonate.IDTokenSource(ctx, conf, option.WithTokenSource(accessTokenSource))
	if err != nil {
		return "", time.Time{}, err
	}

	idToken, err := idTokenSource.Token()
	if err != nil {
		return "", time.Time{}, err
	}

	return idToken.AccessToken, idToken.Expiry, nil
}
