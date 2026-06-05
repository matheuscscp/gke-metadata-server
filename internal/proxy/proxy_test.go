// Copyright 2026 Matheus Pimenta.
// SPDX-License-Identifier: AGPL-3.0

package proxy

import "testing"

func TestIsMetadataRequest(t *testing.T) {
	for _, tt := range []struct {
		name string
		line string
		want bool
	}{
		// Metadata surface, must be served by the inner metadata server.
		{"root", "GET / HTTP/1.1\r\nHost: metadata\r\n\r\n", true},
		{"root with query", "GET /?recursive=true HTTP/1.1\r\n\r\n", true},
		{"computeMetadata no slash", "GET /computeMetadata HTTP/1.1\r\n\r\n", true},
		{"computeMetadata slash", "GET /computeMetadata/ HTTP/1.1\r\n\r\n", true},
		{"v1 dir", "GET /computeMetadata/v1/ HTTP/1.1\r\n\r\n", true},
		{"v1 leaf", "GET /computeMetadata/v1/instance/name HTTP/1.1\r\n\r\n", true},
		{"v1 token with query", "GET /computeMetadata/v1/instance/service-accounts/default/token?scopes=x HTTP/1.1\r\n\r\n", true},

		// Not metadata, must be proxied through to the real endpoint.
		{"proxy test path", "GET /_proxy_test HTTP/1.1\r\n\r\n", false},
		{"aws imds", "GET /latest/meta-data/ HTTP/1.1\r\n\r\n", false},
		{"computeMetadata prefix lookalike", "GET /computeMetadatax HTTP/1.1\r\n\r\n", false},
		{"non-GET method", "POST /computeMetadata/v1/x HTTP/1.1\r\n\r\n", false},
		{"empty", "", false},
		{"only method", "GET ", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := isMetadataRequest([]byte(tt.line)); got != tt.want {
				t.Fatalf("isMetadataRequest(%q) = %v, want %v", tt.line, got, tt.want)
			}
		})
	}
}

func TestRequestPathComplete(t *testing.T) {
	for _, tt := range []struct {
		name string
		buf  string
		want bool
	}{
		{"full line", "GET / HTTP/1.1", true},
		{"two spaces only", "GET /computeMetadata/v1 ", true},
		{"newline ends line", "GET /\r\n", true},
		{"method and path no trailing space", "GET /computeMetadata/v1", false},
		{"only method", "GET ", false},
		{"empty", "", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := requestPathComplete([]byte(tt.buf)); got != tt.want {
				t.Fatalf("requestPathComplete(%q) = %v, want %v", tt.buf, got, tt.want)
			}
		})
	}
}
