// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/discovery/ssdp"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// --- fakes ---

type fakeDiscoveredLister struct {
	ccus []ssdp.DiscoveredCCU
}

func (f *fakeDiscoveredLister) List() []ssdp.DiscoveredCCU { return f.ccus }

type fakeIgnoreStore struct {
	entries map[string]sqlite.IgnoredCCU
	addErr  error
}

func newFakeIgnoreStore() *fakeIgnoreStore {
	return &fakeIgnoreStore{entries: map[string]sqlite.IgnoredCCU{}}
}

func (f *fakeIgnoreStore) Add(_ context.Context, e sqlite.IgnoredCCU) error {
	if f.addErr != nil {
		return f.addErr
	}
	f.entries[e.Serial] = e
	return nil
}

func (f *fakeIgnoreStore) Remove(_ context.Context, serial string) (bool, error) {
	if _, ok := f.entries[serial]; ok {
		delete(f.entries, serial)
		return true, nil
	}
	return false, nil
}

func (f *fakeIgnoreStore) List(_ context.Context) ([]sqlite.IgnoredCCU, error) {
	out := make([]sqlite.IgnoredCCU, 0, len(f.entries))
	for _, e := range f.entries {
		out = append(out, e)
	}
	return out, nil
}

func (f *fakeIgnoreStore) IgnoredSerials(_ context.Context) (map[string]struct{}, error) {
	set := make(map[string]struct{}, len(f.entries))
	for k := range f.entries {
		set[k] = struct{}{}
	}
	return set, nil
}

type fakeConfiguredLister struct {
	hosts []string
}

func (f *fakeConfiguredLister) List(_ context.Context) ([]sqlite.CentralRow, error) {
	rows := make([]sqlite.CentralRow, 0, len(f.hosts))
	for _, h := range f.hosts {
		rows = append(rows, sqlite.CentralRow{Host: h, Name: "configured-" + h})
	}
	return rows, nil
}

// --- tests ---

// TestListDiscoveredCCUs_FiltersIgnoredAndMarksConfigured checks that:
//   - ignored CCUs are excluded from the response,
//   - CCUs whose host matches a configured central are flagged AlreadyConfigured.
func TestListDiscoveredCCUs_FiltersIgnoredAndMarksConfigured(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	discoverer := &fakeDiscoveredLister{
		ccus: []ssdp.DiscoveredCCU{
			{Serial: "SER001", Name: "Otto", Host: "172.18.4.29", LastSeen: now},
			{Serial: "SER002", Name: "Keller", Host: "192.168.1.5", LastSeen: now},
			{Serial: "SER003", Name: "Ignored", Host: "10.0.0.99", LastSeen: now},
		},
	}
	ignoreStore := newFakeIgnoreStore()
	// Pre-populate SER003 as ignored.
	_ = ignoreStore.Add(context.Background(), sqlite.IgnoredCCU{Serial: "SER003"})

	// SER001's host is already configured.
	cfgLister := &fakeConfiguredLister{hosts: []string{"172.18.4.29"}}

	deps := &DiscoveryDeps{
		Discoverer: discoverer,
		Ignore:     ignoreStore,
		Centrals:   cfgLister,
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/centrals/discovered", http.NoBody)
	w := httptest.NewRecorder()
	ListDiscoveredCCUs(deps).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body []discoveredCCU
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) != 2 {
		t.Fatalf("expected 2 entries (ignored one filtered), got %d: %+v", len(body), body)
	}
	// Find SER001 and verify it is marked as already configured.
	var found *discoveredCCU
	for i := range body {
		if body[i].Serial == "SER001" {
			found = &body[i]
		}
	}
	if found == nil {
		t.Fatal("SER001 not found in response")
	}
	if !found.AlreadyConfigured {
		t.Error("SER001 should be AlreadyConfigured=true")
	}
	// SER003 must not appear.
	for _, e := range body {
		if e.Serial == "SER003" {
			t.Error("ignored SER003 must not appear in response")
		}
	}
}

// TestListDiscoveredCCUs_NilDiscoverer_ReturnsEmpty verifies that a nil
// Discoverer causes the handler to return an empty JSON array.
func TestListDiscoveredCCUs_NilDiscoverer_ReturnsEmpty(t *testing.T) {
	t.Parallel()

	deps := &DiscoveryDeps{Discoverer: nil}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/centrals/discovered", http.NoBody)
	w := httptest.NewRecorder()
	ListDiscoveredCCUs(deps).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body []discoveredCCU
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) != 0 {
		t.Errorf("expected empty array, got %d entries", len(body))
	}
}

// TestIgnoreDiscoveredCCU_HappyPath verifies that POST /…/{serial}/ignore
// returns 204 and persists the entry in the ignore store.
func TestIgnoreDiscoveredCCU_HappyPath(t *testing.T) {
	t.Parallel()

	discoverer := &fakeDiscoveredLister{
		ccus: []ssdp.DiscoveredCCU{
			{Serial: "SER001", Name: "Otto", Host: "172.18.4.29"},
		},
	}
	ignoreStore := newFakeIgnoreStore()
	deps := &DiscoveryDeps{Discoverer: discoverer, Ignore: ignoreStore}

	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/centrals/discovered/SER001/ignore", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"serial": "SER001"}))
	w := httptest.NewRecorder()
	IgnoreDiscoveredCCU(deps).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", w.Code, w.Body.String())
	}
	if _, ok := ignoreStore.entries["SER001"]; !ok {
		t.Error("SER001 not found in ignore store after handler call")
	}
}

// TestIgnoreDiscoveredCCU_MissingSerial verifies that an empty serial path
// param causes a 400 response.
func TestIgnoreDiscoveredCCU_MissingSerial(t *testing.T) {
	t.Parallel()

	deps := &DiscoveryDeps{Ignore: newFakeIgnoreStore()}
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/centrals/discovered//ignore", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"serial": ""}))
	w := httptest.NewRecorder()
	IgnoreDiscoveredCCU(deps).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestUnignoreDiscoveredCCU_HappyPath verifies that DELETE /…/{serial}/ignore
// returns 204 when the entry exists.
func TestUnignoreDiscoveredCCU_HappyPath(t *testing.T) {
	t.Parallel()

	ignoreStore := newFakeIgnoreStore()
	_ = ignoreStore.Add(context.Background(), sqlite.IgnoredCCU{Serial: "SER001", Name: "Otto"})
	deps := &DiscoveryDeps{Ignore: ignoreStore}

	req := httptest.NewRequest(http.MethodDelete,
		"/api/v1/centrals/discovered/SER001/ignore", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"serial": "SER001"}))
	w := httptest.NewRecorder()
	UnignoreDiscoveredCCU(deps).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", w.Code, w.Body.String())
	}
	if _, ok := ignoreStore.entries["SER001"]; ok {
		t.Error("SER001 still in ignore store after un-ignore")
	}
}

// TestUnignoreDiscoveredCCU_NotFound verifies that un-ignoring an unknown
// serial returns 404.
func TestUnignoreDiscoveredCCU_NotFound(t *testing.T) {
	t.Parallel()

	deps := &DiscoveryDeps{Ignore: newFakeIgnoreStore()}
	req := httptest.NewRequest(http.MethodDelete,
		"/api/v1/centrals/discovered/UNKNOWN/ignore", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"serial": "UNKNOWN"}))
	w := httptest.NewRecorder()
	UnignoreDiscoveredCCU(deps).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestListIgnoredCCUs_ReturnsAll verifies that ListIgnoredCCUs returns every
// entry in the ignore store.
func TestListIgnoredCCUs_ReturnsAll(t *testing.T) {
	t.Parallel()

	ignoreStore := newFakeIgnoreStore()
	_ = ignoreStore.Add(context.Background(), sqlite.IgnoredCCU{Serial: "A", Name: "Alpha"})
	_ = ignoreStore.Add(context.Background(), sqlite.IgnoredCCU{Serial: "B", Name: "Beta"})
	deps := &DiscoveryDeps{Ignore: ignoreStore}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/centrals/discovered/ignored", http.NoBody)
	w := httptest.NewRecorder()
	ListIgnoredCCUs(deps).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body []sqlite.IgnoredCCU
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) != 2 {
		t.Errorf("expected 2 entries, got %d", len(body))
	}
}
