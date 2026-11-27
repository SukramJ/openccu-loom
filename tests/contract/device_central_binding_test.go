// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package contract

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/north/rest"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
)

// oneCentralLocator knows one central, "ccu-a", holding one device, "ADDR".
type oneCentralLocator struct{}

func (oneCentralLocator) HoldsDevice(central, address string) (known, holds bool) {
	if central != "ccu-a" {
		return false, false
	}
	if i := strings.LastIndexByte(address, ':'); i >= 0 {
		address = address[:i]
	}
	return true, address == "ADDR"
}

// concretePath fills a chi route template with values a handler accepts as
// well-formed: the device under test for {addr}, a channel number for {no},
// a placeholder elsewhere.
func concretePath(route, addr string) string {
	segs := strings.Split(route, "/")
	for i, s := range segs {
		switch {
		case s == "{addr}":
			segs[i] = addr
		case s == "{no}":
			segs[i] = "1"
		case strings.HasPrefix(s, "{"):
			segs[i] = "x"
		}
	}
	return strings.Join(segs, "/")
}

// TestEveryDeviceRouteHonoursCentral walks every mounted route that addresses
// a device and asserts the central binding runs in front of its handler: a
// central that does not exist, and one that does not hold the address, are
// both refused with 404 before the operation runs. A device route mounted
// without the guard would serve the request from whichever CCU holds the
// address first — the failure the binding exists to prevent.
func TestEveryDeviceRouteHonoursCentral(t *testing.T) {
	deps := fullyWiredRouterDeps()
	deps.DeviceCentrals = oneCentralLocator{}
	router := rest.NewRouter(deps)

	var routes int
	err := chi.Walk(router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if !strings.HasPrefix(route, "/api/v1/devices/{addr}") {
			return nil
		}
		routes++
		for _, tc := range []struct {
			addr, central, title string
		}{
			{"ADDR", "nope", "Unknown central"},
			{"OTHER", "ccu-a", "Device not on this central"},
		} {
			req := httptest.NewRequest(method, concretePath(route, tc.addr)+"?central="+tc.central, http.NoBody)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), tc.title) {
				t.Errorf("%s %s ?central=%s on %s: status %d body %.120s — want 404 %q from the central guard",
					method, route, tc.central, tc.addr, rec.Code, rec.Body.String(), tc.title)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("chi.Walk: %v", err)
	}
	// The walk must have found the device routes at all; an empty walk would
	// pass every assertion above.
	if routes < 60 {
		t.Fatalf("walked %d device routes, want the full set (> 60)", routes)
	}
}

// editorRoutes reads the route list the component enforces and the daemon
// publishes.
func editorRoutes(t *testing.T) []hmapi.EditorRoute {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "assets", "ui", "src", "component", "editor-routes.json"))
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Routes []hmapi.EditorRoute `json:"routes"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	return file.Routes
}

// covers reports whether an editor route covers a mounted chi route. A path
// parameter in the mounted route may stand for any value, so it matches a
// literal prefix segment too: the check over-reports coverage rather than
// missing a route a relay would forward.
func covers(r hmapi.EditorRoute, method, route string) bool {
	if r.Method != method {
		return false
	}
	want := strings.Split(r.Prefix, "/")
	have := strings.Split(route, "/")
	if len(have) < len(want) {
		return false
	}
	for i, seg := range want {
		if seg == "*" || seg == have[i] || strings.HasPrefix(have[i], "{") {
			continue
		}
		return false
	}
	return true
}

// editorCoveredRoutes is every mounted route a relay forwards for the editor.
// The list is the review surface of the editor's reach: a route that joins it
// is one more call a Home Assistant user can make through the integration's
// credential, so it changes here, deliberately, together with
// editor-routes.json. Lifecycle calls (unpair, delete, replace, firmware,
// restore, reload) must never appear.
var editorCoveredRoutes = []string{
	"DELETE /devices/{addr}/central-links",
	"DELETE /devices/{addr}/links",
	"DELETE /sessions/edit",
	"GET /auth/me",
	"GET /devices",
	"GET /devices/{addr}",
	"GET /devices/{addr}/audit",
	"GET /devices/{addr}/cdps",
	"GET /devices/{addr}/cdps/{name}",
	"GET /devices/{addr}/central-links",
	"GET /devices/{addr}/channels",
	"GET /devices/{addr}/channels/{no}",
	"GET /devices/{addr}/channels/{no}/calc-dps",
	"GET /devices/{addr}/channels/{no}/calc-dps/{name}",
	"GET /devices/{addr}/channels/{no}/config/export",
	"GET /devices/{addr}/channels/{no}/data-points",
	"GET /devices/{addr}/channels/{no}/data-points/{param}",
	"GET /devices/{addr}/channels/{no}/event-groups",
	"GET /devices/{addr}/channels/{no}/flags",
	"GET /devices/{addr}/channels/{no}/linkable-channels",
	"GET /devices/{addr}/channels/{no}/schedule",
	"GET /devices/{addr}/channels/{no}/team-candidates",
	"GET /devices/{addr}/channels/{no}/ui-schema",
	"GET /devices/{addr}/channels/{no}/week_profile",
	"GET /devices/{addr}/export-definition",
	"GET /devices/{addr}/icon",
	"GET /devices/{addr}/link-ps/{peer}",
	"GET /devices/{addr}/links",
	"GET /devices/{addr}/paramsets/{key}",
	"GET /devices/{addr}/paramsets/{key}/apply-targets",
	"GET /devices/{addr}/projection",
	"GET /devices/{addr}/replace-candidates",
	"GET /devices/{addr}/schedule",
	"GET /functions",
	"GET /info",
	"GET /measurements/recording",
	"GET /rooms",
	"GET /schedules",
	"GET /system/ccu",
	"GET /taxonomy",
	"PATCH /devices/{addr}",
	"PATCH /devices/{addr}/channels/{no}",
	"PATCH /devices/{addr}/links",
	"POST /devices/{addr}/central-links",
	"POST /devices/{addr}/channels/{no}/paramsets/{key}/determine",
	"POST /devices/{addr}/channels/{no}/schedule/active-profile",
	"POST /devices/{addr}/links/test",
	"POST /devices/{addr}/paramsets/{key}/apply-to",
	"POST /devices/{addr}/schedule/active-profile",
	"POST /functions",
	"POST /rooms",
	"POST /sessions/edit",
	"POST /sessions/edit/heartbeat",
	"POST /sessions/edit/take-over",
	"PUT /devices/{addr}/channels/{no}/data-points/{param}/value",
	"PUT /devices/{addr}/channels/{no}/flags",
	"PUT /devices/{addr}/channels/{no}/schedule",
	"PUT /devices/{addr}/channels/{no}/team",
	"PUT /devices/{addr}/channels/{no}/week_profile/channel-locks/{key}",
	"PUT /devices/{addr}/link-ps/{peer}",
	"PUT /devices/{addr}/paramsets/{key}",
	"PUT /devices/{addr}/schedule",
	"PUT /measurements/recording",
}

// TestEditorRoutesExcludeLifecycle pins what the editor's route list lets a
// relay forward, measured against the mounted router rather than against the
// list's own spelling: a prefix that grows by one segment can cover a route
// nobody meant to hand to the integration.
func TestEditorRoutesExcludeLifecycle(t *testing.T) {
	list := editorRoutes(t)
	router := rest.NewRouter(fullyWiredRouterDeps())
	var got []string
	used := make([]bool, len(list))
	err := chi.Walk(router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		path, ok := strings.CutPrefix(route, "/api/v1")
		if !ok {
			return nil
		}
		path = strings.TrimSuffix(path, "/")
		hit := false
		for i, r := range list {
			if covers(r, method, path) {
				used[i] = true
				hit = true
			}
		}
		if hit {
			got = append(got, method+" "+path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("chi.Walk: %v", err)
	}
	slices.Sort(got)
	got = slices.Compact(got)
	if !slices.Equal(got, editorCoveredRoutes) {
		t.Errorf("the editor's route list covers a different set of mounted routes.\ngot:\n  %s\nwant:\n  %s",
			strings.Join(got, "\n  "), strings.Join(editorCoveredRoutes, "\n  "))
	}
	for i, r := range list {
		if !used[i] {
			t.Errorf("editor route %s %s covers no mounted route", r.Method, r.Prefix)
		}
	}
}
