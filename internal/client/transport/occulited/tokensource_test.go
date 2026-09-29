// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package occulited

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFileTokenFollowsRotation pins the reason the token is read per
// request: occulited mints an add-on's token anew at every start, so a
// request after the file changed must carry the fresh credential with no
// reconnect or retry involved.
func TestFileTokenFollowsRotation(t *testing.T) {
	t.Parallel()

	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"api":"system","status":"ok"}`))
	}))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "addon.api")
	// occulited writes the secret with a trailing newline; the source
	// must trim it or every request would carry a broken credential.
	if err := os.WriteFile(path, []byte("olt_11111111111111111111111111111111\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	c, err := New(Config{BaseURL: srv.URL, TokenSource: FileToken(path)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()
	if _, err := c.Health(ctx); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if err := os.WriteFile(path, []byte("olt_22222222222222222222222222222222\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Health(ctx); err != nil {
		t.Fatalf("second call: %v", err)
	}

	want := []string{
		"Bearer olt_11111111111111111111111111111111",
		"Bearer olt_22222222222222222222222222222222",
	}
	if len(seen) != 2 || seen[0] != want[0] || seen[1] != want[1] {
		t.Errorf("Authorization headers = %v, want %v", seen, want)
	}
}

// TestFileTokenReadFailureFailsTheRequest pins that a vanished token
// file surfaces as a request error naming the file, never as a silent
// unauthenticated call.
func TestFileTokenReadFailureFailsTheRequest(t *testing.T) {
	t.Parallel()

	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "gone.api")
	c, err := New(Config{BaseURL: srv.URL, TokenSource: FileToken(path)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = c.Health(context.Background())
	if err == nil || !strings.Contains(err.Error(), path) {
		t.Errorf("want an error naming %s, got %v", path, err)
	}
	if called {
		t.Error("the request reached the server without a credential")
	}
}
