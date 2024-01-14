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

package serviceaccounts

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	corev1 "k8s.io/api/core/v1"
)

type Provider interface {
	Get(ctx context.Context, namespace, name string) (*corev1.ServiceAccount, error)
}

const (
	GoogleEmailPattern = `^[a-zA-Z0-9-]+@[a-zA-Z0-9-]+\.iam\.gserviceaccount\.com$`

	gkeAnnotation = "iam.gke.io/gcp-service-account"
)

var googleEmailRegex = regexp.MustCompile(GoogleEmailPattern)

func GoogleEmail(sa *corev1.ServiceAccount) (string, error) {
	v, ok := sa.Annotations[gkeAnnotation]
	if !ok {
		return "", fmt.Errorf("annotation %s is missing for service account '%s/%s'",
			gkeAnnotation, sa.Namespace, sa.Name)
	}
	v = strings.TrimSpace(v)
	if !IsGoogleEmail(v) {
		return "", fmt.Errorf("annotation %s value '%s' does not match pattern '%s'",
			gkeAnnotation, v, GoogleEmailPattern)
	}
	return v, nil
}

func IsGoogleEmail(s string) bool {
	return googleEmailRegex.MatchString(s)
}
