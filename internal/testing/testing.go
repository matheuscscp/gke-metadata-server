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

package pkgtesting

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	jwt "github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
)

func RequestJSON(t *testing.T, headers http.Header, url, name, expectedMetadataFlavor string, obj any) {
	body := requestURL(t, headers, url, name, "application/json", expectedMetadataFlavor)
	defer body.Close()
	if err := json.NewDecoder(body).Decode(obj); err != nil {
		t.Fatalf("error decoding %s response body as json: %v", name, err)
	}
}

func RequestIDToken(t *testing.T, headers http.Header, url, name, expectedMetadataFlavor string,
	expectedAudiences, expectedIssuers []string, expectedSub string) {
	body := requestURL(t, headers, url, name, "application/text", expectedMetadataFlavor)
	defer body.Close()
	b, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("error reading %s response body as text: %v", name, err)
	}
	token, _, err := jwt.NewParser().ParseUnverified(string(b), jwt.MapClaims{})
	if err != nil {
		t.Fatalf("error parsing %s response body as jwt: %v", name, err)
	}
	aud, err := token.Claims.GetAudience()
	if err != nil {
		t.Errorf("error getting %s jwt aud claim: %v", name, err)
	}
	if nAuds := len(aud); nAuds != 1 {
		t.Errorf("jwt %s does not have exactly one aud claim: %v", name, nAuds)
	} else {
		a := aud[0]
		var found bool
		for _, expectedAud := range expectedAudiences {
			if a == expectedAud {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("jwt %s aud claim '%s' does not match any in the list [%s]", name, a, strings.Join(expectedAudiences, ", "))
		}
	}
	iss, err := token.Claims.GetIssuer()
	if err != nil {
		t.Errorf("error getting %s jwt iss claim: %v", name, err)
	}
	var found bool
	for _, expectedIss := range expectedIssuers {
		if iss == expectedIss {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("jwt %s iss claim '%s' does not match any in the list [%s]", name, iss, strings.Join(expectedIssuers, ", "))
	}
	sub, err := token.Claims.GetSubject()
	if err != nil {
		t.Errorf("error getting %s jwt sub claim: %v", name, err)
	}
	assert.Equal(t, expectedSub, sub)
	exp, err := token.Claims.GetExpirationTime()
	if err != nil {
		t.Errorf("error getting %s jwt exp claim: %v", name, err)
	}
	secs := time.Until(exp.Time).Seconds()
	assert.True(t, 3500 <= secs && secs <= 3600)
}

func requestURL(t *testing.T, headers http.Header, url, name, expectedContentType,
	expectedMetadataFlavor string) io.ReadCloser {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("error creating %s request: %v", name, err)
	}
	for k, v := range headers {
		for i := range v {
			req.Header.Add(k, v[i])
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("error requesting %s: %v", name, err)
	}
	getErr := func() error {
		defer resp.Body.Close()
		b, readErr := io.ReadAll(resp.Body)
		err := errors.New(string(b))
		return errors.Join(err, readErr)
	}
	if c := resp.StatusCode; c != 200 {
		t.Fatalf("non-200 status code %v for %s. error(s): %v", c, name, getErr())
	}
	if ct := resp.Header.Get("Content-Type"); ct != expectedContentType {
		t.Fatalf("unexpected content type %s for %s (was expecting %s). error(s): %v",
			ct, name, expectedContentType, getErr())
	}
	if mf := resp.Header.Get("Metadata-Flavor"); mf != expectedMetadataFlavor {
		t.Errorf("unexpected metadata flavor %s for %s (was expecting %s). error(s): %v",
			mf, name, expectedMetadataFlavor, getErr())
	}
	return resp.Body
}
