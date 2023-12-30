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
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	pkgtesting "github.com/matheuscscp/gke-metadata-server/internal/testing"

	"cloud.google.com/go/storage"
	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"google.golang.org/api/googleapi"
)

const (
	gkeMetadataFlavor = "Google"

	testBucket = "gke-metadata-server-test"
)

var gkeHeaders = http.Header{
	"Metadata-Flavor": []string{gkeMetadataFlavor},
}

func TestGKEServiceAccountIdentityAPI(t *testing.T) {
	const aud = "test.com"
	const iss = "https://accounts.google.com"
	const sub = `\d{20,30}`
	const url = "http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/identity?audience=" + aud

	rawToken := pkgtesting.RequestIDToken(t, gkeHeaders, url, "google id token", gkeMetadataFlavor, aud, iss, sub)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	google, err := oidc.NewProvider(ctx, iss)
	if err != nil {
		t.Fatalf("error creating google accounts oidc provider: %v", err)
	}
	token, err := google.VerifierContext(ctx, &oidc.Config{ClientID: aud}).Verify(ctx, rawToken)
	if err != nil {
		t.Fatalf("error verifying google id token: %v", err)
	}
	var claims struct {
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
	}
	if err := token.Claims(&claims); err != nil {
		t.Fatalf("error unmarshaling google id token claims: %v", err)
	}
	assert.Equal(t, "test-sa@gke-metadata-server.iam.gserviceaccount.com", claims.Email)
	assert.True(t, claims.EmailVerified)
}

func TestGKEServiceAccountTokenAPI(t *testing.T) {
	const url = "http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/token"

	var respBody struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		TokenType   string `json:"token_type"`
	}

	pkgtesting.RequestJSON(t, gkeHeaders, url, "google access token", gkeMetadataFlavor, &respBody)

	pkgtesting.AssertExpirationSeconds(t, respBody.ExpiresIn)
	assert.Equal(t, "Bearer", respBody.TokenType)

	// now let's try to use this access token for real, doing a GCS roundtrip via the HTTP API,
	// sending the token as Bearer

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var key, value string
	for {
		key, value = uuid.NewString(), uuid.NewString()
		uploadURL := "https://storage.googleapis.com/upload/storage/v1/b/" + testBucket + "/o?uploadType=media&name=" + key
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, bytes.NewReader([]byte(value)))
		if err != nil {
			t.Fatalf("error creating gcs object upload request: %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+respBody.AccessToken)
		req.Header.Set("Content-Type", "text/plain")
		req.Header.Set("If-None-Match", "*")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("error performing gcs object upload request: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusPreconditionFailed {
			continue
		}
		if resp.StatusCode != http.StatusOK {
			b, readErr := io.ReadAll(resp.Body)
			err := errors.New(string(b))
			t.Fatalf("unexpected status code %v in gcs object upload request: %v", resp.StatusCode, errors.Join(err, readErr))
		}
		break
	}

	// get and check object
	headers := http.Header{
		"Authorization": []string{"Bearer " + respBody.AccessToken},
	}
	getObjectURL := "https://storage.googleapis.com/storage/v1/b/" + testBucket + "/o/" + key + "?alt=media"
	text := pkgtesting.RequestText(t, headers, getObjectURL, "gcs object")
	assert.Equal(t, value, text)

	// delete object
	deleteURL := "https://storage.googleapis.com/storage/v1/b/" + testBucket + "/o/" + key
	req, err := http.NewRequest(http.MethodDelete, deleteURL, nil)
	if err != nil {
		t.Fatalf("error creating gcs object delete request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+respBody.AccessToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("error performing gcs object delete request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		b, readErr := io.ReadAll(resp.Body)
		err := errors.New(string(b))
		t.Errorf("unexpected status code %v in gcs object delete request: %v", resp.StatusCode, errors.Join(err, readErr))
	}
}

func TestGKEServiceAccountTokenAPIImplicitly(t *testing.T) {
	// Here we do a GCS roundtrip using the Go library to test the
	// GKE Service Account Token API. The Go library will internally
	// call this API to get an Access Token for GCS operations.

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := storage.NewClient(ctx)
	if err != nil {
		t.Fatalf("error creating gcs client: %v", err)
	}
	defer client.Close()
	bkt := client.Bucket(testBucket)

	var key, value string
	var obj *storage.ObjectHandle
	for {
		key, value = uuid.NewString(), uuid.NewString()
		obj = bkt.Object(key)
		w := obj.If(storage.Conditions{DoesNotExist: true}).NewWriter(ctx)
		if _, err := w.Write([]byte(value)); err != nil {
			if !isGooglePreconditionFailed(err) {
				t.Fatalf("error writing object to bucket: %v", err)
			}
			continue
		}
		if err := w.Close(); err != nil {
			if !isGooglePreconditionFailed(err) {
				t.Fatalf("error closing object writer: %v", err)
			}
			continue
		}
		defer func() {
			if err := obj.Delete(ctx); err != nil {
				t.Errorf("error deleting object: %v", err)
			}
		}()
		break
	}
	r, err := obj.NewReader(ctx)
	if err != nil {
		t.Fatalf("error creating object reader: %v", err)
	}
	defer r.Close()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("error reading object from bucket: %v", err)
	}
	assert.Equal(t, value, string(b))
}

func isGooglePreconditionFailed(err error) bool {
	var apiErr *googleapi.Error
	return errors.As(err, &apiErr) && apiErr.Code == http.StatusPreconditionFailed
}
