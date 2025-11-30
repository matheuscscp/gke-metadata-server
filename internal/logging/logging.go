// Copyright 2025 Matheus Pimenta.
// SPDX-License-Identifier: AGPL-3.0

package logging

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/go-logr/logr"
	"github.com/sirupsen/logrus"
	"k8s.io/klog/v2"
)

type loggerContextKey struct{}

type logrAdapter struct {
	logger logrus.FieldLogger
	level  logrus.Level
}

var logLevel logrus.Level = logrus.InfoLevel

func NewLogger(level logrus.Level) logrus.FieldLogger {
	logLevel = level
	l := logrus.New()
	l.SetFormatter(&logrus.JSONFormatter{
		TimestampFormat: time.RFC3339Nano,
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

func Debug() bool {
	return logLevel >= logrus.DebugLevel
}

func InitKLog(l logrus.FieldLogger, level logrus.Level) {
	klog.SetLogger(logr.New(&logrAdapter{
		logger: l,
		level:  level,
	}))
}

func (l *logrAdapter) Enabled(level int) bool {
	switch level {
	case 0: // info
		return l.level >= logrus.InfoLevel
	case 1: // debug
		return l.level >= logrus.DebugLevel
	case 2: // trace
		return l.level >= logrus.TraceLevel
	default:
		return false
	}
}

func (l *logrAdapter) Error(err error, msg string, keysAndValues ...any) {
	l.logger.WithError(err).WithFields(keysAndValuesToFields(keysAndValues)).Error(msg)
}

func (l *logrAdapter) Info(level int, msg string, keysAndValues ...any) {
	switch level {
	case 0: // info
		l.logger.WithFields(keysAndValuesToFields(keysAndValues)).Info(msg)
	case 1: // debug
		l.logger.WithFields(keysAndValuesToFields(keysAndValues)).Debug(msg)
	case 2: // trace
		l.logger.WithFields(keysAndValuesToFields(keysAndValues)).Trace(msg)
	}
}

func (l *logrAdapter) Init(logr.RuntimeInfo) {
}

func (l *logrAdapter) WithName(name string) logr.LogSink {
	return &logrAdapter{
		logger: l.logger.WithField("name", name),
		level:  l.level,
	}
}

func (l *logrAdapter) WithValues(keysAndValues ...any) logr.LogSink {
	return &logrAdapter{
		logger: l.logger.WithFields(keysAndValuesToFields(keysAndValues)),
		level:  l.level,
	}
}

func keysAndValuesToFields(keysAndValues []any) logrus.Fields {
	fields := logrus.Fields{}
	for i := 0; i < len(keysAndValues); i += 2 {
		k := fmt.Sprint(keysAndValues[i])
		if i+1 >= len(keysAndValues) {
			fields[k] = "<missing>"
		} else {
			fields[k] = keysAndValues[i+1]
		}
	}
	return fields
}
