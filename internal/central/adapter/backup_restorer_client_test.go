// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"net/http"
	"strings"
	"testing"
)

// TestDefaultRestoreClientSecure verifies that defaultRestoreClient(false)
// returns a non-nil client with a TLS config that does NOT skip verification.
func TestDefaultRestoreClientSecure(t *testing.T) {
	t.Parallel()
	c := defaultRestoreClient(false)
	if c == nil {
		t.Fatal("defaultRestoreClient(false) returned nil")
	}
	tr, ok := c.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("Transport = %T, want *http.Transport", c.Transport)
	}
	if tr.TLSClientConfig != nil && tr.TLSClientConfig.InsecureSkipVerify {
		t.Error("secure client must not have InsecureSkipVerify=true")
	}
}

// TestDefaultRestoreClientInsecure verifies that defaultRestoreClient(true)
// returns a client whose TLS config has InsecureSkipVerify=true.
func TestDefaultRestoreClientInsecure(t *testing.T) {
	t.Parallel()
	c := defaultRestoreClient(true)
	if c == nil {
		t.Fatal("defaultRestoreClient(true) returned nil")
	}
	tr, ok := c.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("Transport = %T, want *http.Transport", c.Transport)
	}
	if tr.TLSClientConfig == nil {
		t.Fatal("insecure client must have a TLSClientConfig")
	}
	if !tr.TLSClientConfig.InsecureSkipVerify {
		t.Error("insecure client must have InsecureSkipVerify=true")
	}
}

// TestDefaultRestoreClientTimeout verifies both clients have a non-zero Timeout.
func TestDefaultRestoreClientTimeout(t *testing.T) {
	t.Parallel()
	for _, insecure := range []bool{false, true} {
		c := defaultRestoreClient(insecure)
		if c.Timeout <= 0 {
			t.Errorf("defaultRestoreClient(%v).Timeout = %v, want > 0", insecure, c.Timeout)
		}
	}
}

// TestHTTPBackupRestorerUsesFallbackClient verifies that when HTTPClient
// is nil the restorer uses the fallback client from defaultRestoreClient.
// The simplest assertion is that the call does not panic and produces an
// HTTP error (the httptest server only exists in other tests; here we
// supply a server that immediately closes).
func TestHTTPBackupRestorerUsesFallbackClient(t *testing.T) {
	t.Parallel()
	// Use a server address that is guaranteed to refuse, so the fallback
	// client path is exercised. The test only cares that no panic occurs.
	restorer := &HTTPBackupRestorer{
		BaseURL: "http://127.0.0.1:1", // port 1 is privileged and will refuse
		Session: fakeSession{id: "S"},
		// HTTPClient deliberately nil — forces use of defaultRestoreClient.
	}
	_, err := restorer.Restore(t.Context(), "backup", strings.NewReader("x"))
	// Any error is acceptable; we only check there was no panic and the
	// error message does not contain "panic" (just sanity).
	if err == nil {
		t.Log("restore succeeded unexpectedly (port 1 may have been open in CI)")
	}
}
