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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
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
	expectedAudience, expectedIssuer, expectedSubject string) string {
	body := requestURL(t, headers, url, name, "application/text", expectedMetadataFlavor)
	defer body.Close()
	b, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("error reading %s response body as text: %v", name, err)
	}
	rawToken := string(b)
	token, _, err := jwt.NewParser().ParseUnverified(rawToken, jwt.MapClaims{})
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
		CheckRegex(t, name, expectedAudience, aud[0])
	}
	iss, err := token.Claims.GetIssuer()
	if err != nil {
		t.Errorf("error getting %s jwt iss claim: %v", name, err)
	}
	CheckRegex(t, name, expectedIssuer, iss)
	sub, err := token.Claims.GetSubject()
	if err != nil {
		t.Errorf("error getting %s jwt sub claim: %v", name, err)
	}
	CheckRegex(t, name, expectedSubject, sub)
	exp, err := token.Claims.GetExpirationTime()
	if err != nil {
		t.Errorf("error getting %s jwt exp claim: %v", name, err)
	}
	secs := int(time.Until(exp.Time).Seconds())
	AssertExpirationSeconds(t, secs)
	return rawToken
}

func RequestText(t *testing.T, headers http.Header, url, name string) string {
	body := requestURL(t, headers, url, name, "text/plain", "")
	defer body.Close()
	b, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("error reading %s response body as text: %v", name, err)
	}
	return string(b)
}

func requestURL(t *testing.T, headers http.Header, url, name, expectedContentType,
	expectedMetadataFlavor string) io.ReadCloser {
	for i := 0; ; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		readCloser, err := doRequestURL(ctx, headers, url, name, expectedContentType, expectedMetadataFlavor)
		if err == nil {
			return readCloser
		}
		if !errors.Is(err, context.Canceled) || i == 3 {
			t.Fatal(err.Error())
		}
	}
}

func doRequestURL(ctx context.Context, headers http.Header, url, name, expectedContentType,
	expectedMetadataFlavor string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("error creating %s request: %w", name, err)
	}
	for k, v := range headers {
		for i := range v {
			req.Header.Add(k, v[i])
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error requesting %s: %w", name, err)
	}
	getErr := func() error {
		defer resp.Body.Close()
		b, readErr := io.ReadAll(resp.Body)
		err := errors.New(string(b))
		return errors.Join(err, readErr)
	}
	if c := resp.StatusCode; c != 200 {
		return nil, fmt.Errorf("non-200 status code %v for %s. error(s): %w", c, name, getErr())
	}
	if ct := resp.Header.Get("Content-Type"); ct != expectedContentType {
		return nil, fmt.Errorf("unexpected content type %s for %s (was expecting %s). error(s): %w",
			ct, name, expectedContentType, getErr())
	}
	if mf := resp.Header.Get("Metadata-Flavor"); mf != expectedMetadataFlavor {
		return nil, fmt.Errorf("unexpected metadata flavor %s for %s (was expecting '%s'). error(s): %w",
			mf, name, expectedMetadataFlavor, getErr())
	}
	return resp.Body, nil
}

func EvalEnv(s string) string {
	return strings.ReplaceAll(s, "TEST_ID", os.Getenv("TEST_ID"))
}

func CheckRegex(t *testing.T, name, pattern, value string) {
	pattern = "^" + EvalEnv(pattern) + "$"
	re, err := regexp.Compile(pattern)
	if err != nil {
		t.Errorf("error compiling regex %s for %s: %v", pattern, name, err)
		return
	}
	if !re.MatchString(value) {
		t.Errorf("value '%s' does not match regex %s for %s", value, pattern, name)
	}
}

func AssertExpirationSeconds(t *testing.T, secs int) {
	assert.LessOrEqual(t, 3500, secs)
	assert.LessOrEqual(t, secs, 3600)
}
