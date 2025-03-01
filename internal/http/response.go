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

package pkghttp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/matheuscscp/gke-metadata-server/internal/logging"

	"github.com/sirupsen/logrus"
)

type requestObservabilityContextKey struct{}

type requestObservability struct {
	startTime            time.Time
	observeLatencyMillis latencyMillisObserver
}

type latencyMillisObserver func(r *http.Request, statusCode int, latencyMs float64)

type errData struct {
	err     error
	errResp []any
}

func InitRequest(r *http.Request, observeLatencyMillis latencyMillisObserver) *http.Request {
	ctx := r.Context()
	k := requestObservabilityContextKey{}
	v := requestObservability{
		startTime:            time.Now(),
		observeLatencyMillis: observeLatencyMillis,
	}
	return r.WithContext(context.WithValue(ctx, k, v))
}

func RespondNotFound(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNotFound)
}

func RespondErrorf(w http.ResponseWriter, r *http.Request, statusCode int, format string, a ...any) {
	err := fmt.Errorf(format, a...)
	RespondError(w, r, statusCode, err)
}

func RespondError(w http.ResponseWriter, r *http.Request, statusCode int, err error, errResp ...any) {
	RespondJSON(w, r, statusCode, map[string]any{
		"error":         err.Error(),
		"http_response": responseLogFields(statusCode, errResp...),
	}, errData{err, errResp})
}

func RespondJSON(w http.ResponseWriter, r *http.Request, statusCode int, obj any, optErrData ...errData) {
	b, err := json.Marshal(obj)
	if err != nil {
		RespondErrorf(w, r, http.StatusInternalServerError, "error marshaling json response: %w", err)
		return
	}

	setGKEMetadataServerHeaders(w, "application/json", statusCode)
	w.WriteHeader(statusCode)
	if n, err := w.Write(b); err != nil {
		logging.
			FromRequest(r).
			WithError(err).
			Error("error writing response")
	} else if payloadLen := len(b); n < payloadLen {
		logging.
			FromRequest(r).
			WithFields(logrus.Fields{
				"bytes_written":  n,
				"bytes_expected": payloadLen,
			}).
			Error("less response bytes written than expected")
	}

	var errData errData
	if e := optErrData; len(e) > 0 {
		errData = e[0]
	}
	observeRequest(r, statusCode, errData.err, errData.errResp...)
}

func RespondText(w http.ResponseWriter, r *http.Request, statusCode int, text string) {
	setGKEMetadataServerHeaders(w, "application/text", statusCode)
	w.WriteHeader(statusCode)
	if n, err := w.Write([]byte(text)); err != nil {
		logging.
			FromRequest(r).
			WithError(err).
			Error("error writing response")
	} else if payloadLen := len(text); n < payloadLen {
		logging.
			FromRequest(r).
			WithFields(logrus.Fields{
				"bytes_written":  n,
				"bytes_expected": payloadLen,
			}).
			Error("less response bytes written than expected")
	}

	observeRequest(r, statusCode, nil)
}

func setGKEMetadataServerHeaders(w http.ResponseWriter, contentType string, statusCode int) {
	w.Header().Set("Content-Type", contentType)
	if 200 <= statusCode && statusCode < 300 {
		w.Header().Set(MetadataFlavorHeader, MetadataFlavorGoogle)
		w.Header().Set("Server", "GKE Metadata Server")
	}
}

func responseLogFields(statusCode int, errResp ...any) logrus.Fields {
	status := http.StatusText(statusCode)
	if statusCode == StatusClientClosedRequest {
		status = "Client Closed Request"
	}
	f := logrus.Fields{
		"status":      status,
		"status_code": statusCode,
	}
	if len(errResp) > 0 {
		f["data"] = errResp[0]
	}
	return f
}

func observeRequest(r *http.Request, statusCode int, err error, errResp ...any) {
	o := r.Context().Value(requestObservabilityContextKey{}).(requestObservability)

	latency := time.Since(o.startTime)

	o.observeLatencyMillis(r, statusCode, float64(latency.Milliseconds()))

	l := logging.FromRequest(r).WithFields(logrus.Fields{
		"http_response": responseLogFields(statusCode, errResp...),
		"latency": logrus.Fields{
			"string": latency.String(),
			"nanos":  latency.Nanoseconds(),
		},
	})

	if err != nil {
		l = l.WithError(err)
	}

	switch {
	case statusCode < 400:
		if strings.HasSuffix(r.URL.Path, "/token") || strings.HasSuffix(r.URL.Path, "/identity") {
			l.Info("request")
		} else {
			l.Debug("request")
		}
	case statusCode < 500:
		l.Info("client error")
	default:
		l.Error("server error")
	}
}
