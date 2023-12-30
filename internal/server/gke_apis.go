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
	"net/http"
	"strings"

	pkghttp "github.com/matheuscscp/gke-metadata-server/internal/http"
	"github.com/matheuscscp/gke-metadata-server/internal/logging"

	"golang.org/x/oauth2/google"
	"google.golang.org/api/idtoken"
	"google.golang.org/api/option"
)

func (s *Server) gkeNodeNameAPI(w http.ResponseWriter, r *http.Request) {
	pkghttp.RespondText(w, r, http.StatusOK, s.opts.NodeName)
}

func (s *Server) gkeServiceAccountAliasesAPI(w http.ResponseWriter, r *http.Request) {
	pkghttp.RespondText(w, r, http.StatusOK, "default\n")
}

func (s *Server) gkeServiceAccountEmailAPI(w http.ResponseWriter, r *http.Request) {
	podGoogleServiceAccountEmail, r, err := s.getPodGoogleServiceAccountEmail(w, r)
	if err != nil {
		return
	}
	pkghttp.RespondText(w, r, http.StatusOK, podGoogleServiceAccountEmail)
}

func (s *Server) gkeServiceAccountIdentityAPI(w http.ResponseWriter, r *http.Request) {
	audience := strings.TrimSpace(r.URL.Query().Get("audience"))
	if audience == "" {
		w.WriteHeader(http.StatusBadRequest)
		if _, err := fmt.Fprintln(w, "non-empty audience parameter required"); err != nil {
			logging.FromRequest(r).WithError(err).Error("error writing audience error response")
		}
		return
	}

	s.runWithGoogleCredentialsFromPodServiceAccountToken(w, r, func(creds *google.Credentials) {
		source, err := idtoken.NewTokenSource(r.Context(), audience, option.WithCredentials(creds))
		if err != nil {
			pkghttp.RespondErrorf(w, r, http.StatusInternalServerError,
				"error getting google id token source: %w", err)
			return
		}
		tok, err := source.Token()
		if err != nil {
			respondGoogleAPIErrorf(w, r, "error getting google id token: %w", err)
			return
		}
		pkghttp.RespondText(w, r, http.StatusOK, tok.AccessToken)
	})
}

func (s *Server) gkeServiceAccountScopesAPI(w http.ResponseWriter, r *http.Request) {
	pkghttp.RespondText(w, r, http.StatusOK, strings.Join(gkeAccessScopes(), "\n")+"\n")
}

func (s *Server) gkeServiceAccountTokenAPI(w http.ResponseWriter, r *http.Request) {
	s.runWithGoogleCredentialsFromPodServiceAccountToken(w, r, func(creds *google.Credentials) {
		tok, err := creds.TokenSource.Token()
		if err != nil {
			respondGoogleAPIErrorf(w, r, "error getting google access token: %w", err)
			return
		}
		pkghttp.RespondJSON(w, r, http.StatusOK, map[string]any{
			"access_token": strings.TrimSpace(tok.AccessToken),
			"expires_in":   tokenExpirationSeconds - 10,
			"token_type":   "Bearer",
		})
	})
}
