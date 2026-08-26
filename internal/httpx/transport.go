// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package httpx holds the daemon's HTTP client construction.
//
// It exists for one invariant: every [http.Client] the daemon builds owns
// its transport. Leaving [http.Client.Transport] nil falls back to the
// process-wide [http.DefaultTransport], which couples otherwise unrelated
// callers through a single connection pool — the readiness probe, SSDP
// discovery, a firmware download and the JSON-RPC client would all share
// one. Anything that closes idle connections on that transport then tears
// down requests the others have in flight, surfacing as
// `transport connection broken: http: CloseIdleConnections called`.
//
// The sharpest instance is the test suite: [httptest.Server.Close] calls
// CloseIdleConnections on http.DefaultTransport unconditionally — the
// stdlib documents this as a courtesy to users of the default transport —
// so in a package with parallel tests, one server shutting down breaks a
// request another test is making. That is a real failure of the code
// under test, not test noise: the same coupling exists in production, it
// simply has no equally reliable trigger there.
package httpx

import (
	"net/http"
	"time"
)

// NewClient returns an [http.Client] with the given timeout and its own
// transport, cloned from [http.DefaultTransport] so the stdlib defaults
// (proxy handling, dial and TLS timeouts, HTTP/2) are preserved.
//
// Prefer this over `&http.Client{Timeout: d}` everywhere;
// `TestEveryHTTPClientOwnsItsTransport` enforces it.
func NewClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: NewTransport()}
}

// NewTransport returns a private clone of [http.DefaultTransport].
// Callers that need to customise the transport — TLS configuration
// above all — clone here and adjust the result rather than mutating the
// shared default.
func NewTransport() *http.Transport {
	if t, ok := http.DefaultTransport.(*http.Transport); ok {
		return t.Clone()
	}
	// http.DefaultTransport is *http.Transport in every supported Go
	// release; the fallback keeps the contract (a transport of our own)
	// rather than silently handing back nil and reinstating the default.
	return &http.Transport{}
}
