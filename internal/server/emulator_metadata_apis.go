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

	pkghttp "github.com/matheuscscp/gke-metadata-server/internal/http"
)

func (s *Server) emuPodGoogleCredConfigAPI(w http.ResponseWriter, r *http.Request) {
	podGoogleServiceAccountEmail, r, err := s.getPodGoogleServiceAccountEmail(w, r)
	if err != nil {
		return
	}
	credConfig := s.getGoogleCredentialConfig(podGoogleServiceAccountEmail, map[string]any{
		"format": map[string]string{"type": "text"},
		"url":    fmt.Sprintf("http://%s%s", r.Host, emuPodServiceAccountTokenAPI),
		"headers": map[string]string{
			metadataFlavorHeader: metadataFlavorEmulator,
		},
	})
	pkghttp.RespondJSON(w, r, http.StatusOK, credConfig)
}

func (s *Server) emuPodServiceAccountTokenAPI(w http.ResponseWriter, r *http.Request) {
	saToken, r, err := s.getPodServiceAccountToken(w, r)
	if err != nil {
		return
	}
	pkghttp.RespondText(w, r, http.StatusOK, saToken)
}
