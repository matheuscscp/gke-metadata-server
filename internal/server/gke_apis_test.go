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
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"testing"
	"time"

	"cloud.google.com/go/compute/metadata"
	"cloud.google.com/go/storage"
	"github.com/coreos/go-oidc/v3/oidc"
	jwt "github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	const expectedAudience = "test.com"
	const expectedIssuer = "https://accounts.google.com"
	const expectedSubject = `^\d{20,30}$`
	const url = "http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/identity?audience=" + expectedAudience

	// request google id token
	rawToken := requestURL(t, gkeHeaders, url, "application/text", gkeMetadataFlavor)
	unverifiedToken, _, err := jwt.NewParser().ParseUnverified(rawToken, jwt.MapClaims{})
	require.NoError(t, err)
	aud, err := unverifiedToken.Claims.GetAudience()
	if err != nil {
		t.Errorf("error getting google id token jwt aud claim: %v", err)
	} else if n := len(aud); n != 1 {
		t.Errorf("google id token does not have exactly one aud claim: %v", n)
	} else {
		assert.Equal(t, expectedAudience, aud[0])
	}
	iss, err := unverifiedToken.Claims.GetIssuer()
	if err != nil {
		t.Errorf("error getting google id token jwt iss claim: %v", err)
	} else {
		assert.Equal(t, expectedIssuer, iss)
	}
	sub, err := unverifiedToken.Claims.GetSubject()
	if err != nil {
		t.Errorf("error getting google id token jwt sub claim: %v", err)
	} else {
		subjectRegex := regexp.MustCompile(expectedSubject)
		assert.True(t, subjectRegex.MatchString(sub))
	}
	exp, err := unverifiedToken.Claims.GetExpirationTime()
	if err != nil {
		t.Errorf("error getting google id token jwt exp claim: %v", err)
	} else {
		assertExpirationSeconds(t, int(time.Until(exp.Time).Seconds()))
	}
	require.False(t, t.Failed())

	// verify token
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	google, err := oidc.NewProvider(ctx, expectedIssuer)
	require.NoError(t, err)
	token, err := google.
		VerifierContext(ctx, &oidc.Config{ClientID: expectedAudience}).
		Verify(ctx, rawToken)
	require.NoError(t, err)
	var claims struct {
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
	}
	err = token.Claims(&claims)
	require.NoError(t, err)
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

	// request google access token
	respBodyStr := requestURL(t, gkeHeaders, url, "application/json", gkeMetadataFlavor)
	err := json.Unmarshal([]byte(respBodyStr), &respBody)
	require.NoError(t, err)
	assertExpirationSeconds(t, respBody.ExpiresIn)
	assert.Equal(t, "Bearer", respBody.TokenType)
	require.False(t, t.Failed())

	// now let's use this token in real calls to the GCS HTTP API

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// create an object with a random key and value
	var key, value string
	for {
		key, value = uuid.NewString(), uuid.NewString()
		uploadURL := "https://storage.googleapis.com/upload/storage/v1/b/" + testBucket + "/o?uploadType=media&name=" + key
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, bytes.NewReader([]byte(value)))
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+respBody.AccessToken)
		req.Header.Set("Content-Type", "text/plain")
		req.Header.Set("If-None-Match", "*")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
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
	const authzHeader = "Authorization"
	bearerToken := "Bearer " + respBody.AccessToken
	headers := http.Header{
		authzHeader: []string{bearerToken},
	}
	getObjectURL := "https://storage.googleapis.com/storage/v1/b/" + testBucket + "/o/" + key + "?alt=media"
	text := requestURL(t, headers, getObjectURL, "text/plain", "")
	assert.Equal(t, value, text)

	// delete object
	deleteURL := "https://storage.googleapis.com/storage/v1/b/" + testBucket + "/o/" + key
	req, err := http.NewRequest(http.MethodDelete, deleteURL, nil)
	require.NoError(t, err)
	req.Header.Set(authzHeader, bearerToken)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
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

	require.True(t, metadata.OnGCE())

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := storage.NewClient(ctx)
	require.NoError(t, err)
	defer client.Close()
	bkt := client.Bucket(testBucket)

	var key, value string
	var obj *storage.ObjectHandle
	for {
		key, value = uuid.NewString(), uuid.NewString()
		obj = bkt.Object(key)
		w := obj.If(storage.Conditions{DoesNotExist: true}).NewWriter(ctx)
		if _, err := w.Write([]byte(value)); err != nil {
			require.True(t, isGooglePreconditionFailed(err))
			continue
		}
		if err := w.Close(); err != nil {
			require.True(t, isGooglePreconditionFailed(err))
			continue
		}
		defer func() {
			err := obj.Delete(ctx)
			assert.NoError(t, err)
		}()
		break
	}
	r, err := obj.NewReader(ctx)
	require.NoError(t, err)
	defer r.Close()
	b, err := io.ReadAll(r)
	require.NoError(t, err)
	assert.Equal(t, value, string(b))
}

func requestURL(t *testing.T, headers http.Header, url, expectedContentType,
	expectedMetadataFlavor string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	require.NoError(t, err)
	for k, v := range headers {
		for i := range v {
			req.Header.Add(k, v[i])
		}
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, expectedContentType, resp.Header.Get("Content-Type"))
	assert.Equal(t, expectedMetadataFlavor, resp.Header.Get("Metadata-Flavor"))
	b, err := io.ReadAll(resp.Body)
	if t.Failed() {
		require.NoError(t, err)
		t.Fatalf("error making request: %s", string(b))
	}
	require.NoError(t, err)
	return string(b)
}

func assertExpirationSeconds(t *testing.T, secs int) {
	t.Helper()
	assert.LessOrEqual(t, 3400, secs)
	assert.LessOrEqual(t, secs, 3600)
}

func isGooglePreconditionFailed(err error) bool {
	var apiErr *googleapi.Error
	return errors.As(err, &apiErr) && apiErr.Code == http.StatusPreconditionFailed
}
