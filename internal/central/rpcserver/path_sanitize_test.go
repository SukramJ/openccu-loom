// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package rpcserver

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestRouteFromPathRejectsTraversal is a contract test that pins the
// XML-RPC callback router to a strict allowlist on the central-name
// segment. Any path shape outside `/RPC2/[A-Za-z0-9_-]+` is rejected
// up-front so untrusted CCUs cannot misroute callbacks between
// centrals via path traversal or encoded slashes.
func TestRouteFromPathRejectsTraversal(t *testing.T) {
	cases := []struct {
		name string
		path string
		ok   bool
		want string
	}{
		// Happy paths — accepted shapes.
		{"plain", "/RPC2/ccu-01", true, "ccu-01"},
		{"underscore", "/RPC2/ccu_01", true, "ccu_01"},
		{"alnum", "/RPC2/ccuMain42", true, "ccuMain42"},

		// Reject — wrong prefix.
		{"no-prefix", "/RPC1/ccu-01", false, ""},
		{"empty-segment", "/RPC2/", false, ""},
		{"trailing-slash", "/RPC2/ccu-01/", false, ""},

		// Reject — traversal / multi-segment / encoded shapes.
		{"dotdot", "/RPC2/..", false, ""},
		{"dotdot-target", "/RPC2/../other", false, ""},
		{"multi-segment", "/RPC2/ccu1/extra", false, ""},
		// Note: net/http decodes %2F into "/" before routing, so this
		// arrives as `/RPC2/ccu1/other` and is rejected by the
		// "contains /" check. The regex is the second line of defense.
		{"encoded-slash-decoded", "/RPC2/ccu1/other", false, ""},

		// Reject — disallowed characters.
		{"space", "/RPC2/ccu 01", false, ""},
		{"dot", "/RPC2/ccu.01", false, ""},
		{"colon", "/RPC2/ccu:01", false, ""},
		{"percent", "/RPC2/ccu%01", false, ""},
		{"null-byte", "/RPC2/ccu\x00", false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := routeFromPath(tc.path)
			if ok != tc.ok {
				t.Fatalf("ok=%v want %v (path=%q got=%q)", ok, tc.ok, tc.path, got)
			}
			if got != tc.want {
				t.Fatalf("got=%q want=%q (path=%q)", got, tc.want, tc.path)
			}
		})
	}
}

// TestUnroutableCallbackPathIsLogged pins that a dropped callback leaves a
// trace. The router answers 404 for any path segment outside the allowlist,
// which is correct — but it used to do so silently, at every log level. A
// central whose stored name is not a routable segment therefore announced a
// callback URL the daemon itself rejects, and the only observable symptom was
// that no value ever changed: /health stayed green, the interface stayed
// connected, and the log said nothing.
func TestUnroutableCallbackPathIsLogged(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	srv, err := NewXMLRPCServer(XMLRPCConfig{
		Addr:   "127.0.0.1:0",
		Logger: slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})),
	})
	if err != nil {
		t.Fatalf("NewXMLRPCServer: %v", err)
	}
	t.Cleanup(func() { _ = srv.listener.Close() })
	srv.Register("ccu-01", &stubHandlers{})

	// The name a CCU adopted before the allowlist would announce, percent-
	// encoded by the client and decoded by net/http before routing.
	req := httptest.NewRequest(http.MethodPost, "/RPC2/CCU%20Wohnzimmer", strings.NewReader(""))
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d, want 404 for an unroutable segment", w.Code)
	}
	out := buf.String()
	if !strings.Contains(out, "unroutable path") || !strings.Contains(out, "CCU Wohnzimmer") {
		t.Errorf("the dropped callback left no trace naming the path:\n%s", out)
	}
}

// TestCallbackForUnregisteredCentralIsLogged covers the second drop: a
// well-formed segment that no central has registered. It is the shape a
// callback takes while a central is being torn down, and it was equally
// silent.
func TestCallbackForUnregisteredCentralIsLogged(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	srv, err := NewXMLRPCServer(XMLRPCConfig{
		Addr:   "127.0.0.1:0",
		Logger: slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})),
	})
	if err != nil {
		t.Fatalf("NewXMLRPCServer: %v", err)
	}
	t.Cleanup(func() { _ = srv.listener.Close() })
	srv.Register("ccu-01", &stubHandlers{})

	req := httptest.NewRequest(http.MethodPost, "/RPC2/ccu-02", strings.NewReader(""))
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d, want 404 for an unregistered central", w.Code)
	}
	if out := buf.String(); !strings.Contains(out, "no central registered") || !strings.Contains(out, "ccu-02") {
		t.Errorf("the dropped callback left no trace naming the central:\n%s", out)
	}
}
