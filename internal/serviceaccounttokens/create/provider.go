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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/matheuscscp/gke-metadata-server/internal/googlecredentials"
	"github.com/matheuscscp/gke-metadata-server/internal/serviceaccounttokens"

	"github.com/google/uuid"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/idtoken"
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

func (p *Provider) GetServiceAccountToken(ctx context.Context, namespace, name string) (string, time.Duration, error) {
	expSecs := int64(p.opts.GoogleCredentialsConfig.TokenExpirationSeconds())
	resp, err := p.opts.
		KubeClient.
		CoreV1().
		ServiceAccounts(namespace).
		CreateToken(ctx, name, &authnv1.TokenRequest{
			Spec: authnv1.TokenRequestSpec{
				Audiences:         []string{p.opts.GoogleCredentialsConfig.WorkloadIdentityProviderAudience()},
				ExpirationSeconds: &expSecs,
			},
		}, metav1.CreateOptions{})
	if err != nil {
		return "", 0, err
	}
	return resp.Status.Token, time.Duration(expSecs) * time.Second, nil
}

func (p *Provider) GetGoogleAccessToken(ctx context.Context, saToken, googleEmail string) (string, time.Duration, error) {
	var token *oauth2.Token
	err := p.runWithGoogleCredentialsFromKubernetesServiceAccountToken(ctx, saToken, googleEmail, func(ctx context.Context, c *google.Credentials) (err error) {
		token, err = c.TokenSource.Token()
		return
	})
	if err != nil {
		return "", 0, err
	}
	return token.AccessToken, time.Until(token.Expiry), nil
}

func (p *Provider) GetGoogleIdentityToken(ctx context.Context, saToken, googleEmail, audience string) (string, time.Duration, error) {
	var token *oauth2.Token
	err := p.runWithGoogleCredentialsFromKubernetesServiceAccountToken(ctx, saToken, googleEmail, func(ctx context.Context, c *google.Credentials) (err error) {
		source, err := idtoken.NewTokenSource(ctx, audience, option.WithCredentials(c))
		if err != nil {
			return err
		}
		token, err = source.Token()
		return
	})
	if err != nil {
		return "", 0, err
	}
	return token.AccessToken, time.Until(token.Expiry), nil
}

// runWithGoogleCredentialsFromKubernetesServiceAccountToken creates
// a *google.Credentials object from a Kubernetes ServiceAccount
// Token. The function internally writes the token
// to a temporary file and runs the given callback f() passing
// a *google.Credentials object configured to use this temporary
// file. The temporary file is removed before the function
// returns (hence why a callback is used).
func (p *Provider) runWithGoogleCredentialsFromKubernetesServiceAccountToken(ctx context.Context,
	token, email string, f func(context.Context, *google.Credentials) error) error {
	// write k8s sa token to tmp file
	var tokenFile string
	for {
		tokenFile = filepath.Join(os.TempDir(), fmt.Sprintf("%s.json", uuid.NewString()))
		file, err := os.OpenFile(tokenFile, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err != nil {
			if os.IsExist(err) {
				continue
			}
			return fmt.Errorf("error creating temporary file for service account token '%s': %w", tokenFile, err)
		}
		defer os.Remove(tokenFile)
		if _, err := file.Write([]byte(token)); err != nil {
			return fmt.Errorf("error writing service account token to temporary file '%s': %w", tokenFile, err)
		}
		if err := file.Close(); err != nil {
			return fmt.Errorf("error closing service account token temporary file '%s': %w", tokenFile, err)
		}
		break
	}

	// get the credential config with k8s sa token file as the credential source
	b, err := json.Marshal(p.opts.GoogleCredentialsConfig.Get(email, map[string]any{
		"format": map[string]string{"type": "text"},
		"file":   tokenFile,
	}))
	if err != nil {
		return fmt.Errorf("error marshaling google credential config to json: %w", err)
	}
	creds, err := google.CredentialsFromJSON(ctx, b, googlecredentials.AccessScopes()...)
	if err != nil {
		return fmt.Errorf("error getting google credentials for service account token: %w", err)
	}

	// run callback with creds, then defer will remove the sa token file
	return f(ctx, creds)
}