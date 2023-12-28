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

package logging

import (
	"context"
	"net/http"
	"time"

	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
)

type loggerContextKey struct{}

var logLevel logrus.Level = logrus.InfoLevel

func ShouldLog(thisLevel logrus.Level) bool {
	return thisLevel <= logLevel
}

func NewLogger(level logrus.Level) logrus.FieldLogger {
	logLevel = level
	l := logrus.New()
	l.SetFormatter(&logrus.JSONFormatter{
		TimestampFormat: time.RFC3339,
	})
	l.SetLevel(level)
	return l
}

func FromRequest(r *http.Request) logrus.FieldLogger {
	return FromContext(r.Context())
}

func FromContext(ctx context.Context) logrus.FieldLogger {
	if v := ctx.Value(loggerContextKey{}); v != nil {
		if l, ok := v.(logrus.FieldLogger); ok && l != nil {
			return l
		}
	}
	return NewLogger(logLevel)
}

func IntoRequest(r *http.Request, l logrus.FieldLogger) *http.Request {
	ctxWithLogger := IntoContext(r.Context(), l)
	return r.WithContext(ctxWithLogger)
}

func IntoContext(ctx context.Context, l logrus.FieldLogger) context.Context {
	return context.WithValue(ctx, loggerContextKey{}, l)
}

func Pod(pod *corev1.Pod) logrus.Fields {
	return logrus.Fields{
		"name":            pod.Name,
		"namespace":       pod.Namespace,
		"service_account": pod.Spec.ServiceAccountName,
		"ip":              pod.Status.PodIP,
	}
}
