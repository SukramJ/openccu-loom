// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// stubConfigReader is an inline stub for ConfigReader.
type stubConfigReader struct {
	snapshot ConfigSnapshot
}

func (s *stubConfigReader) SanitizedConfig() ConfigSnapshot {
	return s.snapshot
}

func TestConfig_HappyPath(t *testing.T) {
	t.Parallel()
	reader := &stubConfigReader{
		snapshot: ConfigSnapshot{
			Locale: "en",
			Centrals: []ConfigCentral{
				{Name: "ccu-01", Host: "192.168.1.100", Interfaces: []string{"HmIP-RF"}},
			},
			CallbackPorts: ConfigPorts{XMLRPC: 8120, BINRPC: 8129},
			Features:      map[string]bool{"mqtt": true},
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/config", http.NoBody)
	w := httptest.NewRecorder()
	Config(reader).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body ConfigSnapshot
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Locale != "en" {
		t.Fatalf("expected locale=en, got %q", body.Locale)
	}
	if len(body.Centrals) != 1 || body.Centrals[0].Name != "ccu-01" {
		t.Fatalf("unexpected centrals: %+v", body.Centrals)
	}
	if body.CallbackPorts.XMLRPC != 8120 {
		t.Fatalf("expected xmlrpc port 8120, got %d", body.CallbackPorts.XMLRPC)
	}
}

func TestConfig_EmptySnapshot(t *testing.T) {
	t.Parallel()
	reader := &stubConfigReader{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/config", http.NoBody)
	w := httptest.NewRecorder()
	Config(reader).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestConfig_NilReaderReturnsServiceUnavailable(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/config", http.NoBody)
	w := httptest.NewRecorder()
	Config(nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 on nil reader, got %d", w.Code)
	}
}
