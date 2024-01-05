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

package server

import (
	"fmt"
)

func (s *Server) getGoogleCredentialConfig(googleServiceAccountEmail string, credSource map[string]any) any {
	impersonationURL := fmt.Sprintf(
		"https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/%s:generateAccessToken",
		googleServiceAccountEmail)

	return map[string]any{
		"type":                              "external_account",
		"audience":                          s.opts.WorkloadIdentityProviderAudience,
		"subject_token_type":                "urn:ietf:params:oauth:token-type:jwt",
		"token_url":                         "https://sts.googleapis.com/v1/token",
		"credential_source":                 credSource,
		"service_account_impersonation_url": impersonationURL,
		"service_account_impersonation": map[string]any{
			"token_lifetime_seconds": s.opts.TokenExpirationSeconds,
		},
	}
}

func WorkloadIdentityProviderAudience(workloadIdentityProvider string) string {
	return fmt.Sprintf("//iam.googleapis.com/%s", workloadIdentityProvider)
}

func gkeAccessScopes() []string {
	return []string{
		"https://www.googleapis.com/auth/cloud-platform",
		"https://www.googleapis.com/auth/userinfo.email",
	}
}
