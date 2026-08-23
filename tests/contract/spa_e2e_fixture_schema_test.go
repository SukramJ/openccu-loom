// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

// spa_e2e_fixture_schema_test.go — the SPA's Playwright fixtures answer to
// assets/openapi.yaml.
//
// assets/ui/tests/e2e/helpers/mock-api.ts is a router: it maps a request URL
// to one of 59 hand-written JSON files under assets/ui/tests/e2e/fixtures/
// and serves it as the mocked REST response. Nothing previously checked
// those files against the contract the daemon actually serves, so the whole
// browser-level suite could stay green while asserting a shape the daemon
// no longer returns — a spec field renamed or dropped in openapi.yaml (and
// in the handler) would not turn a single e2e test red.
//
// fixtureRoutes below is that same route table, hand-extracted from
// mock-api.ts (the mapping lives in TypeScript, not in a machine-readable
// form this test can parse, so it is transcribed and kept in sync by hand —
// TestSPAE2EFixturesAreAllRouted below catches a fixture file the table
// forgets). For each entry this test loads the named fixture, resolves the
// OpenAPI 200 response schema for that path + method, and validates the
// fixture's JSON against it with kin-openapi's own validator — the same
// library the daemon's request/response middleware uses in production
// (internal/north/rest/middleware/openapi.go).
import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

// fixtureRoute names one URL a fixture answers, extracted from
// assets/ui/tests/e2e/helpers/mock-api.ts. A fixture served at more than one
// route (e.g. users.json for both /auth/users and /users, which carry
// different list-entry schemas) gets one row per route.
type fixtureRoute struct {
	fixture string
	path    string
	method  string
}

// fixtureRoutes is the route → fixture mapping mock-api.ts wires for
// mockAllApis(), the mock every e2e spec runs under. Variant fixtures used
// only by a route-override helper (mockAlarmTriggered, mockEnergy, …) are
// included too, keyed to the same production path.
var fixtureRoutes = []fixtureRoute{
	{"auth-me.json", "/auth/me", "GET"},
	{"restart-pending.json", "/system/restart-pending", "GET"},
	{"startup-capture.json", "/system/startup-capture", "GET"},
	{"config-changes.json", "/system/config-changes", "GET"},
	{"system-update.json", "/system/update", "GET"},
	{"install-mode.json", "/install-mode/interfaces", "GET"},
	{"info.json", "/info", "GET"},
	{"ui-surfaces.json", "/ui/surfaces", "GET"},
	{"devices.json", "/devices", "GET"},
	{"alarm-wizard-devices.json", "/devices", "GET"},
	{"sysvars.json", "/sysvars", "GET"},
	{"groups.json", "/groups", "GET"},
	{"links.json", "/links", "GET"},
	{"schedules.json", "/schedules", "GET"},
	{"programs.json", "/programs", "GET"},
	{"health.json", "/health", "GET"},
	{"interfaces.json", "/interfaces", "GET"},
	{"incidents.json", "/incidents", "GET"},
	{"log-levels.json", "/diagnostics/log-levels", "GET"},
	{"captures.json", "/diagnostics/capture", "GET"},
	{"rpc-recordings.json", "/diagnostics/rpc-recording", "GET"},
	{"diagnostics.json", "/diagnostics", "GET"},
	{"config-schema.json", "/config/schema", "GET"},
	{"config-effective.json", "/config/effective", "GET"},
	{"rooms.json", "/rooms", "GET"},
	{"functions.json", "/functions", "GET"},
	{"alarm-state.json", "/alarm/state", "GET"},
	{"alarm-state-triggered.json", "/alarm/state", "GET"},
	{"alarm-sensors.json", "/alarm/zones/{id}/sensors", "GET"},
	{"alarm-outputs.json", "/alarm/zones/{id}/outputs", "GET"},
	{"alarm-output-candidates.json", "/alarm/output-candidates", "GET"},
	{"alarm-remote-key-candidates.json", "/alarm/remote-key-candidates", "GET"},
	{"alarm-readiness.json", "/alarm/zones/{id}/readiness", "GET"},
	{"alarm-triggered-motion.json", "/alarm/triggered-motion", "GET"},
	{"alarm-walktest.json", "/alarm/zones/{id}/walktest", "GET"},
	{"alarm-zones.json", "/alarm/zones", "GET"},
	{"alarm-journal.json", "/alarm/journal", "GET"},
	{"alarm-codes.json", "/alarm/codes", "GET"},
	{"security-faults.json", "/security/faults", "GET"},
	{"security-sources.json", "/security/sources", "GET"},
	{"security-snapshot.json", "/security", "GET"},
	{"users.json", "/auth/users", "GET"},
	{"users.json", "/users", "GET"},
	{"tokens.json", "/auth/tokens", "GET"},
	{"tokens.json", "/auth/tokens/v2", "GET"},
	{"centrals.json", "/centrals", "GET"},
	{"inbox.json", "/inbox", "GET"},
	{"audit.json", "/audit", "GET"},
	{"backups.json", "/backups", "GET"},
	{"matter-sessions.json", "/matter/sessions", "GET"},
	{"matter-mdns.json", "/matter/mdns", "GET"},
	{"matter-endpoints.json", "/matter/endpoints", "GET"},
	{"matter-compatibility.json", "/matter/compatibility", "GET"},
	{"matter-events.json", "/matter/events", "GET"},
	{"matter-fabrics.json", "/matter/fabrics", "GET"},
	{"matter-status.json", "/matter/status", "GET"},
	{"visibility-candidates.json", "/visibility/unignore/candidates", "GET"},
	{"visibility-unignore.json", "/visibility/unignore", "GET"},
	{"energy.json", "/energy", "GET"},
}

// driftedFixtures ratchets fixture/route pairs that fail schema validation
// at HEAD. Each reason names the exact missing/extra field this test found —
// a real finding this guard surfaced, not fixed, because reshaping the
// fixture without checking what the daemon actually serves would just trade
// one unverified shape for another. Removing a fixture's drift here (by
// fixing the fixture against a confirmed daemon response, or fixing the
// spec) must make TestSPAE2EFixturesMatchOpenAPISchema fail until the entry
// is deleted too — see the loop below.
var driftedFixtures = map[string]string{
	"info.json|GET|/info":                     `missing required "config_ui_url"`,
	"programs.json|GET|/programs":             `item missing required "unique_id"`,
	"users.json|GET|/auth/users":              `item missing required "username"`,
	"users.json|GET|/users":                   `item missing required "created_at"`,
	"centrals.json|GET|/centrals":             `item missing required "interfaces"`,
	"matter-fabrics.json|GET|/matter/fabrics": `item missing required "fabric_id_hex"`,
	"matter-status.json|GET|/matter/status":   `missing required "listening"`,
}

// TestSPAE2EFixturesMatchOpenAPISchema validates every fixture in
// fixtureRoutes against the OpenAPI 200 response schema for the route it
// answers. A fixture that no longer matches the contract means the e2e
// suite has been passing against a shape the daemon does not serve.
func TestSPAE2EFixturesMatchOpenAPISchema(t *testing.T) {
	root := repoRoot(t)
	specPath := filepath.Join(root, "assets", "openapi.yaml")
	fixturesDir := filepath.Join(root, "assets", "ui", "tests", "e2e", "fixtures")

	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = false
	doc, err := loader.LoadFromFile(specPath)
	if err != nil {
		t.Fatalf("load openapi.yaml: %v", err)
	}
	if err := doc.Validate(context.Background()); err != nil {
		t.Fatalf("validate openapi.yaml: %v", err)
	}

	if len(fixtureRoutes) == 0 {
		t.Fatal("fixtureRoutes is empty — the route table may have been dropped")
	}

	checked := 0
	for _, r := range fixtureRoutes {
		item := doc.Paths.Find(r.path)
		if item == nil {
			t.Errorf("%s → %s %s: path not in openapi.yaml", r.fixture, r.method, r.path)
			continue
		}
		op := item.GetOperation(r.method)
		if op == nil {
			t.Errorf("%s → %s %s: operation not in openapi.yaml", r.fixture, r.method, r.path)
			continue
		}
		resp := op.Responses.Value("200")
		if resp == nil || resp.Value == nil {
			t.Errorf("%s → %s %s: no 200 response in openapi.yaml", r.fixture, r.method, r.path)
			continue
		}
		mt := resp.Value.Content.Get("application/json")
		if mt == nil || mt.Schema == nil || mt.Schema.Value == nil {
			t.Errorf("%s → %s %s: 200 response has no application/json schema", r.fixture, r.method, r.path)
			continue
		}

		raw, err := os.ReadFile(filepath.Join(fixturesDir, r.fixture))
		if err != nil {
			t.Errorf("%s: read fixture: %v", r.fixture, err)
			continue
		}
		var instance any
		if err := json.Unmarshal(raw, &instance); err != nil {
			t.Errorf("%s: parse fixture JSON: %v", r.fixture, err)
			continue
		}

		validateErr := mt.Schema.Value.VisitJSON(instance)
		key := r.fixture + "|" + r.method + "|" + r.path
		reason, ratcheted := driftedFixtures[key]
		switch {
		case validateErr != nil && ratcheted:
			// Known drift — reported to the caller, not silently fixed.
			t.Logf("%s: known drift against %s %s (%s) — %v", r.fixture, r.method, r.path, reason, validateErr)
		case validateErr != nil:
			t.Errorf("%s does not match the %s %s 200 schema in openapi.yaml: %v", r.fixture, r.method, r.path, validateErr)
		case ratcheted:
			// The ratchet entry no longer reproduces — either the fixture or
			// the spec changed underneath it. Force the entry's removal so
			// the map cannot outlive the drift it names.
			t.Errorf("%s: driftedFixtures[%q] no longer fails validation — remove the ratchet entry", r.fixture, key)
		default:
			checked++
		}
	}

	if !t.Failed() {
		t.Logf("TestSPAE2EFixturesMatchOpenAPISchema: %d fixture routes validated against openapi.yaml", checked)
	}
}

// fixturesWithoutARoute exempts a fixture file from
// TestSPAE2EFixturesAreAllRouted — every entry needs a reason that is true
// today, checked against the current tree, not an aspiration.
var fixturesWithoutARoute = map[string]string{
	// Not referenced by any `fixture(...)` call in mock-api.ts or any
	// *.spec.ts file (checked by grep across assets/ui/tests/e2e/) — dead
	// files left over from a prior test shape, not wired to a route at all.
	"devices-empty.json": "unreferenced by mock-api.ts or any spec; mockEmptyDevices() inlines its own JSON instead",
	"sysvars-empty.json": "unreferenced by mock-api.ts or any spec; mockEmptySysvars() inlines its own JSON instead",
}

// TestSPAE2EFixturesAreAllRouted asserts that every JSON file under
// assets/ui/tests/e2e/fixtures/ appears in fixtureRoutes (or is named in
// fixturesWithoutARoute with a true reason). Without this, a fixture added
// to mock-api.ts without a matching row here would silently sit outside
// TestSPAE2EFixturesMatchOpenAPISchema's coverage — an unrouted fixture is
// exactly the "answers to no schema" defect this guard exists to catch.
func TestSPAE2EFixturesAreAllRouted(t *testing.T) {
	root := repoRoot(t)
	fixturesDir := filepath.Join(root, "assets", "ui", "tests", "e2e", "fixtures")

	entries, err := os.ReadDir(fixturesDir)
	if err != nil {
		t.Fatalf("read fixtures dir: %v", err)
	}

	routed := make(map[string]bool, len(fixtureRoutes))
	for _, r := range fixtureRoutes {
		routed[r.fixture] = true
	}

	var unrouted []string
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		if routed[e.Name()] {
			continue
		}
		if _, exempt := fixturesWithoutARoute[e.Name()]; exempt {
			continue
		}
		unrouted = append(unrouted, e.Name())
	}
	sort.Strings(unrouted)

	if len(unrouted) > 0 {
		t.Errorf("fixture files not in fixtureRoutes and not exempted in fixturesWithoutARoute "+
			"(add a route row so TestSPAE2EFixturesMatchOpenAPISchema covers them, or an exemption "+
			"with a true reason): %v", unrouted)
	}
}
