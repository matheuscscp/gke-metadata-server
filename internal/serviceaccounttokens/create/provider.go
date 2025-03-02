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
	"fmt"
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

func (p *Provider) GetGoogleAccessToken(ctx context.Context, saToken string, googleEmail *string) (string, time.Time, error) {
	token, err := p.opts.GoogleCredentialsConfig.NewToken(ctx, saToken, googleEmail)
	if err != nil {
		return "", time.Time{}, err
	}
	return token.AccessToken, token.Expiry, nil
}

func (p *Provider) GetGoogleIdentityToken(ctx context.Context, saToken, googleEmail, audience string) (string, time.Time, error) {
	accessToken, err := p.opts.GoogleCredentialsConfig.NewToken(ctx, saToken, &googleEmail)
	if err != nil {
		return "", time.Time{}, err
	}
	accessTokenSource := oauth2.StaticTokenSource(accessToken)

	conf := impersonate.IDTokenConfig{
		Audience:        audience,
		TargetPrincipal: googleEmail,
		IncludeEmail:    true,
	}
	idTokenSource, err := impersonate.IDTokenSource(ctx, conf, option.WithTokenSource(accessTokenSource))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("error creating google identity token source: %w", err)
	}

	idToken, err := idTokenSource.Token()
	if err != nil {
		return "", time.Time{}, fmt.Errorf("error creating google identity token: %w", err)
	}

	return idToken.AccessToken, idToken.Expiry, nil
}
