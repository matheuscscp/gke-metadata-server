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

	pkghttp "github.com/matheuscscp/gke-metadata-server/internal/http"

	"google.golang.org/api/googleapi"
)

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
