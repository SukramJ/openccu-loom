// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mcp_test

import (
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/internal/model/weekprofile"
	"github.com/SukramJ/openccu-loom/internal/north/mcp"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

// hubDeps builds a Deps wired with the given fakeHubs and fakeCentrals.
func hubDeps(centrals *fakeCentrals, hubs *fakeHubs, devs *fakeDevices) mcp.Deps {
	return mcp.Deps{
		Centrals: centrals,
		Hubs:     hubs,
		Devices:  devs,
	}
}

// ─── list_programs ────────────────────────────────────────────────────────────

func TestListPrograms_PopulatedHub(t *testing.T) {
	h := hub.NewHub("alpha")
	h.PutProgram(hub.NewProgram("alpha", "prog-1", "Lights On", "turn lights on", false, nil))
	h.PutProgram(hub.NewProgram("alpha", "prog-2", "Lights Off", "", false, nil))
	h.PutProgram(hub.NewProgram("alpha", "Tmp_internal", "Internal", "", true, nil)) // IsInternal — must be filtered

	hubs := newFakeHubs()
	hubs.add("alpha", h)
	centrals := &fakeCentrals{names: []string{"alpha"}}

	cs := connect(t, hubDeps(centrals, hubs, newFakeDevices()))
	defer cs.Close()

	res := callTool(t, cs, "list_programs", map[string]any{"central_name": "alpha"})
	if res.IsError {
		t.Fatalf("list_programs returned error: %v", res.Content)
	}

	var out struct {
		Programs []struct {
			ID         string `json:"id"`
			Name       string `json:"name"`
			Central    string `json:"central"`
			IsInternal bool   // not in JSON, just for clarity
		} `json:"programs"`
	}
	unmarshalStructured(t, res, &out)

	if len(out.Programs) != 2 {
		t.Fatalf("expected 2 programs (internal filtered), got %d", len(out.Programs))
	}
	ids := map[string]bool{}
	for _, p := range out.Programs {
		ids[p.ID] = true
		if p.Central != "alpha" {
			t.Errorf("central: want alpha, got %q", p.Central)
		}
	}
	if ids["Tmp_internal"] {
		t.Error("internal program must be filtered out")
	}
	if !ids["prog-1"] || !ids["prog-2"] {
		t.Errorf("expected prog-1 and prog-2, got %v", ids)
	}
}

func TestListPrograms_FilterInternalPrograms(t *testing.T) {
	h := hub.NewHub("alpha")
	h.PutProgram(hub.NewProgram("alpha", "Tmp_hidden", "Hidden", "", true, nil))

	hubs := newFakeHubs()
	hubs.add("alpha", h)
	centrals := &fakeCentrals{names: []string{"alpha"}}

	cs := connect(t, hubDeps(centrals, hubs, newFakeDevices()))
	defer cs.Close()

	res := callTool(t, cs, "list_programs", map[string]any{})
	if res.IsError {
		t.Fatalf("list_programs returned error")
	}

	var out struct {
		Programs []struct{ ID string } `json:"programs"`
	}
	unmarshalStructured(t, res, &out)
	if len(out.Programs) != 0 {
		t.Fatalf("expected 0 programs when all are internal, got %d", len(out.Programs))
	}
}

// TestListPrograms_UnknownCentralReturnsError pins the fix for
// A central_name that names no configured central must
// surface as an error, not a well-formed empty result an agent would
// report as "you have no programs" — indistinguishable from a central
// that genuinely has none. Mirrors the WS ErrCentralUnknown path.
func TestListPrograms_UnknownCentralReturnsError(t *testing.T) {
	hubs := newFakeHubs() // no "ghost" central registered
	centrals := &fakeCentrals{names: []string{"alpha"}}

	cs := connect(t, hubDeps(centrals, hubs, newFakeDevices()))
	defer cs.Close()

	res := callTool(t, cs, "list_programs", map[string]any{"central_name": "ghost"})
	if !res.IsError {
		t.Fatal("expected IsError=true for an unknown central_name")
	}
}

// TestCentralScopedHubTools_UnknownCentralReturnsError table-tests every
// remaining centralsToScan-backed tool (list_programs' own sibling test
// above covers the pattern in full detail): each must reject an unknown
// central_name as an error rather than silently returning an empty
// list/aggregate.
func TestCentralScopedHubTools_UnknownCentralReturnsError(t *testing.T) {
	tools := []string{
		"list_sysvars",
		"list_service_messages",
		"list_alarm_messages",
		"list_inbox",
		"get_system_info",
	}
	for _, tool := range tools {
		t.Run(tool, func(t *testing.T) {
			hubs := newFakeHubs()
			centrals := &fakeCentrals{names: []string{"alpha"}}
			cs := connect(t, hubDeps(centrals, hubs, newFakeDevices()))
			defer cs.Close()

			res := callTool(t, cs, tool, map[string]any{"central_name": "ghost"})
			if !res.IsError {
				t.Fatalf("%s: expected IsError=true for an unknown central_name", tool)
			}
		})
	}
}

func TestListPrograms_MultiCCU_SpansAll(t *testing.T) {
	hAlpha := hub.NewHub("alpha")
	hAlpha.PutProgram(hub.NewProgram("alpha", "alpha-prog", "Alpha", "", false, nil))
	hBeta := hub.NewHub("beta")
	hBeta.PutProgram(hub.NewProgram("beta", "beta-prog", "Beta", "", false, nil))

	hubs := newFakeHubs()
	hubs.add("alpha", hAlpha)
	hubs.add("beta", hBeta)
	centrals := &fakeCentrals{names: []string{"alpha", "beta"}}

	cs := connect(t, hubDeps(centrals, hubs, newFakeDevices()))
	defer cs.Close()

	// No central_name → spans both.
	res := callTool(t, cs, "list_programs", map[string]any{})
	if res.IsError {
		t.Fatalf("list_programs returned error")
	}
	var out struct {
		Programs []struct{ ID string } `json:"programs"`
	}
	unmarshalStructured(t, res, &out)
	if len(out.Programs) != 2 {
		t.Fatalf("expected 2 programs across both centrals, got %d", len(out.Programs))
	}
}

func TestListPrograms_MultiCCU_ScopedToOne(t *testing.T) {
	hAlpha := hub.NewHub("alpha")
	hAlpha.PutProgram(hub.NewProgram("alpha", "alpha-prog", "Alpha", "", false, nil))
	hBeta := hub.NewHub("beta")
	hBeta.PutProgram(hub.NewProgram("beta", "beta-prog", "Beta", "", false, nil))

	hubs := newFakeHubs()
	hubs.add("alpha", hAlpha)
	hubs.add("beta", hBeta)
	centrals := &fakeCentrals{names: []string{"alpha", "beta"}}

	cs := connect(t, hubDeps(centrals, hubs, newFakeDevices()))
	defer cs.Close()

	res := callTool(t, cs, "list_programs", map[string]any{"central_name": "alpha"})
	if res.IsError {
		t.Fatalf("list_programs scoped returned error")
	}
	var out struct {
		Programs []struct{ ID string } `json:"programs"`
	}
	unmarshalStructured(t, res, &out)
	if len(out.Programs) != 1 {
		t.Fatalf("expected 1 program for alpha only, got %d", len(out.Programs))
	}
	if out.Programs[0].ID != "alpha-prog" {
		t.Errorf("expected alpha-prog, got %q", out.Programs[0].ID)
	}
}

// ─── list_sysvars ─────────────────────────────────────────────────────────────

func TestListSysvars_PopulatedHub(t *testing.T) {
	h := hub.NewHub("alpha")
	sv := hub.NewSysvar("alpha", "Temperature", "", hmenum.HubValueTypeFloat, nil)
	sv.OnValue(hmtypes.FloatValue(21.5))
	h.PutSysvar(sv)

	svInternal := hub.NewSysvar("alpha", "OldVal_x", "", hmenum.HubValueTypeFloat, nil)
	svInternal.IsInternal = true
	h.PutSysvar(svInternal)

	hubs := newFakeHubs()
	hubs.add("alpha", h)
	centrals := &fakeCentrals{names: []string{"alpha"}}

	cs := connect(t, hubDeps(centrals, hubs, newFakeDevices()))
	defer cs.Close()

	res := callTool(t, cs, "list_sysvars", map[string]any{"central_name": "alpha"})
	if res.IsError {
		t.Fatalf("list_sysvars returned error: %v", res.Content)
	}

	var out struct {
		Sysvars []struct {
			Name    string `json:"name"`
			Central string `json:"central"`
		} `json:"sysvars"`
	}
	unmarshalStructured(t, res, &out)

	if len(out.Sysvars) != 1 {
		t.Fatalf("expected 1 sysvar (internal filtered), got %d", len(out.Sysvars))
	}
	if out.Sysvars[0].Name != "Temperature" {
		t.Errorf("expected Temperature, got %q", out.Sysvars[0].Name)
	}
}

func TestListSysvars_MultiCCU_SpansAll(t *testing.T) {
	hAlpha := hub.NewHub("alpha")
	hAlpha.PutSysvar(hub.NewSysvar("alpha", "SV_Alpha", "", hmenum.HubValueTypeLogic, nil))
	hBeta := hub.NewHub("beta")
	hBeta.PutSysvar(hub.NewSysvar("beta", "SV_Beta", "", hmenum.HubValueTypeLogic, nil))

	hubs := newFakeHubs()
	hubs.add("alpha", hAlpha)
	hubs.add("beta", hBeta)
	centrals := &fakeCentrals{names: []string{"alpha", "beta"}}

	cs := connect(t, hubDeps(centrals, hubs, newFakeDevices()))
	defer cs.Close()

	res := callTool(t, cs, "list_sysvars", map[string]any{})
	if res.IsError {
		t.Fatalf("list_sysvars returned error")
	}

	var out struct {
		Sysvars []struct{ Name string } `json:"sysvars"`
	}
	unmarshalStructured(t, res, &out)
	if len(out.Sysvars) != 2 {
		t.Fatalf("expected 2 sysvars across both centrals, got %d", len(out.Sysvars))
	}
}

func TestListSysvars_MultiCCU_ScopedToOne(t *testing.T) {
	hAlpha := hub.NewHub("alpha")
	hAlpha.PutSysvar(hub.NewSysvar("alpha", "SV_Alpha", "", hmenum.HubValueTypeLogic, nil))
	hBeta := hub.NewHub("beta")
	hBeta.PutSysvar(hub.NewSysvar("beta", "SV_Beta", "", hmenum.HubValueTypeLogic, nil))

	hubs := newFakeHubs()
	hubs.add("alpha", hAlpha)
	hubs.add("beta", hBeta)
	centrals := &fakeCentrals{names: []string{"alpha", "beta"}}

	cs := connect(t, hubDeps(centrals, hubs, newFakeDevices()))
	defer cs.Close()

	res := callTool(t, cs, "list_sysvars", map[string]any{"central_name": "beta"})
	if res.IsError {
		t.Fatalf("list_sysvars scoped returned error")
	}

	var out struct {
		Sysvars []struct{ Name string } `json:"sysvars"`
	}
	unmarshalStructured(t, res, &out)
	if len(out.Sysvars) != 1 {
		t.Fatalf("expected 1 sysvar for beta, got %d", len(out.Sysvars))
	}
	if out.Sysvars[0].Name != "SV_Beta" {
		t.Errorf("expected SV_Beta, got %q", out.Sysvars[0].Name)
	}
}

// ─── list_service_messages ────────────────────────────────────────────────────

// TestListServiceMessages_PopulatedHub verifies the MCP tool surfaces a
// service message's identity, timing and location fields — including
// Rooms/Functions with more than one entry each, the shape that used to
// break JSON deserialization further upstream when the ReGa script
// joined them with a raw tab (see get_service_messages.fn).
func TestListServiceMessages_PopulatedHub(t *testing.T) {
	h := hub.NewHub("alpha")
	ts := time.Now().Truncate(time.Second)
	last := ts.Add(-time.Hour)
	h.ServiceMessages.Replace([]hub.ServiceMessage{
		{
			ID:            "sm-1",
			Name:          "Low Battery",
			Address:       "ADDR001:0",
			DeviceName:    "My Device",
			Type:          hmenum.ServiceMessageTypeGeneric,
			Timestamp:     ts,
			LastTimestamp: last,
			Rooms:         []string{"Living Room", "Kitchen"},
			Functions:     []string{"Licht", "Sicherheit", "Umwelt"},
			Quittable:     true,
		},
	})

	hubs := newFakeHubs()
	hubs.add("alpha", h)
	centrals := &fakeCentrals{names: []string{"alpha"}}

	cs := connect(t, hubDeps(centrals, hubs, newFakeDevices()))
	defer cs.Close()

	res := callTool(t, cs, "list_service_messages", map[string]any{"central_name": "alpha"})
	if res.IsError {
		t.Fatalf("list_service_messages returned error: %v", res.Content)
	}

	var out struct {
		Messages []struct {
			ID            string   `json:"id"`
			Name          string   `json:"name"`
			Timestamp     string   `json:"timestamp"`
			LastTimestamp string   `json:"last_timestamp"`
			Rooms         []string `json:"rooms"`
			Functions     []string `json:"functions"`
			Quittable     bool     `json:"quittable"`
			Central       string   `json:"central"`
		} `json:"messages"`
	}
	unmarshalStructured(t, res, &out)

	if len(out.Messages) != 1 {
		t.Fatalf("expected 1 service message, got %d", len(out.Messages))
	}
	m := out.Messages[0]
	if m.ID != "sm-1" {
		t.Errorf("id: want sm-1, got %q", m.ID)
	}
	if m.Name != "Low Battery" {
		t.Errorf("name: want Low Battery, got %q", m.Name)
	}
	if m.Timestamp != ts.UTC().Format(time.RFC3339) {
		t.Errorf("timestamp: want %s, got %q", ts.UTC().Format(time.RFC3339), m.Timestamp)
	}
	if m.LastTimestamp != last.UTC().Format(time.RFC3339) {
		t.Errorf("last_timestamp: want %s, got %q", last.UTC().Format(time.RFC3339), m.LastTimestamp)
	}
	if len(m.Functions) != 3 {
		t.Errorf("functions: want 3 entries, got %v", m.Functions)
	}
	if len(m.Rooms) != 2 {
		t.Errorf("rooms: want 2 entries, got %v", m.Rooms)
	}
	if !m.Quittable {
		t.Error("quittable: want true")
	}
	if m.Central != "alpha" {
		t.Errorf("central: want alpha, got %q", m.Central)
	}
}

func TestListServiceMessages_MultiCCU_SpansAll(t *testing.T) {
	hAlpha := hub.NewHub("alpha")
	hAlpha.ServiceMessages.Replace([]hub.ServiceMessage{{ID: "sm-a", Name: "A"}})
	hBeta := hub.NewHub("beta")
	hBeta.ServiceMessages.Replace([]hub.ServiceMessage{{ID: "sm-b", Name: "B"}})

	hubs := newFakeHubs()
	hubs.add("alpha", hAlpha)
	hubs.add("beta", hBeta)
	centrals := &fakeCentrals{names: []string{"alpha", "beta"}}

	cs := connect(t, hubDeps(centrals, hubs, newFakeDevices()))
	defer cs.Close()

	res := callTool(t, cs, "list_service_messages", map[string]any{})
	if res.IsError {
		t.Fatalf("list_service_messages returned error")
	}

	var out struct {
		Messages []struct{ ID string } `json:"messages"`
	}
	unmarshalStructured(t, res, &out)
	if len(out.Messages) != 2 {
		t.Fatalf("expected 2 messages across both centrals, got %d", len(out.Messages))
	}
}

// ─── list_alarm_messages ──────────────────────────────────────────────────────

// TestListAlarmMessages_PopulatedHub verifies the MCP tool surfaces an
// alarm's identity and timing fields. An alarm entry has no device,
// channel or room — see [hub.AlarmMessage] — so the DTO carries neither.
func TestListAlarmMessages_PopulatedHub(t *testing.T) {
	h := hub.NewHub("alpha")
	ts := time.Now().Truncate(time.Second)
	last := ts.Add(-time.Hour)
	h.Messages.Replace([]hub.AlarmMessage{
		{
			ID:            "alarm-1",
			Name:          "Door Open",
			Timestamp:     ts,
			LastTimestamp: last,
		},
	})

	hubs := newFakeHubs()
	hubs.add("alpha", h)
	centrals := &fakeCentrals{names: []string{"alpha"}}

	cs := connect(t, hubDeps(centrals, hubs, newFakeDevices()))
	defer cs.Close()

	res := callTool(t, cs, "list_alarm_messages", map[string]any{"central_name": "alpha"})
	if res.IsError {
		t.Fatalf("list_alarm_messages returned error: %v", res.Content)
	}

	var out struct {
		Messages []struct {
			ID            string `json:"id"`
			Name          string `json:"name"`
			Timestamp     string `json:"timestamp"`
			LastTimestamp string `json:"last_timestamp"`
			Central       string `json:"central"`
		} `json:"messages"`
	}
	unmarshalStructured(t, res, &out)

	if len(out.Messages) != 1 {
		t.Fatalf("expected 1 alarm message, got %d", len(out.Messages))
	}
	m := out.Messages[0]
	if m.ID != "alarm-1" {
		t.Errorf("id: want alarm-1, got %q", m.ID)
	}
	if m.Timestamp != ts.UTC().Format(time.RFC3339) {
		t.Errorf("timestamp: want %s, got %q", ts.UTC().Format(time.RFC3339), m.Timestamp)
	}
	if m.LastTimestamp != last.UTC().Format(time.RFC3339) {
		t.Errorf("last_timestamp: want %s, got %q", last.UTC().Format(time.RFC3339), m.LastTimestamp)
	}
	if m.Central != "alpha" {
		t.Errorf("central: want alpha, got %q", m.Central)
	}
}

func TestListAlarmMessages_MultiCCU_SpansAll(t *testing.T) {
	hAlpha := hub.NewHub("alpha")
	hAlpha.Messages.Replace([]hub.AlarmMessage{{ID: "a1", Name: "A"}})
	hBeta := hub.NewHub("beta")
	hBeta.Messages.Replace([]hub.AlarmMessage{{ID: "b1", Name: "B"}, {ID: "b2", Name: "B2"}})

	hubs := newFakeHubs()
	hubs.add("alpha", hAlpha)
	hubs.add("beta", hBeta)
	centrals := &fakeCentrals{names: []string{"alpha", "beta"}}

	cs := connect(t, hubDeps(centrals, hubs, newFakeDevices()))
	defer cs.Close()

	res := callTool(t, cs, "list_alarm_messages", map[string]any{})
	if res.IsError {
		t.Fatalf("list_alarm_messages returned error")
	}

	var out struct {
		Messages []struct{ ID string } `json:"messages"`
	}
	unmarshalStructured(t, res, &out)
	if len(out.Messages) != 3 {
		t.Fatalf("expected 3 alarm messages across both centrals, got %d", len(out.Messages))
	}
}

// ─── list_inbox ───────────────────────────────────────────────────────────────

func TestListInbox_PopulatedHub(t *testing.T) {
	h := hub.NewHub("alpha")
	h.Inbox.Replace([]hub.InboxDevice{
		{
			Address:      "NEWDEV001",
			Name:         "New Sensor",
			Model:        "HmIP-SMI55",
			Interface:    "HmIP-RF",
			Serial:       "SN12345",
			Manufacturer: "eQ-3",
		},
	})

	hubs := newFakeHubs()
	hubs.add("alpha", h)
	centrals := &fakeCentrals{names: []string{"alpha"}}

	cs := connect(t, hubDeps(centrals, hubs, newFakeDevices()))
	defer cs.Close()

	res := callTool(t, cs, "list_inbox", map[string]any{"central_name": "alpha"})
	if res.IsError {
		t.Fatalf("list_inbox returned error: %v", res.Content)
	}

	var out struct {
		Devices []struct {
			Address      string `json:"address"`
			Model        string `json:"model"`
			Manufacturer string `json:"manufacturer"`
			Central      string `json:"central"`
		} `json:"devices"`
	}
	unmarshalStructured(t, res, &out)

	if len(out.Devices) != 1 {
		t.Fatalf("expected 1 inbox device, got %d", len(out.Devices))
	}
	d := out.Devices[0]
	if d.Address != "NEWDEV001" {
		t.Errorf("address: want NEWDEV001, got %q", d.Address)
	}
	if d.Model != "HmIP-SMI55" {
		t.Errorf("model: want HmIP-SMI55, got %q", d.Model)
	}
	if d.Central != "alpha" {
		t.Errorf("central: want alpha, got %q", d.Central)
	}
}

func TestListInbox_EmptyInbox(t *testing.T) {
	h := hub.NewHub("alpha")
	hubs := newFakeHubs()
	hubs.add("alpha", h)
	centrals := &fakeCentrals{names: []string{"alpha"}}

	cs := connect(t, hubDeps(centrals, hubs, newFakeDevices()))
	defer cs.Close()

	res := callTool(t, cs, "list_inbox", map[string]any{})
	if res.IsError {
		t.Fatalf("list_inbox returned error")
	}

	var out struct {
		Devices []struct{ Address string } `json:"devices"`
	}
	unmarshalStructured(t, res, &out)
	if len(out.Devices) != 0 {
		t.Fatalf("expected 0 inbox devices, got %d", len(out.Devices))
	}
}

// ─── get_system_info ─────────────────────────────────────────────────────────

func TestGetSystemInfo_PopulatedHub(t *testing.T) {
	h := hub.NewHub("alpha")
	h.PutProgram(hub.NewProgram("alpha", "p1", "P1", "", false, nil))
	h.PutProgram(hub.NewProgram("alpha", "p2", "P2", "", false, nil))
	h.PutSysvar(hub.NewSysvar("alpha", "SV1", "", hmenum.HubValueTypeLogic, nil))
	h.Update.OnInfo(hub.UpdateInfo{
		CurrentFirmware:   "3.75.6",
		AvailableFirmware: "3.77.0",
		UpdateAvailable:   true,
	})

	hubs := newFakeHubs()
	hubs.add("alpha", h)
	centrals := &fakeCentrals{names: []string{"alpha"}}

	deps := hubDeps(centrals, hubs, newFakeDevices())
	deps.Version = "0.2.0"

	cs := connect(t, deps)
	defer cs.Close()

	res := callTool(t, cs, "get_system_info", map[string]any{"central_name": "alpha"})
	if res.IsError {
		t.Fatalf("get_system_info returned error: %v", res.Content)
	}

	var out struct {
		DaemonVersion string `json:"daemon_version"`
		Centrals      []struct {
			Central           string `json:"central"`
			ProgramCount      int    `json:"program_count"`
			SysvarCount       int    `json:"sysvar_count"`
			CurrentFirmware   string `json:"current_firmware"`
			AvailableFirmware string `json:"available_firmware"`
			UpdateAvailable   bool   `json:"update_available"`
		} `json:"centrals"`
	}
	unmarshalStructured(t, res, &out)

	if out.DaemonVersion != "0.2.0" {
		t.Errorf("daemon_version: want 0.2.0, got %q", out.DaemonVersion)
	}
	if len(out.Centrals) != 1 {
		t.Fatalf("expected 1 central entry, got %d", len(out.Centrals))
	}
	c := out.Centrals[0]
	if c.ProgramCount != 2 {
		t.Errorf("program_count: want 2, got %d", c.ProgramCount)
	}
	if c.SysvarCount != 1 {
		t.Errorf("sysvar_count: want 1, got %d", c.SysvarCount)
	}
	if c.CurrentFirmware != "3.75.6" {
		t.Errorf("current_firmware: want 3.75.6, got %q", c.CurrentFirmware)
	}
	if !c.UpdateAvailable {
		t.Error("update_available: want true")
	}
}

func TestGetSystemInfo_MultiCCU_SpansAll(t *testing.T) {
	hAlpha := hub.NewHub("alpha")
	hAlpha.PutProgram(hub.NewProgram("alpha", "p1", "P1", "", false, nil))
	hBeta := hub.NewHub("beta")
	hBeta.PutSysvar(hub.NewSysvar("beta", "SV1", "", hmenum.HubValueTypeLogic, nil))

	hubs := newFakeHubs()
	hubs.add("alpha", hAlpha)
	hubs.add("beta", hBeta)
	centrals := &fakeCentrals{names: []string{"alpha", "beta"}}

	cs := connect(t, hubDeps(centrals, hubs, newFakeDevices()))
	defer cs.Close()

	res := callTool(t, cs, "get_system_info", map[string]any{})
	if res.IsError {
		t.Fatalf("get_system_info returned error")
	}

	var out struct {
		Centrals []struct {
			Central      string `json:"central"`
			ProgramCount int    `json:"program_count"`
			SysvarCount  int    `json:"sysvar_count"`
		} `json:"centrals"`
	}
	unmarshalStructured(t, res, &out)
	if len(out.Centrals) != 2 {
		t.Fatalf("expected 2 central entries, got %d", len(out.Centrals))
	}
	counts := map[string][2]int{}
	for _, c := range out.Centrals {
		counts[c.Central] = [2]int{c.ProgramCount, c.SysvarCount}
	}
	if counts["alpha"][0] != 1 {
		t.Errorf("alpha program_count: want 1, got %d", counts["alpha"][0])
	}
	if counts["beta"][1] != 1 {
		t.Errorf("beta sysvar_count: want 1, got %d", counts["beta"][1])
	}
}

// ─── list_channels ────────────────────────────────────────────────────────────

func TestListChannels_KnownAddress(t *testing.T) {
	devs := newFakeDevices()
	dev := device.New(device.Config{
		Address:   "DEV001",
		Model:     "HmIP-PS",
		Name:      "Test Device",
		Interface: hmenum.InterfaceHmIPRF,
	})
	dev.AddChannel("DEV001:0", 0, "MAINTENANCE", hmenum.ParamsetKeyValues)
	dev.AddChannel("DEV001:1", 1, "SWITCH_TRANSMITTER", hmenum.ParamsetKeyValues)
	devs.add(dev, "alpha")

	centrals := &fakeCentrals{names: []string{"alpha"}}
	hubs := newFakeHubs()

	cs := connect(t, hubDeps(centrals, hubs, devs))
	defer cs.Close()

	res := callTool(t, cs, "list_channels", map[string]any{"address": "DEV001"})
	if res.IsError {
		t.Fatalf("list_channels returned error: %v", res.Content)
	}

	var out struct {
		Found    bool   `json:"found"`
		Central  string `json:"central"`
		Channels []struct {
			Address string `json:"address"`
			Number  int    `json:"number"`
			Type    string `json:"type"`
		} `json:"channels"`
	}
	unmarshalStructured(t, res, &out)

	if !out.Found {
		t.Fatal("expected Found=true for known device DEV001")
	}
	if out.Central != "alpha" {
		t.Errorf("central: want alpha, got %q", out.Central)
	}
	if len(out.Channels) != 2 {
		t.Fatalf("expected 2 channels, got %d", len(out.Channels))
	}
}

func TestListChannels_UnknownAddress(t *testing.T) {
	devs := newFakeDevices()
	centrals := &fakeCentrals{names: []string{"alpha"}}
	hubs := newFakeHubs()

	cs := connect(t, hubDeps(centrals, hubs, devs))
	defer cs.Close()

	res := callTool(t, cs, "list_channels", map[string]any{"address": "DOES_NOT_EXIST"})
	if res.IsError {
		t.Fatalf("expected graceful not-found, got error: %v", res.Content)
	}

	var out struct {
		Found    bool `json:"found"`
		Channels []struct {
			Address string `json:"address"`
		} `json:"channels"`
	}
	unmarshalStructured(t, res, &out)

	if out.Found {
		t.Fatal("expected Found=false for unknown address")
	}
	if len(out.Channels) != 0 {
		t.Errorf("expected 0 channels for unknown device, got %d", len(out.Channels))
	}
}

// ─── get_device_schedule ────────────────────────────────────────────────────

// TestGetDeviceSchedule_ClimateChannel pins that a thermostat channel's
// attached week-profile projects to schedule_type=climate with its
// profile set, current profile and temperature bounds — the read
// surface this tool exists to close (D2-ws-mcp-surface-2): MCP had no
// way to answer "does this device have a schedule" at all.
func TestGetDeviceSchedule_ClimateChannel(t *testing.T) {
	devs := newFakeDevices()
	dev := device.New(device.Config{
		Address:   "THERM01",
		Model:     "HmIP-eTRV-2",
		Interface: hmenum.InterfaceHmIPRF,
	})
	dev.AddChannel("THERM01:1", 1, "HEATING_CLIMATECONTROL_TRANSCEIVER", hmenum.ParamsetKeyValues)
	dev.Channel("THERM01:1").AttachWeekProfile(weekprofile.NewProfileDataPoint(weekprofile.ProfileDataPointConfig{
		CentralName:    "alpha",
		ChannelAddress: "THERM01:1",
		ScheduleType:   weekprofile.ScheduleTypeClimate,
		ProfileCount:   3,
		MinTemp:        5,
		MaxTemp:        30,
	}))
	devs.add(dev, "alpha")

	centrals := &fakeCentrals{names: []string{"alpha"}}
	hubs := newFakeHubs()
	cs := connect(t, hubDeps(centrals, hubs, devs))
	defer cs.Close()

	res := callTool(t, cs, "get_device_schedule", map[string]any{"address": "THERM01"})
	if res.IsError {
		t.Fatalf("get_device_schedule returned error: %v", res.Content)
	}

	var out struct {
		Found    bool   `json:"found"`
		Central  string `json:"central"`
		Channels []struct {
			ChannelAddress    string   `json:"channel_address"`
			ScheduleType      string   `json:"schedule_type"`
			MaxEntries        int      `json:"max_entries"`
			AvailableProfiles []string `json:"available_profiles"`
			CurrentProfile    string   `json:"current_profile"`
			MinTemp           float64  `json:"min_temp"`
			MaxTemp           float64  `json:"max_temp"`
		} `json:"channels"`
	}
	unmarshalStructured(t, res, &out)

	if !out.Found || out.Central != "alpha" {
		t.Fatalf("found=%v central=%q, want found=true central=alpha", out.Found, out.Central)
	}
	if len(out.Channels) != 1 {
		t.Fatalf("expected 1 scheduled channel, got %d", len(out.Channels))
	}
	ch := out.Channels[0]
	if ch.ChannelAddress != "THERM01:1" || ch.ScheduleType != "climate" {
		t.Fatalf("channel=%+v, want THERM01:1/climate", ch)
	}
	if len(ch.AvailableProfiles) != 3 || ch.CurrentProfile != "P1" {
		t.Fatalf("profiles=%v current=%q, want 3 profiles starting P1", ch.AvailableProfiles, ch.CurrentProfile)
	}
	if ch.MinTemp != 5 || ch.MaxTemp != 30 {
		t.Fatalf("min/max temp=%v/%v, want 5/30", ch.MinTemp, ch.MaxTemp)
	}
}

// TestGetDeviceSchedule_DeviceWithoutSchedule pins the negative case: a
// device with no attached week profile reports Found=true (it exists)
// but an empty channel list, distinguishing "no schedule" from "unknown
// device".
func TestGetDeviceSchedule_DeviceWithoutSchedule(t *testing.T) {
	devs := newFakeDevices()
	dev := device.New(device.Config{
		Address:   "PLAIN01",
		Model:     "HmIP-PS",
		Interface: hmenum.InterfaceHmIPRF,
	})
	dev.AddChannel("PLAIN01:1", 1, "SWITCH", hmenum.ParamsetKeyValues)
	devs.add(dev, "alpha")

	centrals := &fakeCentrals{names: []string{"alpha"}}
	hubs := newFakeHubs()
	cs := connect(t, hubDeps(centrals, hubs, devs))
	defer cs.Close()

	res := callTool(t, cs, "get_device_schedule", map[string]any{"address": "PLAIN01"})
	if res.IsError {
		t.Fatalf("get_device_schedule returned error: %v", res.Content)
	}
	var out struct {
		Found    bool          `json:"found"`
		Channels []interface{} `json:"channels"`
	}
	unmarshalStructured(t, res, &out)
	if !out.Found {
		t.Fatal("expected Found=true for a known device")
	}
	if len(out.Channels) != 0 {
		t.Errorf("expected 0 scheduled channels, got %d", len(out.Channels))
	}
}

// TestGetDeviceSchedule_UnknownAddress mirrors TestListChannels_UnknownAddress.
func TestGetDeviceSchedule_UnknownAddress(t *testing.T) {
	devs := newFakeDevices()
	centrals := &fakeCentrals{names: []string{"alpha"}}
	hubs := newFakeHubs()
	cs := connect(t, hubDeps(centrals, hubs, devs))
	defer cs.Close()

	res := callTool(t, cs, "get_device_schedule", map[string]any{"address": "DOES_NOT_EXIST"})
	if res.IsError {
		t.Fatalf("expected graceful not-found, got error: %v", res.Content)
	}
	var out struct {
		Found bool `json:"found"`
	}
	unmarshalStructured(t, res, &out)
	if out.Found {
		t.Fatal("expected Found=false for unknown address")
	}
}

// ─── list_rooms ───────────────────────────────────────────────────────────────

func TestListRooms_DeviceCountTally(t *testing.T) {
	devs := newFakeDevices()
	d1 := device.New(device.Config{
		Address:   "R001",
		Model:     "HmIP-PS",
		Interface: hmenum.InterfaceHmIPRF,
		Rooms:     []string{"Living Room", "Garage"},
	})
	d2 := device.New(device.Config{
		Address:   "R002",
		Model:     "HmIP-PS",
		Interface: hmenum.InterfaceHmIPRF,
		Rooms:     []string{"Living Room"},
	})
	devs.add(d1, "alpha")
	devs.add(d2, "alpha")

	centrals := &fakeCentrals{names: []string{"alpha"}}
	hubs := newFakeHubs()

	cs := connect(t, hubDeps(centrals, hubs, devs))
	defer cs.Close()

	res := callTool(t, cs, "list_rooms", map[string]any{})
	if res.IsError {
		t.Fatalf("list_rooms returned error: %v", res.Content)
	}

	var out struct {
		Rooms []struct {
			Name        string `json:"name"`
			DeviceCount int    `json:"device_count"`
		} `json:"rooms"`
	}
	unmarshalStructured(t, res, &out)

	byName := map[string]int{}
	for _, r := range out.Rooms {
		byName[r.Name] = r.DeviceCount
	}
	if byName["Living Room"] != 2 {
		t.Errorf("Living Room device_count: want 2, got %d", byName["Living Room"])
	}
	if byName["Garage"] != 1 {
		t.Errorf("Garage device_count: want 1, got %d", byName["Garage"])
	}
}

func TestListRooms_CentralScoping(t *testing.T) {
	// Use a single device in alpha only. With a single entry in the map the
	// two Devices() calls inside countGroups and registerListRooms always
	// return the same [0]-indexed element, making the test deterministic.
	devs := newFakeDevices()
	dAlpha := device.New(device.Config{
		Address:   "RA001",
		Model:     "HmIP-PS",
		Interface: hmenum.InterfaceHmIPRF,
		Rooms:     []string{"Kitchen"},
	})
	devs.add(dAlpha, "alpha")

	centrals := &fakeCentrals{names: []string{"alpha", "beta"}}
	hubs := newFakeHubs()

	cs := connect(t, hubDeps(centrals, hubs, devs))
	defer cs.Close()

	// Scoped to alpha — should see Kitchen.
	res := callTool(t, cs, "list_rooms", map[string]any{"central_name": "alpha"})
	if res.IsError {
		t.Fatalf("list_rooms returned error")
	}

	var out struct {
		Rooms []struct {
			Name        string `json:"name"`
			DeviceCount int    `json:"device_count"`
		} `json:"rooms"`
	}
	unmarshalStructured(t, res, &out)

	if len(out.Rooms) != 1 {
		t.Fatalf("expected 1 room for alpha, got %d", len(out.Rooms))
	}
	if out.Rooms[0].Name != "Kitchen" {
		t.Errorf("room name: want Kitchen, got %q", out.Rooms[0].Name)
	}
	if out.Rooms[0].DeviceCount != 1 {
		t.Errorf("device_count: want 1, got %d", out.Rooms[0].DeviceCount)
	}

	// Scoped to beta — no devices, so no rooms.
	res2 := callTool(t, cs, "list_rooms", map[string]any{"central_name": "beta"})
	if res2.IsError {
		t.Fatalf("list_rooms for beta returned error")
	}
	var out2 struct {
		Rooms []struct{ Name string } `json:"rooms"`
	}
	unmarshalStructured(t, res2, &out2)
	if len(out2.Rooms) != 0 {
		t.Fatalf("expected 0 rooms for beta (no devices), got %d", len(out2.Rooms))
	}
}

// ─── list_functions ───────────────────────────────────────────────────────────

func TestListFunctions_DeviceCountTally(t *testing.T) {
	devs := newFakeDevices()
	d1 := device.New(device.Config{
		Address:   "F001",
		Model:     "HmIP-PS",
		Interface: hmenum.InterfaceHmIPRF,
		Functions: []string{"Lighting", "Security"},
	})
	d2 := device.New(device.Config{
		Address:   "F002",
		Model:     "HmIP-PS",
		Interface: hmenum.InterfaceHmIPRF,
		Functions: []string{"Lighting"},
	})
	devs.add(d1, "alpha")
	devs.add(d2, "alpha")

	centrals := &fakeCentrals{names: []string{"alpha"}}
	hubs := newFakeHubs()

	cs := connect(t, hubDeps(centrals, hubs, devs))
	defer cs.Close()

	res := callTool(t, cs, "list_functions", map[string]any{})
	if res.IsError {
		t.Fatalf("list_functions returned error: %v", res.Content)
	}

	var out struct {
		Functions []struct {
			Name        string `json:"name"`
			DeviceCount int    `json:"device_count"`
		} `json:"functions"`
	}
	unmarshalStructured(t, res, &out)

	byName := map[string]int{}
	for _, f := range out.Functions {
		byName[f.Name] = f.DeviceCount
	}
	if byName["Lighting"] != 2 {
		t.Errorf("Lighting device_count: want 2, got %d", byName["Lighting"])
	}
	if byName["Security"] != 1 {
		t.Errorf("Security device_count: want 1, got %d", byName["Security"])
	}
}

func TestListFunctions_CentralScoping(t *testing.T) {
	// Single device in beta only — avoids the ordering ambiguity in
	// countGroups when two calls to Devices() may return different orderings.
	devs := newFakeDevices()
	dBeta := device.New(device.Config{
		Address:   "FB001",
		Model:     "HmIP-PS",
		Interface: hmenum.InterfaceHmIPRF,
		Functions: []string{"Lighting"},
	})
	devs.add(dBeta, "beta")

	centrals := &fakeCentrals{names: []string{"alpha", "beta"}}
	hubs := newFakeHubs()

	cs := connect(t, hubDeps(centrals, hubs, devs))
	defer cs.Close()

	// Scoped to beta — should see Lighting.
	res := callTool(t, cs, "list_functions", map[string]any{"central_name": "beta"})
	if res.IsError {
		t.Fatalf("list_functions returned error")
	}

	var out struct {
		Functions []struct {
			Name        string `json:"name"`
			DeviceCount int    `json:"device_count"`
		} `json:"functions"`
	}
	unmarshalStructured(t, res, &out)

	if len(out.Functions) != 1 {
		t.Fatalf("expected 1 function for beta, got %d", len(out.Functions))
	}
	if out.Functions[0].Name != "Lighting" {
		t.Errorf("function name: want Lighting, got %q", out.Functions[0].Name)
	}
	if out.Functions[0].DeviceCount != 1 {
		t.Errorf("device_count: want 1, got %d", out.Functions[0].DeviceCount)
	}

	// Scoped to alpha — no devices registered, so no functions.
	res2 := callTool(t, cs, "list_functions", map[string]any{"central_name": "alpha"})
	if res2.IsError {
		t.Fatalf("list_functions for alpha returned error")
	}
	var out2 struct {
		Functions []struct{ Name string } `json:"functions"`
	}
	unmarshalStructured(t, res2, &out2)
	if len(out2.Functions) != 0 {
		t.Fatalf("expected 0 functions for alpha (no devices), got %d", len(out2.Functions))
	}
}

// TestListRoomsAndFunctions_UnknownCentralReturnsError pins the fix for
// list_rooms and list_functions reach the model through
// countGroups rather than centralsToScan, so they had not inherited the
// unknown-central check the sibling tools got. A central_name that names
// no configured central must surface as an error, not a well-formed empty
// list an agent would report as "this CCU has no rooms" — indistinguishable
// from a central that genuinely has none.
func TestListRoomsAndFunctions_UnknownCentralReturnsError(t *testing.T) {
	tools := []string{"list_rooms", "list_functions"}
	for _, tool := range tools {
		t.Run(tool, func(t *testing.T) {
			devs := newFakeDevices()
			centrals := &fakeCentrals{names: []string{"alpha"}}
			hubs := newFakeHubs()

			cs := connect(t, hubDeps(centrals, hubs, devs))
			defer cs.Close()

			res := callTool(t, cs, tool, map[string]any{"central_name": "ghost"})
			if !res.IsError {
				t.Fatalf("%s: expected IsError=true for an unknown central_name", tool)
			}
		})
	}
}
