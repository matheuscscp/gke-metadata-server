// MIT License
//
// Copyright (c) 2023 Matheus Pimenta
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

package server_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	pkgtesting "github.com/matheuscscp/gke-metadata-server/internal/testing"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/stretchr/testify/assert"
)

const emuMetadataFlavor = "Emulator"

var emuHeaders = http.Header{
	"Metadata-Flavor": []string{emuMetadataFlavor},
}

func TestEmuPodGoogleCredConfigAPI(t *testing.T) {
	const url = "http://metadata.google.internal/gkeMetadataEmulator/v1/pod/service-account/google-cred-config"

	var respBody struct {
		Type                           string `json:"type"`
		Audience                       string `json:"audience"`
		SubjectTokenType               string `json:"subject_token_type"`
		TokenURL                       string `json:"token_url"`
		ServiceAccountImpersonationURL string `json:"service_account_impersonation_url"`
		ServiceAccountImpersonation    struct {
			TokenLifetimeSeconds int `json:"token_lifetime_seconds"`
		} `json:"service_account_impersonation"`
		CredentialSource struct {
			URL     string `json:"url"`
			Headers struct {
				MetadataFlavor string `json:"Metadata-Flavor"`
			} `json:"headers"`
			Format struct {
				Type string `json:"type"`
			} `json:"format"`
		} `json:"credential_source"`
	}

	pkgtesting.RequestJSON(t, emuHeaders, url, "google credential config", emuMetadataFlavor, &respBody)

	pkgtesting.CheckRegex(t, "workload identity provider audience", workloadIdentityProviderAudience, respBody.Audience)
	assert.Equal(t, "external_account", respBody.Type)
	assert.Equal(t, "urn:ietf:params:oauth:token-type:jwt", respBody.SubjectTokenType)
	assert.Equal(t, "https://sts.googleapis.com/v1/token", respBody.TokenURL)
	assert.Equal(t, "https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/test-sa@gke-metadata-server.iam.gserviceaccount.com:generateAccessToken", respBody.ServiceAccountImpersonationURL)
	assert.Equal(t, 3600, respBody.ServiceAccountImpersonation.TokenLifetimeSeconds)
	assert.Equal(t, "text", respBody.CredentialSource.Format.Type)
	assert.Equal(t, "http://metadata.google.internal/gkeMetadataEmulator/v1/pod/service-account/token", respBody.CredentialSource.URL)
	assert.Equal(t, "Emulator", respBody.CredentialSource.Headers.MetadataFlavor)
}

func TestEmuPodServiceAccountTokenAPI(t *testing.T) {
	const url = "http://metadata.google.internal/gkeMetadataEmulator/v1/pod/service-account/token"

	const aud = workloadIdentityProviderAudience
	const iss = "https://kubernetes.default.svc.cluster.local"
	const sub = "system:serviceaccount:default:test"

	rawToken := pkgtesting.RequestIDToken(t, emuHeaders, url, "pod serviceaccount token", emuMetadataFlavor, aud, iss, sub)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	http.DefaultTransport.(*http.Transport).TLSClientConfig.InsecureSkipVerify = true
	defer func() { http.DefaultTransport.(*http.Transport).TLSClientConfig.InsecureSkipVerify = false }()

	kubernetes, err := oidc.NewProvider(ctx, iss)
	if err != nil {
		t.Fatalf("error creating kubernetes serviceaccount oidc provider: %v", err)
	}
	_, err = kubernetes.VerifierContext(ctx, &oidc.Config{ClientID: pkgtesting.EvalEnv(aud)}).Verify(ctx, rawToken)
	if err != nil {
		t.Fatalf("error verifying pod serviceaccount token: %v", err)
	}
}

const workloadIdentityProviderAudience = "//iam.googleapis.com/projects/637293746831/locations/global/workloadIdentityPools/test-kind-cluster/providers/TEST_ID"
