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

type reqStartTimeContextKey struct{}

func RespondNotFound(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNotFound)
}

func RespondErrorf(w http.ResponseWriter, r *http.Request, statusCode int, format string, a ...any) {
	err := fmt.Errorf(format, a...)
	RespondError(w, r, statusCode, err)
}

func RespondError(w http.ResponseWriter, r *http.Request, statusCode int, err error, optionalData ...any) {
	respLogFields := ResponseLogFields(statusCode)
	resp := map[string]any{
		"http_response": respLogFields,
		"error":         err.Error(),
	}

	l := logging.
		FromRequest(r).
		WithError(err).
		WithField("http_response", respLogFields)

	// any structured error data to send?
	if len(optionalData) > 0 {
		data := optionalData[0]
		resp["data"] = data
		l = l.WithField("data", data)
	}

	RespondJSON(w, r, statusCode, resp)

	t0 := r.Context().Value(reqStartTimeContextKey{}).(time.Time) // let this panic if a time is not present
	l = l.WithField("latency", LatencyLogFields(t0))
	if statusCode < 500 {
		l.Info("client error")
	} else {
		l.Error("server error")
	}
}

func RespondJSON(w http.ResponseWriter, r *http.Request, statusCode int, obj any) {
	marshal := json.Marshal
	if Pretty(r) {
		marshal = func(v any) ([]byte, error) { return json.MarshalIndent(v, "", "  ") }
	}
	b, err := marshal(obj)
	if err != nil {
		RespondErrorf(w, r, http.StatusInternalServerError, "error marshaling json response: %w", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
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
}

func RespondText(w http.ResponseWriter, r *http.Request, statusCode int, text string) {
	w.Header().Set("Content-Type", "application/text")
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
}

func ResponseLogFields(statusCode int) logrus.Fields {
	status := http.StatusText(statusCode)
	if statusCode == StatusClientClosedRequest {
		status = "Client Closed Request"
	}
	return logrus.Fields{
		"status":      status,
		"status_code": statusCode,
	}
}

func LatencyLogFields(t0 time.Time) logrus.Fields {
	latency := time.Since(t0)
	return logrus.Fields{
		"string": latency.String(),
		"nanos":  latency.Nanoseconds(),
	}
}

func StartTimeIntoRequest(r *http.Request, t0 time.Time) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), reqStartTimeContextKey{}, t0))
}

func Pretty(r *http.Request) bool {
	p := strings.ToLower(r.URL.Query().Get("pretty"))
	return p == "" || p == "true"
}
