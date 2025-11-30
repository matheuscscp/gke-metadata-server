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

package retry

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	pkghttp "github.com/matheuscscp/gke-metadata-server/internal/http"
	"github.com/matheuscscp/gke-metadata-server/internal/logging"
	pkgtime "github.com/matheuscscp/gke-metadata-server/internal/time"

	"github.com/prometheus/client_golang/prometheus"
)

type (
	Operation struct {
		MaxAttempts    int           // default: 3. use negative for infinity
		InitialDelay   time.Duration // default: time.Second
		MaxDelay       time.Duration // default: 30 * time.Second
		Description    string
		FailureCounter prometheus.Counter
		Func           func() error
		IsRetryable    func(error) bool
	}

	maxAttemptsError struct {
		desc        string
		maxAttempts int
		lastErr     error
	}

	contextCanceledError struct {
		desc    string
		lastErr error
	}
)

func Do(ctx context.Context, op Operation) error {
	if op.Description == "" {
		return fmt.Errorf("a retryable operation must have a description")
	}
	if op.Func == nil {
		return fmt.Errorf("a retryable operation must have a function to be called")
	}
	if op.FailureCounter == nil {
		return fmt.Errorf("a retryable operation must have a failure counter for observability")
	}
	if op.IsRetryable == nil {
		return fmt.Errorf("a retryable operation must have a function to check if an error is retryable")
	}

	if op.MaxAttempts == 0 {
		op.MaxAttempts = 3
	}
	if op.InitialDelay <= 0 {
		op.InitialDelay = time.Second
	}
	if op.MaxDelay <= 0 {
		op.MaxDelay = 30 * time.Second
	}
	if op.MaxDelay < op.InitialDelay {
		op.MaxDelay = op.InitialDelay
	}

	l := logging.FromContext(ctx)
	for i := 1; op.MaxAttempts < 0 || i <= op.MaxAttempts; i++ {
		err := op.Func()
		if err == nil || !op.IsRetryable(err) {
			return err
		}
		op.FailureCounter.Inc()
		if i == op.MaxAttempts {
			return &maxAttemptsError{op.Description, op.MaxAttempts, err}
		}
		expDelay := (1 << (i - 1)) * op.InitialDelay
		if i > 11 || expDelay > op.MaxDelay {
			expDelay = op.MaxDelay
		}
		l := l.WithError(err)
		logf := l.Warnf
		if i == 1 { // do not warn about the first attempt failure since it's fairly common
			logf = l.Debugf
		}
		logf("error trying to %s. retrying after %v...", op.Description, expDelay)
		if err := pkgtime.SleepContext(ctx, expDelay); err != nil {
			return &contextCanceledError{op.Description, err}
		}
	}
	return nil
}

func HTTPStatusCode(err error) int {
	switch {
	case errors.Is(err, &maxAttemptsError{}):
		return http.StatusTooManyRequests
	case errors.Is(err, &contextCanceledError{}):
		return pkghttp.StatusClientClosedRequest
	default:
		return http.StatusInternalServerError
	}
}

func (m *maxAttemptsError) Error() string {
	return fmt.Errorf("reached max attempts (%v) for trying to %s. last attempt error: %w",
		m.maxAttempts, m.desc, m.lastErr).Error()
}

func (*maxAttemptsError) Is(target error) bool {
	_, is := target.(*maxAttemptsError)
	return is
}

func (c *contextCanceledError) Error() string {
	return fmt.Errorf("context canceled while trying to %s. last attempt error: %w",
		c.desc, c.lastErr).Error()
}

func (*contextCanceledError) Is(target error) bool {
	_, is := target.(*contextCanceledError)
	return is
}
