// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ws

import (
	"bufio"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// wsHandshakeStatus performs a raw WebSocket upgrade handshake against server,
// optionally sending an Origin header (when origin != ""), and returns the
// HTTP status code of the server's response.
func wsHandshakeStatus(t *testing.T, server *httptest.Server, origin string) int {
	t.Helper()
	return wsHandshakeStatusWithHeaders(t, server, origin, nil)
}

// wsHandshakeStatusWithHeaders is wsHandshakeStatus with additional request
// headers (e.g. X-Forwarded-Host to simulate a reverse proxy that rewrites the
// upstream Host).
func wsHandshakeStatusWithHeaders(t *testing.T, server *httptest.Server, origin string, extra map[string]string) int {
	t.Helper()
	wsURL, _ := url.Parse(server.URL)
	conn, err := net.Dial("tcp", wsURL.Host)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	req := "GET /ws HTTP/1.1\r\n" +
		"Host: " + wsURL.Host + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + genWSKey(t) + "\r\n" +
		"Sec-WebSocket-Version: 13\r\n"
	if origin != "" {
		req += "Origin: " + origin + "\r\n"
	}
	for k, v := range extra {
		req += k + ": " + v + "\r\n"
	}
	req += "\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatalf("write handshake: %v", err)
	}

	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("read handshake response: %v", err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

// TestWSHandlerOriginAllowlist pins the CSRF Origin policy: with a non-empty
// allowlist a matching Origin upgrades and a mismatched Origin is rejected,
// while a request carrying no Origin (a non-browser API-token client) is
// allowed through — browsers always attach an Origin to WS handshakes, so an
// absent Origin cannot be a forged cross-site request.
func TestWSHandlerOriginAllowlist(t *testing.T) {
	t.Parallel()
	hub, _, _, _, _, _ := newTestHub(t)
	const allowed = "http://localhost:9999"
	server := httptest.NewServer(Handler(hub, nil, []string{allowed}))
	t.Cleanup(server.Close)

	tests := []struct {
		name   string
		origin string
		want   int
	}{
		{name: "missing origin is allowed (non-browser client)", origin: "", want: http.StatusSwitchingProtocols},
		{name: "matching origin is allowed", origin: allowed, want: http.StatusSwitchingProtocols},
		{name: "mismatched origin is rejected", origin: "http://evil.example", want: http.StatusForbidden},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := wsHandshakeStatus(t, server, tc.origin); got != tc.want {
				t.Fatalf("status = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestWSHandlerSameOriginAllowed pins that a same-origin handshake (the
// Origin's host equals the request Host) is accepted even when that origin
// is NOT in the allow-list — same-origin is never a CSRF vector, so the
// SPA connects on whatever authority the daemon is reached on, not just the
// localhost self-origin the allow-list derives.
func TestWSHandlerSameOriginAllowed(t *testing.T) {
	t.Parallel()
	hub, _, _, _, _, _ := newTestHub(t)
	// Allow-list deliberately excludes the server's own origin.
	server := httptest.NewServer(Handler(hub, nil, []string{"http://localhost:9999"}))
	t.Cleanup(server.Close)

	wsURL, _ := url.Parse(server.URL)
	sameOrigin := "http://" + wsURL.Host // host matches the request Host header
	if got := wsHandshakeStatus(t, server, sameOrigin); got != http.StatusSwitchingProtocols {
		t.Fatalf("same-origin handshake: status = %d, want %d", got, http.StatusSwitchingProtocols)
	}
}

// TestWSHandlerForwardedHostAllowed pins the reverse-proxy case: the browser's
// Origin identifies the external authority (e.g. https://loom.example) while a
// proxy rewrites the request Host to the internal upstream, so the naive
// Origin==Host same-origin check fails. When the proxy records the external
// host in X-Forwarded-Host and that matches the Origin's host, the handshake is
// same-origin and must be allowed even though the external origin is not in the
// allow-list. Without this the SPA's live WebSocket 403s in a reconnect loop
// behind any proxy that does not preserve the original Host header.
func TestWSHandlerForwardedHostAllowed(t *testing.T) {
	t.Parallel()
	hub, _, _, _, _, _ := newTestHub(t)
	// Allow-list excludes the external origin — only X-Forwarded-Host makes it
	// same-origin.
	server := httptest.NewServer(Handler(hub, nil, []string{"http://localhost:9999"}))
	t.Cleanup(server.Close)

	const externalHost = "loom.example"
	got := wsHandshakeStatusWithHeaders(t, server,
		"https://"+externalHost,
		map[string]string{"X-Forwarded-Host": externalHost})
	if got != http.StatusSwitchingProtocols {
		t.Fatalf("proxy-forwarded same-origin handshake: status = %d, want %d", got, http.StatusSwitchingProtocols)
	}

	// A genuinely cross-site page still fails: its Origin matches neither the
	// request Host nor the proxy's X-Forwarded-Host.
	cross := wsHandshakeStatusWithHeaders(t, server,
		"https://evil.example",
		map[string]string{"X-Forwarded-Host": externalHost})
	if cross != http.StatusForbidden {
		t.Fatalf("cross-site handshake behind proxy: status = %d, want %d", cross, http.StatusForbidden)
	}
}
