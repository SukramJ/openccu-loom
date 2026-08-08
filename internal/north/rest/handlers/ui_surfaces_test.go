// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/configstore"
	"github.com/SukramJ/openccu-loom/internal/north/ui/surface"
)

// surfaceSvc builds a config-admin fake whose effective config carries
// the given UI section.
func surfaceSvc(ui config.NorthUI) *fakeConfigAdminSvc {
	cfg := config.Default()
	cfg.North.UI = ui
	return &fakeConfigAdminSvc{
		effectiveResult: &configstore.EffectiveResult{Config: cfg},
	}
}

// recordingPolicy captures the config a save pushed into the live policy.
type recordingPolicy struct {
	got    config.NorthUI
	called bool
}

func (p *recordingPolicy) Set(ui config.NorthUI) {
	p.got = ui
	p.called = true
}

func decodeSurfaces(t *testing.T, body []byte) SurfacesResponse {
	t.Helper()
	var out SurfacesResponse
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode response: %v (%s)", err, body)
	}
	return out
}

// TestGetUISurfacesServesRegistryAndResolution pins the read side: the
// editor needs the registry metadata and the navigation needs the
// resolved map, and both come from one payload so they cannot diverge.
func TestGetUISurfacesServesRegistryAndResolution(t *testing.T) {
	t.Parallel()

	svc := surfaceSvc(config.NorthUI{})
	rr := httptest.NewRecorder()
	GetUISurfaces(svc, nil)(rr, httptest.NewRequest(http.MethodGet, "/api/v1/ui/surfaces", http.NoBody))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body)
	}
	got := decodeSurfaces(t, rr.Body.Bytes())
	if got.Embedded {
		t.Error("embedded reported true for a default config")
	}
	if got.Profile != config.ProfileStandalone {
		t.Errorf("profile = %q, want standalone", got.Profile)
	}
	if len(got.Surfaces) != len(surface.Registry()) {
		t.Errorf("surfaces = %d, want the whole registry (%d)", len(got.Surfaces), len(surface.Registry()))
	}
	if len(got.Effective) != len(surface.Registry()) {
		t.Errorf("effective = %d entries, want one per surface", len(got.Effective))
	}
	if !got.Effective["nav.devices"] {
		t.Error("nav.devices resolved hidden in the standalone default")
	}
	// The editor cannot explain a locked row without this metadata.
	var devices *SurfaceInfo
	for i := range got.Surfaces {
		if got.Surfaces[i].ID == "nav.devices" {
			devices = &got.Surfaces[i]
		}
	}
	if devices == nil {
		t.Fatal("nav.devices missing from the payload")
	}
	if devices.Floor != string(surface.FloorAlways) {
		t.Errorf("nav.devices floor = %q, want always", devices.Floor)
	}
}

// TestPutUISurfacesStoresSparsely pins that a client may send its whole
// form while the daemon stores only real deviations. A stored entry that
// repeats today's default would pin it forever.
func TestPutUISurfacesStoresSparsely(t *testing.T) {
	t.Parallel()

	svc := surfaceSvc(config.NorthUI{})
	policy := &recordingPolicy{}
	body := `{"profiles":{"standalone":{
		"nav.alarm":"hidden",
		"nav.inbox":"visible",
		"nav.unknown_view":"hidden"
	}}}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/ui/surfaces", strings.NewReader(body))
	PutUISurfaces(svc, nil, policy, nil)(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body)
	}
	if !svc.putCalled {
		t.Fatal("the section was never persisted")
	}
	got := decodeSurfaces(t, rr.Body.Bytes())
	stored := got.Profiles[config.ProfileStandalone]
	if len(stored) != 1 || stored["nav.alarm"] != string(config.SurfaceHidden) {
		t.Errorf("stored profile = %v, want only nav.alarm:hidden", stored)
	}
	if !policy.called {
		t.Error("the live policy was not updated — the save would only take effect after a restart")
	}
}

// TestPutUISurfacesRejectsFloorHide pins the server half of the floor.
// The disabled switch in the UI is a courtesy; this is the rule.
func TestPutUISurfacesRejectsFloorHide(t *testing.T) {
	t.Parallel()

	svc := surfaceSvc(config.NorthUI{})
	policy := &recordingPolicy{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/ui/surfaces",
		strings.NewReader(`{"profiles":{"standalone":{"settings.navviews":"hidden"}}}`))
	PutUISurfaces(svc, nil, policy, nil)(rr, req)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", rr.Code, rr.Body)
	}
	if !strings.Contains(rr.Body.String(), "settings.navviews") {
		t.Errorf("the response does not name the refused surface: %s", rr.Body)
	}
	if svc.putCalled {
		t.Error("a rejected profile was persisted anyway")
	}
	if policy.called {
		t.Error("a rejected profile reached the live policy")
	}
}

// TestPutUISurfacesRejectsUnknownState keeps a typo out of the store,
// where it would read as "not configured" and silently restore a default.
func TestPutUISurfacesRejectsUnknownState(t *testing.T) {
	t.Parallel()

	svc := surfaceSvc(config.NorthUI{})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/ui/surfaces",
		strings.NewReader(`{"profiles":{"standalone":{"nav.alarm":"off"}}}`))
	PutUISurfaces(svc, nil, &recordingPolicy{}, nil)(rr, req)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", rr.Code, rr.Body)
	}
	if svc.putCalled {
		t.Error("an invalid state was persisted")
	}
}

// TestPutUISurfacesTogglesModeWithoutTouchingProfiles pins that the
// master toggle is independent of the row editor: flipping the mode must
// not clear overrides the operator prepared for the other profile.
func TestPutUISurfacesTogglesModeWithoutTouchingProfiles(t *testing.T) {
	t.Parallel()

	svc := surfaceSvc(config.NorthUI{
		Profiles: map[string]map[string]config.SurfaceState{
			config.ProfileEmbedded: {"nav.matter": config.SurfaceVisible},
		},
	})
	policy := &recordingPolicy{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/ui/surfaces",
		strings.NewReader(`{"embedded":true}`))
	PutUISurfaces(svc, nil, policy, nil)(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body)
	}
	got := decodeSurfaces(t, rr.Body.Bytes())
	if !got.Embedded || got.Profile != config.ProfileEmbedded {
		t.Fatalf("mode did not switch: embedded=%v profile=%q", got.Embedded, got.Profile)
	}
	if got.Profiles[config.ProfileEmbedded]["nav.matter"] != string(config.SurfaceVisible) {
		t.Errorf("the prepared embedded override was lost: %v", got.Profiles)
	}
	if !policy.got.IsEmbedded() {
		t.Error("the live policy did not learn about the new mode")
	}
}

// TestPutUISurfacesRejectsUnknownProfile keeps a third profile from
// entering the store, where nothing would ever read it.
func TestPutUISurfacesRejectsUnknownProfile(t *testing.T) {
	t.Parallel()

	svc := surfaceSvc(config.NorthUI{})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/ui/surfaces",
		strings.NewReader(`{"profiles":{"kiosk":{"nav.alarm":"hidden"}}}`))
	PutUISurfaces(svc, nil, &recordingPolicy{}, nil)(rr, req)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", rr.Code, rr.Body)
	}
}

// TestUISurfacesUnavailableWithoutService keeps the SPA on a clear
// status instead of a panic when the config service is not wired.
func TestUISurfacesUnavailableWithoutService(t *testing.T) {
	t.Parallel()

	rr := httptest.NewRecorder()
	GetUISurfaces(nil, nil)(rr, httptest.NewRequest(http.MethodGet, "/api/v1/ui/surfaces", http.NoBody))
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("GET status = %d, want 503", rr.Code)
	}

	rr = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/ui/surfaces", strings.NewReader(`{}`))
	PutUISurfaces(nil, nil, nil, nil)(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("PUT status = %d, want 503", rr.Code)
	}
}

// TestPutUISurfacesIsAudited pins the audit row.
//
// It is not bookkeeping here: in the embedded profile a surface entry
// decides whether Home Assistant may write to that surface, so the
// profile change IS an authorization change. "Who handed the paramset
// editor back, and when" has to stay answerable, and an audit row nobody
// asserts is one refactor away from silently disappearing.
func TestPutUISurfacesIsAudited(t *testing.T) {
	t.Parallel()

	svc := surfaceSvc(config.NorthUI{})
	rec := &captureRecorder{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/ui/surfaces",
		strings.NewReader(`{"embedded":true}`))
	PutUISurfaces(svc, rec, &recordingPolicy{}, nil)(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body)
	}
	if len(rec.entries) != 1 {
		t.Fatalf("recorded %d audit entries, want exactly 1", len(rec.entries))
	}
	got := rec.entries[0]
	if got.Action != audit.ActionConfigSectionUpdate {
		t.Errorf("action = %q, want %q", got.Action, audit.ActionConfigSectionUpdate)
	}
	// The note has to name the profile that is now live — an entry that
	// only says "north.ui changed" cannot answer the question above.
	if !strings.Contains(got.Note, config.ProfileEmbedded) {
		t.Errorf("note = %q, want it to name the resulting profile", got.Note)
	}
	if !strings.Contains(got.Note, string(configstore.SectionUI)) {
		t.Errorf("note = %q, want it to name the section", got.Note)
	}
}

// TestPutUISurfacesSurvivesNilRecorder keeps a daemon without audit
// persistence saving profiles rather than panicking.
func TestPutUISurfacesSurvivesNilRecorder(t *testing.T) {
	t.Parallel()

	svc := surfaceSvc(config.NorthUI{})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/ui/surfaces",
		strings.NewReader(`{"embedded":true}`))
	PutUISurfaces(svc, nil, nil, nil)(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body)
	}
}
