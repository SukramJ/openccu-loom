// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// fakePreferencesService is an in-memory UserPreferencesService for tests.
type fakePreferencesService struct {
	data map[string]string // key: "subject\x00key"
}

func newFakePreferencesSvc() *fakePreferencesService {
	return &fakePreferencesService{data: make(map[string]string)}
}

func (f *fakePreferencesService) storageKey(subject, key string) string {
	return fmt.Sprintf("%s\x00%s", subject, key)
}

func (f *fakePreferencesService) Get(_ context.Context, subject, key string) (string, error) {
	v, ok := f.data[f.storageKey(subject, key)]
	if !ok {
		return "", sqlite.ErrPreferenceNotFound
	}
	return v, nil
}

func (f *fakePreferencesService) Set(_ context.Context, subject, key, valueJSON string) error {
	f.data[f.storageKey(subject, key)] = valueJSON
	return nil
}

func (f *fakePreferencesService) Delete(_ context.Context, subject, key string) error {
	delete(f.data, f.storageKey(subject, key))
	return nil
}

// aliceIdentity is the Identity used throughout preference handler tests.
var aliceIdentity = auth.Identity{Subject: "alice", Role: auth.RoleOperator}

// --- GetPreference ---

func TestGetPreference_NoIdentity_Returns401(t *testing.T) {
	t.Parallel()
	svc := newFakePreferencesSvc()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/me/preferences/favorites", http.NoBody)
	req = withChiParam(req, "key", "favorites")
	w := httptest.NewRecorder()

	GetPreference(svc).ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestGetPreference_ExistingKey_Returns200WithValue(t *testing.T) {
	t.Parallel()
	svc := newFakePreferencesSvc()
	_ = svc.Set(context.Background(), "alice", "favorites", `["device-1"]`)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/me/preferences/favorites", http.NoBody)
	req = withIdentity(req, aliceIdentity)
	req = withChiParam(req, "key", "favorites")
	w := httptest.NewRecorder()

	GetPreference(svc).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Key   string          `json:"key"`
		Value json.RawMessage `json:"value"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Key != "favorites" {
		t.Errorf("key=%q want favorites", resp.Key)
	}
	if string(resp.Value) != `["device-1"]` {
		t.Errorf("value=%s want [\"device-1\"]", resp.Value)
	}
}

// TestGetPreference_MissingKey_ReturnsNullValue pins that an unset key
// reads as a null value rather than a 404.
//
// Every key starts unset, and the SPA asks for favorites and
// start_route on its first page load, so 404 was the normal answer for
// a fresh session — one that the request logger recorded at warn level
// each time. The key-value surface has no notion of a key that "should"
// exist, so absence is a value, not a fault.
func TestGetPreference_MissingKey_ReturnsNullValue(t *testing.T) {
	t.Parallel()
	svc := newFakePreferencesSvc()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/me/preferences/unknown", http.NoBody)
	req = withIdentity(req, aliceIdentity)
	req = withChiParam(req, "key", "unknown")
	w := httptest.NewRecorder()

	GetPreference(svc).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Key   string          `json:"key"`
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not valid JSON: %v (%s)", err, w.Body.String())
	}
	if resp.Key != "unknown" {
		t.Errorf("key = %q, want %q", resp.Key, "unknown")
	}
	if string(resp.Value) != "null" {
		t.Errorf("value = %s, want null — the SPA reads this as \"not set yet\"", resp.Value)
	}
}

// --- PutPreference ---

func TestPutPreference_ValidJSON_Returns204AndStoresValue(t *testing.T) {
	t.Parallel()
	svc := newFakePreferencesSvc()
	body := strings.NewReader(`{"pinned":true}`)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/me/preferences/settings", body)
	req = withIdentity(req, aliceIdentity)
	req = withChiParam(req, "key", "settings")
	w := httptest.NewRecorder()

	PutPreference(svc).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", w.Code, w.Body.String())
	}
	stored, err := svc.Get(context.Background(), "alice", "settings")
	if err != nil {
		t.Fatalf("Get after Put: %v", err)
	}
	if stored != `{"pinned":true}` {
		t.Errorf("stored=%q want %q", stored, `{"pinned":true}`)
	}
}

func TestPutPreference_InvalidJSON_Returns400(t *testing.T) {
	t.Parallel()
	svc := newFakePreferencesSvc()
	body := strings.NewReader(`not-json`)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/me/preferences/theme", body)
	req = withIdentity(req, aliceIdentity)
	req = withChiParam(req, "key", "theme")
	w := httptest.NewRecorder()

	PutPreference(svc).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPutPreference_NilService_Returns503(t *testing.T) {
	t.Parallel()
	body := strings.NewReader(`"dark"`)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/me/preferences/theme", body)
	req = withIdentity(req, aliceIdentity)
	req = withChiParam(req, "key", "theme")
	w := httptest.NewRecorder()

	PutPreference(nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
}

// --- DeletePreference ---

func TestDeletePreference_Returns204(t *testing.T) {
	t.Parallel()
	svc := newFakePreferencesSvc()
	_ = svc.Set(context.Background(), "alice", "layout", `{}`)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/me/preferences/layout", http.NoBody)
	req = withIdentity(req, aliceIdentity)
	req = withChiParam(req, "key", "layout")
	w := httptest.NewRecorder()

	DeletePreference(svc).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", w.Code, w.Body.String())
	}
}
