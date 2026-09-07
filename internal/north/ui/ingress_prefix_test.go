// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package ui

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestIngressPrefixRejectsControlBytes pins the no-JS surface's ingress
// prefix to the same rule the REST router applies: a prefix carrying a
// control byte, a backslash or a query/fragment separator is refused
// rather than folded into a redirect Location a browser would resolve
// cross-origin.
func TestIngressPrefixRejectsControlBytes(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"/api/hassio_ingress/abc/": "/api/hassio_ingress/abc",
		"/\t/evil.example":         "",
		"/a b":                     "",
		"/a\\b":                    "",
		"/a?b":                     "",
		"/a#b":                     "",
		"/a\x7f":                   "",
		"//evil.example":           "",
		"relative":                 "",
	}
	for header, want := range cases {
		r := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		r.Header.Set("X-Ingress-Path", header)
		if got := ingressPrefix(r); got != want {
			t.Errorf("ingressPrefix(%q) = %q, want %q", header, got, want)
		}
	}
}
