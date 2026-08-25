// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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
	hosts   []string
	serials []string
}

func (f *fakeConfiguredLister) List(_ context.Context) ([]sqlite.CentralRow, error) {
	n := len(f.hosts)
	if len(f.serials) > n {
		n = len(f.serials)
	}
	rows := make([]sqlite.CentralRow, 0, n)
	for i := range n {
		row := sqlite.CentralRow{}
		if i < len(f.hosts) {
			row.Host = f.hosts[i]
			row.Name = "configured-" + f.hosts[i]
		}
		if i < len(f.serials) {
			row.Serial = f.serials[i]
		}
		rows = append(rows, row)
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
			{Serial: "SER001", Name: "Otto", Host: "192.0.2.29", LastSeen: now},
			{Serial: "SER002", Name: "Keller", Host: "192.168.1.5", LastSeen: now},
			{Serial: "SER003", Name: "Ignored", Host: "10.0.0.99", LastSeen: now},
		},
	}
	ignoreStore := newFakeIgnoreStore()
	// Pre-populate SER003 as ignored.
	_ = ignoreStore.Add(context.Background(), sqlite.IgnoredCCU{Serial: "SER003"})

	// SER001's host is already configured.
	cfgLister := &fakeConfiguredLister{hosts: []string{"192.0.2.29"}}

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
			{Serial: "SER001", Name: "Otto", Host: "192.0.2.29"},
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

// TestListDiscoveredCCUs_MatchesBySerial verifies that a discovered CCU is
// marked AlreadyConfigured when a configured central shares its serial even if
// the hosts differ.
func TestListDiscoveredCCUs_MatchesBySerial(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	discoverer := &fakeDiscoveredLister{
		ccus: []ssdp.DiscoveredCCU{
			{Serial: "SER-X", Name: "Kellerbox", Host: "192.0.2.99", LastSeen: now},
		},
	}
	// Configured central has the same serial but a completely different host.
	cfgLister := &fakeConfiguredLister{
		hosts:   []string{"192.168.50.1"},
		serials: []string{"SER-X"},
	}

	deps := &DiscoveryDeps{
		Discoverer: discoverer,
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
	if len(body) != 1 {
		t.Fatalf("expected 1 entry, got %d: %+v", len(body), body)
	}
	if !body[0].AlreadyConfigured {
		t.Error("SER-X should be AlreadyConfigured=true via serial match (host differs)")
	}
}

// TestListDiscoveredCCUs_SuggestedHostFromDep verifies that SuggestedHost in
// the response is set by deps.SuggestHost and falls back to the raw host when
// that func is nil.
func TestListDiscoveredCCUs_SuggestedHostFromDep(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	discoverer := &fakeDiscoveredLister{
		ccus: []ssdp.DiscoveredCCU{
			{Serial: "S1", Name: "Local", Host: "192.0.2.29", LastSeen: now},
			{Serial: "S2", Name: "Remote", Host: "192.168.1.99", LastSeen: now},
		},
	}

	t.Run("suggest_func_applied", func(t *testing.T) {
		t.Parallel()
		deps := &DiscoveryDeps{
			Discoverer: discoverer,
			SuggestHost: func(_ context.Context, raw string) string {
				if raw == "192.0.2.29" {
					return "localhost"
				}
				return raw
			},
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
			t.Fatalf("expected 2 entries, got %d", len(body))
		}
		bySerial := map[string]discoveredCCU{}
		for _, e := range body {
			bySerial[e.Serial] = e
		}
		if bySerial["S1"].SuggestedHost != "localhost" {
			t.Errorf("S1 SuggestedHost=%q, want %q", bySerial["S1"].SuggestedHost, "localhost")
		}
		if bySerial["S2"].SuggestedHost != "192.168.1.99" {
			t.Errorf("S2 SuggestedHost=%q, want %q", bySerial["S2"].SuggestedHost, "192.168.1.99")
		}
	})

	t.Run("nil_suggest_func_falls_back_to_raw_host", func(t *testing.T) {
		t.Parallel()
		deps := &DiscoveryDeps{
			Discoverer:  discoverer,
			SuggestHost: nil,
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
		for _, e := range body {
			if e.SuggestedHost != e.Host {
				t.Errorf("serial %s: SuggestedHost=%q, want raw Host=%q", e.Serial, e.SuggestedHost, e.Host)
			}
		}
	})
}

// nilReturningIgnoreStore mimics the production shape that triggered the
// null-response defect: [sqlite.DiscoveryIgnoreStore.List] scans into a
// bare `var out []IgnoredCCU` and returns it unchanged, so a store with no
// ignored CCUs returns (nil, nil) rather than an empty slice.
type nilReturningIgnoreStore struct{ *fakeIgnoreStore }

func (*nilReturningIgnoreStore) List(context.Context) ([]sqlite.IgnoredCCU, error) {
	return nil, nil
}

// TestListIgnoredCCUs_StoreReturnsNil_ResponseIsEmptyArrayNotNull pins the
// declared array response schema: GET /centrals/discovered/ignored must
// never marshal to the JSON literal `null`.
func TestListIgnoredCCUs_StoreReturnsNil_ResponseIsEmptyArrayNotNull(t *testing.T) {
	t.Parallel()
	deps := &DiscoveryDeps{Ignore: &nilReturningIgnoreStore{fakeIgnoreStore: newFakeIgnoreStore()}}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/centrals/discovered/ignored", http.NoBody)
	w := httptest.NewRecorder()
	ListIgnoredCCUs(deps).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if got := strings.TrimSpace(w.Body.String()); got != "[]" {
		t.Errorf("body = %q, want the literal empty array [], not null", got)
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
