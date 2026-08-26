// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mcp_test

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/mcp"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
)

// ─── fakes ───────────────────────────────────────────────────────────────────

type fakeGroupsReader struct {
	lastCentral string
	entries     []handlers.GroupCentralEntry
	err         error
}

func (f *fakeGroupsReader) List(_ context.Context, central string) ([]handlers.GroupCentralEntry, error) {
	f.lastCentral = central
	if f.err != nil {
		return nil, f.err
	}
	return f.entries, nil
}

type fakeAreaLister struct {
	rows        []sqlite.AreaRow
	assignments []sqlite.RoomAreaRow
	rowsErr     error
	assignErr   error
}

func (f *fakeAreaLister) GetAll(context.Context) ([]sqlite.AreaRow, error) {
	if f.rowsErr != nil {
		return nil, f.rowsErr
	}
	return f.rows, nil
}

func (f *fakeAreaLister) ListAssignments(context.Context) ([]sqlite.RoomAreaRow, error) {
	if f.assignErr != nil {
		return nil, f.assignErr
	}
	return f.assignments, nil
}

type fakeInterfaceLister struct {
	interfaces []hmapi.InterfaceState
}

func (f *fakeInterfaceLister) Interfaces() []hmapi.InterfaceState { return f.interfaces }

type fakeHistoryReader struct {
	lastQuery handlers.HistoryQuery
	buckets   []handlers.HistoryBucket
	tier      string
	err       error
}

func (f *fakeHistoryReader) Query(_ context.Context, q handlers.HistoryQuery) ([]handlers.HistoryBucket, string, error) {
	f.lastQuery = q
	if f.err != nil {
		return nil, "", f.err
	}
	return f.buckets, f.tier, nil
}

type fakeVisibilityLister struct {
	byCentral map[string][]sqlite.UnIgnoreEntry
	err       error
}

func (f *fakeVisibilityLister) List(_ context.Context, centralName string) ([]sqlite.UnIgnoreEntry, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.byCentral[centralName], nil
}

type fakeEnergyReader struct {
	lastQuery handlers.EnergyQuery
	resp      handlers.EnergyResponse
	err       error
}

func (f *fakeEnergyReader) Energy(_ context.Context, q handlers.EnergyQuery) (handlers.EnergyResponse, error) {
	f.lastQuery = q
	if f.err != nil {
		return handlers.EnergyResponse{}, f.err
	}
	return f.resp, nil
}

type fakeLinkLister struct {
	lastCentral string
	lastLocale  string
	links       []hmapi.Link
	err         error
}

func (f *fakeLinkLister) ListAllLinks(_ context.Context, centralName, locale string) ([]hmapi.Link, error) {
	f.lastCentral = centralName
	f.lastLocale = locale
	if f.err != nil {
		return nil, f.err
	}
	return f.links, nil
}

type fakeScheduleLister struct {
	items []hmapi.ScheduleDeviceSummary
	err   error
}

func (f *fakeScheduleLister) ListScheduleDevices(context.Context) ([]hmapi.ScheduleDeviceSummary, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.items, nil
}

// fleetDeps builds a Deps with all eight fleet seams present, plus a
// two-central roster so central-scoping and unknown-central rejection can
// be exercised.
func fleetDeps() mcp.Deps {
	return mcp.Deps{
		Centrals: &fakeCentrals{names: []string{"alpha", "beta"}},
		Devices:  newFakeDevices(),
	}
}

// ─── registration gating ─────────────────────────────────────────────────────

// TestFleetTools_NilSeamsLeaveToolsUnregistered proves each of the eight
// tools stays off the catalogue when its own dependency is nil, even
// though every other Deps field is wired — an unwired seam must never
// advertise a tool that cannot answer.
func TestFleetTools_NilSeamsLeaveToolsUnregistered(t *testing.T) {
	cs := connect(t, fleetDeps())
	defer cs.Close()
	names := toolNames(t, cs)
	for _, tool := range []string{
		"list_groups", "list_areas", "list_interfaces", "get_measurements",
		"list_hidden_parameters", "get_energy", "list_links", "list_schedules",
	} {
		if names[tool] {
			t.Errorf("%s: registered despite a nil seam", tool)
		}
	}
}

// TestFleetTools_WiredSeamsRegisterTools proves each tool appears once its
// own seam is wired.
func TestFleetTools_WiredSeamsRegisterTools(t *testing.T) {
	deps := fleetDeps()
	deps.Groups = &fakeGroupsReader{}
	deps.Areas = &fakeAreaLister{}
	deps.Interfaces = &fakeInterfaceLister{}
	deps.History = &fakeHistoryReader{}
	deps.Visibility = &fakeVisibilityLister{}
	deps.Energy = &fakeEnergyReader{}
	deps.Links = &fakeLinkLister{}
	deps.Schedules = &fakeScheduleLister{}

	cs := connect(t, deps)
	defer cs.Close()
	names := toolNames(t, cs)
	for _, tool := range []string{
		"list_groups", "list_areas", "list_interfaces", "get_measurements",
		"list_hidden_parameters", "get_energy", "list_links", "list_schedules",
	} {
		if !names[tool] {
			t.Errorf("%s: not registered despite a wired seam", tool)
		}
	}
}

// ─── list_groups ─────────────────────────────────────────────────────────────

func TestListGroups_HappyPath(t *testing.T) {
	reader := &fakeGroupsReader{entries: []handlers.GroupCentralEntry{
		{Central: "alpha", Groups: []handlers.GroupEntry{{ID: 1, Name: "Heating"}}},
	}}
	deps := fleetDeps()
	deps.Groups = reader
	cs := connect(t, deps)
	defer cs.Close()

	res := callTool(t, cs, "list_groups", map[string]any{"central_name": "alpha"})
	if res.IsError {
		t.Fatalf("list_groups returned error: %v", res.Content)
	}
	if reader.lastCentral != "alpha" {
		t.Errorf("lastCentral: want alpha, got %q", reader.lastCentral)
	}
	var out struct {
		Centrals []struct {
			Central string `json:"central"`
		} `json:"centrals"`
	}
	unmarshalStructured(t, res, &out)
	if len(out.Centrals) != 1 || out.Centrals[0].Central != "alpha" {
		t.Errorf("unexpected result: %+v", out)
	}
}

func TestListGroups_UnknownCentral(t *testing.T) {
	deps := fleetDeps()
	deps.Groups = &fakeGroupsReader{}
	cs := connect(t, deps)
	defer cs.Close()

	res := callTool(t, cs, "list_groups", map[string]any{"central_name": "ghost"})
	if !res.IsError {
		t.Fatal("expected an error for an unknown central")
	}
}

// ─── list_areas ──────────────────────────────────────────────────────────────

func TestListAreas_ScopesRoomsToCentral(t *testing.T) {
	lister := &fakeAreaLister{
		rows: []sqlite.AreaRow{{ID: "area-1", Name: "Ground Floor", Position: 1}},
		assignments: []sqlite.RoomAreaRow{
			{CentralName: "alpha", RoomName: "Kitchen", AreaID: "area-1"},
			{CentralName: "beta", RoomName: "Garage", AreaID: "area-1"},
		},
	}
	deps := fleetDeps()
	deps.Areas = lister
	cs := connect(t, deps)
	defer cs.Close()

	res := callTool(t, cs, "list_areas", map[string]any{"central_name": "alpha"})
	if res.IsError {
		t.Fatalf("list_areas returned error: %v", res.Content)
	}
	var out struct {
		Areas []hmapi.Area `json:"areas"`
	}
	unmarshalStructured(t, res, &out)
	if len(out.Areas) != 1 {
		t.Fatalf("expected 1 area, got %d", len(out.Areas))
	}
	if len(out.Areas[0].Rooms) != 1 || out.Areas[0].Rooms[0].Room != "Kitchen" {
		t.Errorf("expected only alpha's room, got %+v", out.Areas[0].Rooms)
	}
}

func TestListAreas_NoScopeReturnsEveryRoom(t *testing.T) {
	lister := &fakeAreaLister{
		rows: []sqlite.AreaRow{{ID: "area-1", Name: "Ground Floor"}},
		assignments: []sqlite.RoomAreaRow{
			{CentralName: "alpha", RoomName: "Kitchen", AreaID: "area-1"},
			{CentralName: "beta", RoomName: "Garage", AreaID: "area-1"},
		},
	}
	deps := fleetDeps()
	deps.Areas = lister
	cs := connect(t, deps)
	defer cs.Close()

	res := callTool(t, cs, "list_areas", map[string]any{})
	if res.IsError {
		t.Fatalf("list_areas returned error: %v", res.Content)
	}
	var out struct {
		Areas []hmapi.Area `json:"areas"`
	}
	unmarshalStructured(t, res, &out)
	if len(out.Areas) != 1 || len(out.Areas[0].Rooms) != 2 {
		t.Errorf("expected both rooms unscoped, got %+v", out.Areas)
	}
}

// ─── list_interfaces ─────────────────────────────────────────────────────────

func TestListInterfaces_ProjectsState(t *testing.T) {
	dutyCycle := 12
	deps := fleetDeps()
	deps.Interfaces = &fakeInterfaceLister{interfaces: []hmapi.InterfaceState{
		{ID: "alpha-HmIP-RF", Name: "HmIP-RF", Connected: true, Interface: "HmIP-RF", DutyCycle: &dutyCycle},
	}}
	cs := connect(t, deps)
	defer cs.Close()

	res := callTool(t, cs, "list_interfaces", map[string]any{})
	if res.IsError {
		t.Fatalf("list_interfaces returned error: %v", res.Content)
	}
	var out struct {
		Interfaces []hmapi.InterfaceState `json:"interfaces"`
	}
	unmarshalStructured(t, res, &out)
	if len(out.Interfaces) != 1 || !out.Interfaces[0].Connected || out.Interfaces[0].DutyCycle == nil || *out.Interfaces[0].DutyCycle != 12 {
		t.Errorf("unexpected result: %+v", out.Interfaces)
	}
}

// ─── get_measurements ────────────────────────────────────────────────────────

func TestGetMeasurements_MissingRequiredFieldErrors(t *testing.T) {
	deps := fleetDeps()
	deps.History = &fakeHistoryReader{}
	cs := connect(t, deps)
	defer cs.Close()

	res := callTool(t, cs, "get_measurements", map[string]any{
		"central": "alpha",
		// interface_id, channel, parameter, from, to all missing.
	})
	if !res.IsError {
		t.Fatal("expected an error for missing required fields")
	}
}

func TestGetMeasurements_HappyPath(t *testing.T) {
	reader := &fakeHistoryReader{
		buckets: []handlers.HistoryBucket{{TS: time.Unix(0, 0).UTC(), Avg: 21.5, Count: 3}},
		tier:    "hour",
	}
	deps := fleetDeps()
	deps.History = reader
	cs := connect(t, deps)
	defer cs.Close()

	res := callTool(t, cs, "get_measurements", map[string]any{
		"central":      "alpha",
		"interface_id": "HmIP-RF",
		"channel":      "0001D3C99C1234:1",
		"parameter":    "TEMPERATURE",
		"from":         "2026-01-01T00:00:00Z",
		"to":           "2026-01-02T00:00:00Z",
	})
	if res.IsError {
		t.Fatalf("get_measurements returned error: %v", res.Content)
	}
	if reader.lastQuery.Buckets != 200 {
		t.Errorf("expected default bucket count 200, got %d", reader.lastQuery.Buckets)
	}
	var out struct {
		Buckets []handlers.HistoryBucket `json:"buckets"`
		Tier    string                   `json:"tier"`
	}
	unmarshalStructured(t, res, &out)
	if len(out.Buckets) != 1 || out.Tier != "hour" {
		t.Errorf("unexpected result: %+v", out)
	}
}

// ─── list_hidden_parameters ──────────────────────────────────────────────────

func TestListHiddenParameters_SpansCentrals(t *testing.T) {
	lister := &fakeVisibilityLister{byCentral: map[string][]sqlite.UnIgnoreEntry{
		"alpha": {{Pattern: "HmIP-*:1:LOWBAT"}},
		"beta":  {{Pattern: "HmIP-*:2:*"}},
	}}
	deps := fleetDeps()
	deps.Visibility = lister
	cs := connect(t, deps)
	defer cs.Close()

	res := callTool(t, cs, "list_hidden_parameters", map[string]any{})
	if res.IsError {
		t.Fatalf("list_hidden_parameters returned error: %v", res.Content)
	}
	var out struct {
		Patterns []struct {
			Pattern string `json:"pattern"`
			Central string `json:"central"`
		} `json:"patterns"`
	}
	unmarshalStructured(t, res, &out)
	if len(out.Patterns) != 2 {
		t.Fatalf("expected 2 patterns across both centrals, got %d: %+v", len(out.Patterns), out.Patterns)
	}
}

func TestListHiddenParameters_ScopedToOneCentral(t *testing.T) {
	lister := &fakeVisibilityLister{byCentral: map[string][]sqlite.UnIgnoreEntry{
		"alpha": {{Pattern: "HmIP-*:1:LOWBAT"}},
		"beta":  {{Pattern: "HmIP-*:2:*"}},
	}}
	deps := fleetDeps()
	deps.Visibility = lister
	cs := connect(t, deps)
	defer cs.Close()

	res := callTool(t, cs, "list_hidden_parameters", map[string]any{"central_name": "alpha"})
	if res.IsError {
		t.Fatalf("list_hidden_parameters returned error: %v", res.Content)
	}
	var out struct {
		Patterns []struct {
			Pattern string `json:"pattern"`
			Central string `json:"central"`
		} `json:"patterns"`
	}
	unmarshalStructured(t, res, &out)
	if len(out.Patterns) != 1 || out.Patterns[0].Central != "alpha" {
		t.Errorf("unexpected result: %+v", out.Patterns)
	}
}

// ─── get_energy ──────────────────────────────────────────────────────────────

func TestGetEnergy_DefaultsGroupToDay(t *testing.T) {
	reader := &fakeEnergyReader{resp: handlers.EnergyResponse{Group: "day"}}
	deps := fleetDeps()
	deps.Energy = reader
	cs := connect(t, deps)
	defer cs.Close()

	res := callTool(t, cs, "get_energy", map[string]any{
		"central": "alpha",
		"from":    "2026-01-01T00:00:00Z",
		"to":      "2026-01-02T00:00:00Z",
	})
	if res.IsError {
		t.Fatalf("get_energy returned error: %v", res.Content)
	}
	if reader.lastQuery.Group != "day" {
		t.Errorf("group: want day, got %q", reader.lastQuery.Group)
	}
}

func TestGetEnergy_InvalidGroupErrors(t *testing.T) {
	deps := fleetDeps()
	deps.Energy = &fakeEnergyReader{}
	cs := connect(t, deps)
	defer cs.Close()

	res := callTool(t, cs, "get_energy", map[string]any{
		"central": "alpha",
		"from":    "2026-01-01T00:00:00Z",
		"to":      "2026-01-02T00:00:00Z",
		"group":   "week",
	})
	if !res.IsError {
		t.Fatal("expected an error for an invalid group")
	}
}

func TestGetEnergy_MissingCentralErrors(t *testing.T) {
	deps := fleetDeps()
	deps.Energy = &fakeEnergyReader{}
	cs := connect(t, deps)
	defer cs.Close()

	res := callTool(t, cs, "get_energy", map[string]any{
		"from": "2026-01-01T00:00:00Z",
		"to":   "2026-01-02T00:00:00Z",
	})
	if !res.IsError {
		t.Fatal("expected an error for a missing central")
	}
}

// ─── list_links ──────────────────────────────────────────────────────────────

func TestListLinks_HappyPath(t *testing.T) {
	lister := &fakeLinkLister{links: []hmapi.Link{{Sender: "AAA:1", Receiver: "BBB:1", Direction: "sender"}}}
	deps := fleetDeps()
	deps.Links = lister
	cs := connect(t, deps)
	defer cs.Close()

	res := callTool(t, cs, "list_links", map[string]any{"central_name": "alpha"})
	if res.IsError {
		t.Fatalf("list_links returned error: %v", res.Content)
	}
	if lister.lastCentral != "alpha" {
		t.Errorf("lastCentral: want alpha, got %q", lister.lastCentral)
	}
	var out struct {
		Links []hmapi.Link `json:"links"`
	}
	unmarshalStructured(t, res, &out)
	if len(out.Links) != 1 {
		t.Errorf("expected 1 link, got %d", len(out.Links))
	}
}

func TestListLinks_UnknownCentral(t *testing.T) {
	deps := fleetDeps()
	deps.Links = &fakeLinkLister{}
	cs := connect(t, deps)
	defer cs.Close()

	res := callTool(t, cs, "list_links", map[string]any{"central_name": "ghost"})
	if !res.IsError {
		t.Fatal("expected an error for an unknown central")
	}
}

// ─── list_schedules ──────────────────────────────────────────────────────────

func TestListSchedules_HappyPath(t *testing.T) {
	lister := &fakeScheduleLister{items: []hmapi.ScheduleDeviceSummary{
		{Central: "alpha", Address: "0001D3C99C1234", Name: "Thermostat", Kind: "climate"},
	}}
	deps := fleetDeps()
	deps.Schedules = lister
	cs := connect(t, deps)
	defer cs.Close()

	res := callTool(t, cs, "list_schedules", map[string]any{})
	if res.IsError {
		t.Fatalf("list_schedules returned error: %v", res.Content)
	}
	var out struct {
		Schedules []hmapi.ScheduleDeviceSummary `json:"schedules"`
	}
	unmarshalStructured(t, res, &out)
	if len(out.Schedules) != 1 || out.Schedules[0].Kind != "climate" {
		t.Errorf("unexpected result: %+v", out.Schedules)
	}
}

func TestListSchedules_EmptyReturnsEmptyArray(t *testing.T) {
	deps := fleetDeps()
	deps.Schedules = &fakeScheduleLister{}
	cs := connect(t, deps)
	defer cs.Close()

	res := callTool(t, cs, "list_schedules", map[string]any{})
	if res.IsError {
		t.Fatalf("list_schedules returned error: %v", res.Content)
	}
	var out struct {
		Schedules []hmapi.ScheduleDeviceSummary `json:"schedules"`
	}
	unmarshalStructured(t, res, &out)
	if out.Schedules == nil {
		t.Error("expected an empty array, got nil")
	}
}
