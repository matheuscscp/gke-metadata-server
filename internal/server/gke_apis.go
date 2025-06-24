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
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/matheuscscp/gke-metadata-server/internal/googlecredentials"
	pkghttp "github.com/matheuscscp/gke-metadata-server/internal/http"
	"github.com/matheuscscp/gke-metadata-server/internal/logging"
	"github.com/matheuscscp/gke-metadata-server/internal/serviceaccounts"

	"google.golang.org/api/googleapi"
)

func (s *Server) gkeNodeNameAPI() pkghttp.MetadataHandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) (any, error) {
		return s.opts.NodeName, nil
	}
}

func (s *Server) gkeProjectIDAPI() pkghttp.MetadataHandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) (any, error) {
		return s.opts.ProjectID, nil
	}
}

func (s *Server) gkeNumericProjectIDAPI() pkghttp.MetadataHandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) (any, error) {
		return s.opts.NumericProjectID, nil
	}
}

func (s *Server) gkeServiceAccountAliasesAPI() pkghttp.MetadataHandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) (any, error) {
		return []string{"default"}, nil
	}
}

func (s *Server) gkeServiceAccountEmailAPI() pkghttp.MetadataHandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) (any, error) {
		email, _, err := s.getPodGoogleServiceAccountEmailOrWorkloadIdentityPool(w, r)
		if err != nil {
			return nil, err
		}
		return email, nil
	}
}

func (s *Server) gkeServiceAccountIdentityAPI() pkghttp.MetadataHandler {
	mh := func(w http.ResponseWriter, r *http.Request) (any, error) {

		// validate audience
		audience := strings.TrimSpace(r.URL.Query().Get("audience"))
		if audience == "" {
			w.WriteHeader(http.StatusBadRequest)
			if _, err := fmt.Fprintln(w, "non-empty audience parameter required"); err != nil {
				logging.FromRequest(r).WithError(err).Error("error writing audience error response")
			}
			return nil, fmt.Errorf("non-empty audience parameter required")
		}

		// ensure the pod has a target google service account
		googleEmail, r, err := s.getPodGoogleServiceAccountEmail(w, r)
		if err != nil {
			return nil, err
		}
		if googleEmail == nil {
			saRef := r.Context().Value(podServiceAccountReferenceContextKey{}).(*serviceaccounts.Reference)
			msg := fmt.Sprintf(`Your Kubernetes service account (%s/%s) is not annotated with a target Google service account, which is a requirement for retrieving Identity Tokens using Workload Identity.
Please add the iam.gke.io/gcp-service-account=[GSA_NAME]@[PROJECT_ID] annotation to your Kubernetes service account.
Refer to https://cloud.google.com/kubernetes-engine/docs/how-to/workload-identity
`, saRef.Namespace, saRef.Name)
			pkghttp.RespondText(w, r, http.StatusNotFound, msg)
			return nil, fmt.Errorf("pod service account not annotated with target Google service account")
		}

		// get the identity token
		saRef, r, err := s.getPodServiceAccountReference(w, r)
		if err != nil {
			return nil, err
		}
		accessTokens, _, r, err := s.getPodGoogleAccessTokens(w, r, nil)
		if err != nil {
			return nil, err
		}
		identityToken, _, err := s.opts.ServiceAccountTokens.GetGoogleIdentityToken(
			r.Context(), saRef, accessTokens.DirectAccess, *googleEmail, audience)
		if err != nil {
			respondGoogleAPIErrorf(w, r, "error getting google id token: %w", err)
			return nil, err
		}

		return identityToken, nil
	}

	return pkghttp.TokenHandler{MetadataHandler: pkghttp.MetadataHandlerFunc(mh)}
}

func (s *Server) gkeServiceAccountScopesAPI() pkghttp.MetadataHandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) (any, error) {
		return googlecredentials.AccessScopes(), nil
	}
}

func (s *Server) gkeServiceAccountTokenAPI() pkghttp.MetadataHandler {
	mh := func(w http.ResponseWriter, r *http.Request) (any, error) {
		var scopes []string
		for scope := range strings.SplitSeq(r.URL.Query().Get("scopes"), ",") {
			if s := strings.TrimSpace(scope); s != "" {
				scopes = append(scopes, s)
			}
		}
		tokens, expiresAt, _, err := s.getPodGoogleAccessTokens(w, r, scopes)
		if err != nil {
			return nil, err
		}
		token := tokens.DirectAccess
		if t := tokens.Impersonated; t != "" {
			token = t
		}
		return map[string]any{
			"access_token": token,
			"expires_in":   int(time.Until(expiresAt).Seconds()),
			"token_type":   "Bearer",
		}, nil
	}

	return pkghttp.TokenHandler{MetadataHandler: pkghttp.MetadataHandlerFunc(mh)}
}

func respondGoogleAPIErrorf(w http.ResponseWriter, r *http.Request, format string, err error) {
	const oauthSubstring = "oauth2/google: status code "

	statusCode := http.StatusInternalServerError
	var bodyString string

	switch apiErr, strErr := (*googleapi.Error)(nil), err.Error(); {
	case errors.As(err, &apiErr):
		err = apiErr
		statusCode = apiErr.Code
		bodyString = apiErr.Body
	case strings.Contains(strErr, oauthSubstring):
		strErr = strErr[strings.Index(strErr, oauthSubstring)+len(oauthSubstring):]
		if idx := strings.Index(strErr, ":"); idx >= 0 {
			fmt.Sscan(strErr[:idx], &statusCode)
			bodyString = strErr[idx+2:]
		}
	}
	err = fmt.Errorf(format, err)

	var body any
	if json.Unmarshal([]byte(bodyString), &body) == nil && body != nil {
		pkghttp.RespondError(w, r, statusCode, err, body)
		return
	}

	if bodyString != "" {
		pkghttp.RespondError(w, r, statusCode, err, bodyString)
		return
	}

	pkghttp.RespondError(w, r, statusCode, err)
}
