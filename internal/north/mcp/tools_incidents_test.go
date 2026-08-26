// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mcp_test

import (
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/mcp"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
)

// fakeIncidentsReader returns a fixed set of incidents under test control.
type fakeIncidentsReader struct {
	incidents []hmapi.Incident
}

func (f *fakeIncidentsReader) Incidents() []hmapi.Incident {
	return f.incidents
}

// fixedIncidents returns a deterministic set of 4 incidents spanning two
// centrals ("alpha" and "beta"), with one using an interface-suffixed
// Component ("alpha/HmIP-RF"), ordered deliberately oldest-first so we can
// verify that registerListIncidents sorts newest-first independently.
func fixedIncidents() []hmapi.Incident {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return []hmapi.Incident{
		{
			ID:        "inc-alpha-1",
			When:      base.Add(1 * time.Hour),
			Component: "alpha",
			Severity:  "warning",
			Summary:   "Alpha central warning",
			Detail:    "detail-alpha-1",
		},
		{
			ID:        "inc-alpha-if",
			When:      base.Add(3 * time.Hour),
			Component: "alpha/HmIP-RF",
			Severity:  "error",
			Summary:   "Alpha interface error",
			Detail:    "detail-alpha-if",
		},
		{
			ID:        "inc-beta-1",
			When:      base.Add(2 * time.Hour),
			Component: "beta",
			Severity:  "info",
			Summary:   "Beta info",
			Detail:    "",
		},
		{
			ID:        "inc-beta-2",
			When:      base.Add(4 * time.Hour),
			Component: "beta",
			Severity:  "warning",
			Summary:   "Beta newest",
			Detail:    "detail-beta-2",
		},
	}
}

func incidentsDeps(reader mcp.IncidentsReader) mcp.Deps {
	return mcp.Deps{
		Centrals:  &fakeCentrals{names: []string{"alpha", "beta"}},
		Devices:   newFakeDevices(),
		Incidents: reader,
	}
}

// TestListIncidentsNewestFirst verifies that list_incidents with no args
// returns all incidents ordered newest-first, with all fields mirrored and
// When formatted as RFC3339 UTC.
func TestListIncidentsNewestFirst(t *testing.T) {
	reader := &fakeIncidentsReader{incidents: fixedIncidents()}
	cs := connect(t, incidentsDeps(reader))
	defer cs.Close()

	res := callTool(t, cs, "list_incidents", map[string]any{})
	if res.IsError {
		t.Fatalf("list_incidents returned error: %v", res.Content)
	}

	var out struct {
		Incidents []struct {
			ID        string `json:"id"`
			When      string `json:"when"`
			Component string `json:"component"`
			Severity  string `json:"severity"`
			Summary   string `json:"summary"`
			Detail    string `json:"detail"`
		} `json:"incidents"`
	}
	unmarshalStructured(t, res, &out)

	if len(out.Incidents) != 4 {
		t.Fatalf("expected 4 incidents, got %d", len(out.Incidents))
	}

	// Verify newest-first order by ID (inc-beta-2 is T+4h, inc-alpha-if is
	// T+3h, inc-beta-1 is T+2h, inc-alpha-1 is T+1h).
	wantOrder := []string{"inc-beta-2", "inc-alpha-if", "inc-beta-1", "inc-alpha-1"}
	for i, want := range wantOrder {
		if out.Incidents[i].ID != want {
			t.Errorf("position %d: want ID %q, got %q", i, want, out.Incidents[i].ID)
		}
	}

	// Spot-check field mapping on the first result.
	first := out.Incidents[0]
	if first.Component != "beta" {
		t.Errorf("component: want %q, got %q", "beta", first.Component)
	}
	if first.Severity != "warning" {
		t.Errorf("severity: want %q, got %q", "warning", first.Severity)
	}
	if first.Summary != "Beta newest" {
		t.Errorf("summary: want %q, got %q", "Beta newest", first.Summary)
	}
	if first.Detail != "detail-beta-2" {
		t.Errorf("detail: want %q, got %q", "detail-beta-2", first.Detail)
	}

	// Verify RFC3339 UTC formatting: T+4h from base.
	wantWhen := time.Date(2026, 1, 1, 4, 0, 0, 0, time.UTC).Format(time.RFC3339)
	if first.When != wantWhen {
		t.Errorf("when: want %q, got %q", wantWhen, first.When)
	}
}

// TestListIncidentsCentralFilter verifies that central_name:"alpha" returns
// incidents whose Component is exactly "alpha" OR starts with "alpha/",
// excluding "beta" incidents.
func TestListIncidentsCentralFilter(t *testing.T) {
	reader := &fakeIncidentsReader{incidents: fixedIncidents()}
	cs := connect(t, incidentsDeps(reader))
	defer cs.Close()

	res := callTool(t, cs, "list_incidents", map[string]any{"central_name": "alpha"})
	if res.IsError {
		t.Fatalf("list_incidents with central_name returned error: %v", res.Content)
	}

	var out struct {
		Incidents []struct {
			ID        string `json:"id"`
			Component string `json:"component"`
		} `json:"incidents"`
	}
	unmarshalStructured(t, res, &out)

	if len(out.Incidents) != 2 {
		t.Fatalf("expected 2 alpha incidents, got %d", len(out.Incidents))
	}

	ids := map[string]bool{}
	for _, inc := range out.Incidents {
		ids[inc.ID] = true
		if inc.Component != "alpha" && inc.Component != "alpha/HmIP-RF" {
			t.Errorf("unexpected component %q in alpha-scoped result", inc.Component)
		}
	}
	if !ids["inc-alpha-1"] {
		t.Error("expected inc-alpha-1 in alpha-scoped result")
	}
	if !ids["inc-alpha-if"] {
		t.Error("expected inc-alpha-if (Component=alpha/HmIP-RF) in alpha-scoped result")
	}
}

// TestListIncidentsLimitOne verifies that limit:1 returns exactly the single
// newest incident.
func TestListIncidentsLimitOne(t *testing.T) {
	reader := &fakeIncidentsReader{incidents: fixedIncidents()}
	cs := connect(t, incidentsDeps(reader))
	defer cs.Close()

	res := callTool(t, cs, "list_incidents", map[string]any{"limit": 1})
	if res.IsError {
		t.Fatalf("list_incidents with limit=1 returned error: %v", res.Content)
	}

	var out struct {
		Incidents []struct {
			ID string `json:"id"`
		} `json:"incidents"`
	}
	unmarshalStructured(t, res, &out)

	if len(out.Incidents) != 1 {
		t.Fatalf("expected 1 incident with limit=1, got %d", len(out.Incidents))
	}
	// The newest incident is inc-beta-2 at T+4h.
	if out.Incidents[0].ID != "inc-beta-2" {
		t.Errorf("expected newest incident inc-beta-2, got %q", out.Incidents[0].ID)
	}
}

// TestListIncidentsAbsentWhenNil verifies that list_incidents is not
// registered in the tool catalogue when Deps.Incidents is nil.
func TestListIncidentsAbsentWhenNil(t *testing.T) {
	deps := mcp.Deps{
		Centrals:  &fakeCentrals{names: []string{"alpha"}},
		Devices:   newFakeDevices(),
		Incidents: nil, // explicitly absent
	}
	cs := connect(t, deps)
	defer cs.Close()

	names := toolNames(t, cs)
	if names["list_incidents"] {
		t.Fatal("list_incidents must not be in the tool catalogue when Deps.Incidents is nil")
	}
}
