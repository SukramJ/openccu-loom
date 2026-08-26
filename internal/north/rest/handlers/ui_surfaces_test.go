// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

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

// TestEmbeddedScopeDecidesPerRequest is the whole point of the scope
// setting: ONE config, two doors, two answers.
//
// It goes through the handler rather than the resolver because the
// resolver takes `insideHA` as an argument — a test that passes it
// itself proves only that the resolver honours what it is told, never
// that a request's own headers decide it. The header is the production
// input, so the request has to carry it.
func TestEmbeddedScopeDecidesPerRequest(t *testing.T) {
	t.Parallel()

	embedded := true
	cases := []struct {
		name      string
		scope     config.EmbeddedScope
		header    string
		want      string
		wantInHA  bool
		wantScope config.EmbeddedScope
	}{
		{
			name: "default scope, through Home Assistant", scope: "",
			header: "/api/hassio_ingress/tok", want: config.ProfileEmbedded,
			wantInHA: true, wantScope: config.EmbeddedScopeInsideHA,
		},
		{
			// The case the setting exists for: someone opened this daemon's
			// own URL, so the duplicate-editor argument does not apply.
			name: "default scope, direct visit", scope: "",
			header: "", want: config.ProfileStandalone,
			wantInHA: false, wantScope: config.EmbeddedScopeInsideHA,
		},
		{
			name: "always, through Home Assistant", scope: config.EmbeddedScopeAlways,
			header: "/api/hassio_ingress/tok", want: config.ProfileEmbedded,
			wantInHA: true, wantScope: config.EmbeddedScopeAlways,
		},
		{
			name: "always, direct visit", scope: config.EmbeddedScopeAlways,
			header: "", want: config.ProfileEmbedded,
			wantInHA: false, wantScope: config.EmbeddedScopeAlways,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			svc := surfaceSvc(config.NorthUI{Embedded: &embedded, EmbeddedScope: tc.scope})
			req := httptest.NewRequest(http.MethodGet, "/api/v1/ui/surfaces", http.NoBody)
			if tc.header != "" {
				req.Header.Set("X-Ingress-Path", tc.header)
			}
			rr := httptest.NewRecorder()
			GetUISurfaces(svc, nil)(rr, req)

			got := decodeSurfaces(t, rr.Body.Bytes())
			if got.Profile != tc.want {
				t.Errorf("profile = %q, want %q", got.Profile, tc.want)
			}
			if got.InsideHA != tc.wantInHA {
				t.Errorf("inside_ha = %v, want %v", got.InsideHA, tc.wantInHA)
			}
			if got.EmbeddedScope != string(tc.wantScope) {
				t.Errorf("embedded_scope = %q, want %q", got.EmbeddedScope, tc.wantScope)
			}
			// The master toggle reports the declaration, not the outcome —
			// the editor must still show "embedded is on" on a direct visit,
			// or the operator cannot find the switch they set.
			if !got.Embedded {
				t.Error("embedded reported false while the toggle is on")
			}
			// A cache that served one door's copy to the other would hand an
			// operator the wrong navigation.
			if cc := rr.Header().Get("Cache-Control"); cc != "no-store" {
				t.Errorf("Cache-Control = %q, want no-store", cc)
			}
		})
	}
}

// TestPutUISurfacesPersistsEmbeddedScope pins the write half: the editor
// can move the scope, and the answer it gets back reflects the new value
// rather than the one it sent.
func TestPutUISurfacesPersistsEmbeddedScope(t *testing.T) {
	t.Parallel()

	svc := surfaceSvc(config.NorthUI{})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/ui/surfaces",
		strings.NewReader(`{"embedded":true,"embedded_scope":"always"}`))
	rr := httptest.NewRecorder()
	PutUISurfaces(svc, nil, nil)(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body)
	}
	got := decodeSurfaces(t, rr.Body.Bytes())
	if got.EmbeddedScope != string(config.EmbeddedScopeAlways) {
		t.Errorf("embedded_scope = %q, want always", got.EmbeddedScope)
	}
	// No Ingress header on this request, so "always" is the only reason
	// the embedded profile can come back.
	if got.Profile != config.ProfileEmbedded {
		t.Errorf("profile = %q, want embedded", got.Profile)
	}
}

// TestPutUISurfacesRejectsUnknownEmbeddedScope pins that a typo fails
// loudly. Falling back to the default would keep hiding views on direct
// access — exactly what the operator was switching off.
func TestPutUISurfacesRejectsUnknownEmbeddedScope(t *testing.T) {
	t.Parallel()

	svc := surfaceSvc(config.NorthUI{})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/ui/surfaces",
		strings.NewReader(`{"embedded":true,"embedded_scope":"allways"}`))
	rr := httptest.NewRecorder()
	PutUISurfaces(svc, nil, nil)(rr, req)

	if rr.Code == http.StatusOK {
		t.Fatalf("a misspelled scope was accepted: %s", rr.Body)
	}
	if !strings.Contains(rr.Body.String(), "embedded_scope") {
		t.Errorf("the rejection does not name the offending field: %s", rr.Body)
	}
}

// TestPutUISurfacesStoresSparsely pins that a client may send its whole
// form while the daemon stores only real deviations. A stored entry that
// repeats today's default would pin it forever.
func TestPutUISurfacesStoresSparsely(t *testing.T) {
	t.Parallel()

	svc := surfaceSvc(config.NorthUI{})
	body := `{"profiles":{"standalone":{
		"nav.alarm":"hidden",
		"nav.inbox":"visible",
		"nav.unknown_view":"hidden"
	}}}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/ui/surfaces", strings.NewReader(body))
	PutUISurfaces(svc, nil, nil)(rr, req)

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
}

// TestPutUISurfacesRejectsFloorHide pins the server half of the floor.
// The disabled switch in the UI is a courtesy; this is the rule.
func TestPutUISurfacesRejectsFloorHide(t *testing.T) {
	t.Parallel()

	svc := surfaceSvc(config.NorthUI{})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/ui/surfaces",
		strings.NewReader(`{"profiles":{"standalone":{"settings.navviews":"hidden"}}}`))
	PutUISurfaces(svc, nil, nil)(rr, req)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", rr.Code, rr.Body)
	}
	if !strings.Contains(rr.Body.String(), "settings.navviews") {
		t.Errorf("the response does not name the refused surface: %s", rr.Body)
	}
	if svc.putCalled {
		t.Error("a rejected profile was persisted anyway")
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
	PutUISurfaces(svc, nil, nil)(rr, req)

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
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/ui/surfaces",
		strings.NewReader(`{"embedded":true}`))
	// Saved from inside the Home Assistant panel, which is where an
	// operator turning this on normally sits. Without the header the
	// default scope would answer "standalone" for this very request — see
	// TestEmbeddedScopeDecidesPerRequest.
	req.Header.Set("X-Ingress-Path", "/api/hassio_ingress/tok")
	PutUISurfaces(svc, nil, nil)(rr, req)

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
}

// TestPutUISurfacesRejectsUnknownProfile keeps a third profile from
// entering the store, where nothing would ever read it.
func TestPutUISurfacesRejectsUnknownProfile(t *testing.T) {
	t.Parallel()

	svc := surfaceSvc(config.NorthUI{})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/ui/surfaces",
		strings.NewReader(`{"profiles":{"kiosk":{"nav.alarm":"hidden"}}}`))
	PutUISurfaces(svc, nil, nil)(rr, req)

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
	PutUISurfaces(nil, nil, nil)(rr, req)
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
	PutUISurfaces(svc, rec, nil)(rr, req)

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
	PutUISurfaces(svc, nil, nil)(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body)
	}
}
