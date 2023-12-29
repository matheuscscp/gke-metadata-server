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

import "net/http"

const (
	StatusClientClosedRequest = 499
)

type StatusRecorder struct {
	http.ResponseWriter
	statusCode        int
	writeHeaderCalled bool
}

func (s *StatusRecorder) Write(b []byte) (int, error) {
	if !s.writeHeaderCalled {
		s.WriteHeader(s.StatusCode())
	}
	return s.ResponseWriter.Write(b)
}

func (s *StatusRecorder) WriteHeader(statusCode int) {
	s.statusCode = statusCode
	s.writeHeaderCalled = true
	s.ResponseWriter.WriteHeader(statusCode)
}

func (s *StatusRecorder) StatusCode() int {
	if !s.writeHeaderCalled {
		return http.StatusOK
	}
	return s.statusCode
}

func (s *StatusRecorder) WriteHeaderCalled() bool {
	return s.writeHeaderCalled
}
