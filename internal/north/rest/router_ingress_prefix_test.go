// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package rest

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestSafeIngressPrefix_RejectsControlBytes pins the router's ingress
// guard to the same contract as the remote proxy's ingressBase: a
// browser strips TAB / CR / LF from a URL, so "/<TAB>/host" resolves as
// the protocol-relative "//host" the guard exists to reject. Query and
// fragment bytes are rejected for the same reason — the value is
// concatenated into a redirect target, not parsed.
func TestSafeIngressPrefix_RejectsControlBytes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		header string
		want   string
	}{
		{"plain ingress path", "/api/hassio_ingress/tok", "/api/hassio_ingress/tok"},
		{"trailing slash trimmed", "/api/hassio_ingress/tok/", "/api/hassio_ingress/tok"},
		{"protocol relative", "//evil.example", ""},
		{"backslash", "/\\evil.example", ""},
		{"tab", "/\t/evil.example", ""},
		{"newline", "/\n/evil.example", ""},
		{"space", "/ /evil.example", ""},
		{"del byte", "/\x7f/evil.example", ""},
		{"embedded backslash", "/api/\\evil.example", ""},
		{"query", "/api/x?next=//evil.example", ""},
		{"fragment", "/api/x#//evil.example", ""},
		{"absent", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
			if tc.header != "" {
				r.Header.Set("X-Ingress-Path", tc.header)
			}
			if got := safeIngressPrefix(r); got != tc.want {
				t.Fatalf("safeIngressPrefix(%q) = %q, want %q", tc.header, got, tc.want)
			}
		})
	}
}
