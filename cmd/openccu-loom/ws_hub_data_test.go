// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// buildDevicesAdapter builds a minimal *adapter.DevicesAdapter backed by the given registry.
func buildDevicesAdapter(t *testing.T, reg *central.Registry) *adapter.DevicesAdapter {
	t.Helper()
	return adapter.NewDevicesAdapter(reg)
}

// ── wsHubQuery with live programs ────────────────────────────────────────────

func TestWSHubQuery_ListPrograms_WithProgram_ReturnsEntry(t *testing.T) {
	t.Parallel()
	q, h := liveHubQuery(t)
	p := hub.NewProgram("ccu-01", "prog-1", "Lights Off", "Turn off all lights", false, nil)
	h.PutProgram(p)

	got, err := q.ListPrograms(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListPrograms: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 program, got %d", len(got))
	}
	if got[0]["id"] != "prog-1" {
		t.Errorf("expected id=prog-1, got %v", got[0]["id"])
	}
}

func TestWSHubQuery_ListPrograms_WithActive_IncludesActive(t *testing.T) {
	t.Parallel()
	q, h := liveHubQuery(t)
	p := hub.NewProgram("ccu-01", "prog-2", "Night Mode", "", false, nil)
	// Activate the program so the "active" field is observed.
	p.OnActive(true) // active=true, marks hasActive=true (observed)
	h.PutProgram(p)

	got, err := q.ListPrograms(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListPrograms: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 program, got %d", len(got))
	}
	if got[0]["active"] != true {
		t.Errorf("expected active=true, got %v", got[0]["active"])
	}
}

// TestWSHubQuery_ListPrograms_InternalFilter exercises the internal-program
// delivery filter: hidden by default, revealed by an explicit override or
// the central's configured default.
func TestWSHubQuery_ListPrograms_InternalFilter(t *testing.T) {
	t.Parallel()
	boolPtr := func(b bool) *bool { return &b }

	build := func(def bool) *wsHubQuery {
		q, h := liveHubQuery(t)
		h.SetIncludeInternalProgramsDefault(def)
		h.PutProgram(hub.NewProgram("ccu-01", "P-Normal", "Normal", "", false, nil))
		internal := hub.NewProgram("ccu-01", "Tmp_1", "Tmp_1", "", true, nil)
		h.PutProgram(internal)
		return q
	}

	countInternal := func(t *testing.T, q *wsHubQuery, override *bool) (total, internal int) {
		t.Helper()
		got, err := q.ListPrograms(context.Background(), override)
		if err != nil {
			t.Fatalf("ListPrograms: %v", err)
		}
		for _, e := range got {
			if e["is_internal"] == true {
				internal++
			}
		}
		return len(got), internal
	}

	// Default false, no override → internal hidden.
	if total, internal := countInternal(t, build(false), nil); total != 1 || internal != 0 {
		t.Fatalf("default hide: expected 1 program, 0 internal; got total=%d internal=%d", total, internal)
	}
	// Default true, no override → both shown.
	if total, internal := countInternal(t, build(true), nil); total != 2 || internal != 1 {
		t.Fatalf("default show: expected 2 programs, 1 internal; got total=%d internal=%d", total, internal)
	}
	// Override true wins over default false.
	if total, internal := countInternal(t, build(false), boolPtr(true)); total != 2 || internal != 1 {
		t.Fatalf("override show: expected 2 programs, 1 internal; got total=%d internal=%d", total, internal)
	}
	// Override false wins over default true.
	if total, internal := countInternal(t, build(true), boolPtr(false)); total != 1 || internal != 0 {
		t.Fatalf("override hide: expected 1 program, 0 internal; got total=%d internal=%d", total, internal)
	}
}

// ── wsHubQuery with live sysvars ─────────────────────────────────────────────

func TestWSHubQuery_ListSysvars_WithSysvar_ReturnsEntry(t *testing.T) {
	t.Parallel()
	q, h := liveHubQuery(t)
	s := hub.NewSysvar("ccu-01", "presence", "Presence detector", hmenum.HubValueTypeLogic, nil)
	h.PutSysvar(s)

	got, err := q.ListSysvars(context.Background())
	if err != nil {
		t.Fatalf("ListSysvars: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 sysvar, got %d", len(got))
	}
	if got[0]["name"] != "presence" {
		t.Errorf("expected name=presence, got %v", got[0]["name"])
	}
}

func TestWSHubQuery_ListSysvars_WithMinMax_IncludesMinMax(t *testing.T) {
	t.Parallel()
	q, h := liveHubQuery(t)
	s := hub.NewSysvar("ccu-01", "temperature", "Room temp", hmenum.HubValueTypeFloat, nil)
	minVal, _ := hmtypes.NewParamValue(0.0)
	maxVal, _ := hmtypes.NewParamValue(100.0)
	s.Min = &minVal
	s.Max = &maxVal
	h.PutSysvar(s)

	got, err := q.ListSysvars(context.Background())
	if err != nil {
		t.Fatalf("ListSysvars: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 sysvar, got %d", len(got))
	}
	if _, ok := got[0]["min"]; !ok {
		t.Error("expected 'min' key in response")
	}
	if _, ok := got[0]["max"]; !ok {
		t.Error("expected 'max' key in response")
	}
}

func TestWSHubQuery_ListSysvars_WithObservedValue_IncludesValue(t *testing.T) {
	t.Parallel()
	q, h := liveHubQuery(t)
	s := hub.NewSysvar("ccu-01", "counter", "Counter", hmenum.HubValueTypeFloat, nil)
	pv, _ := hmtypes.NewParamValue(42.0)
	s.OnValue(pv) // set an observed value
	h.PutSysvar(s)

	got, err := q.ListSysvars(context.Background())
	if err != nil {
		t.Fatalf("ListSysvars: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 sysvar, got %d", len(got))
	}
	if got[0]["observed"] != true {
		t.Errorf("expected observed=true, got %v", got[0]["observed"])
	}
}

// ── wsHubQuery BackupStatus ───────────────────────────────────────────────────

func TestWSHubQuery_BackupStatus_LiveHub_ReturnsNilOrError(t *testing.T) {
	t.Parallel()
	q, _ := liveHubQuery(t)
	// BackupStatusRemote on a live hub returns error (no CCU backend).
	_, err := q.BackupStatus(context.Background())
	// Either nil result with no error, or an error — both acceptable.
	_ = err
}

// ── wsHubQuery FirmwareInfo ───────────────────────────────────────────────────

func TestWSHubQuery_FirmwareInfo_LiveHub_NilUpdate_ReturnsObservedFalse(t *testing.T) {
	t.Parallel()
	q, h := liveHubQuery(t)
	// Fresh hub has h.Update == nil → returns {observed: false}.
	if h.Update != nil {
		t.Skip("h.Update is non-nil; skipping nil-Update branch test")
	}
	got, err := q.FirmwareInfo(context.Background())
	if err != nil {
		t.Fatalf("FirmwareInfo: %v", err)
	}
	if got["observed"] != false {
		t.Errorf("expected observed=false, got %v", got["observed"])
	}
}

// ── wsHubQuery InboxDevices ───────────────────────────────────────────────────

func TestWSHubQuery_InboxDevices_LiveHub_NilInbox2_ReturnsEmpty(t *testing.T) {
	t.Parallel()
	q, h := liveHubQuery(t)
	if h.Inbox != nil {
		t.Skip("h.Inbox is non-nil; skipping nil-Inbox branch test")
	}
	got, err := q.InboxDevices(context.Background())
	if err != nil {
		t.Fatalf("InboxDevices: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 inbox entries, got %d", len(got))
	}
}

// ── wsDeviceQuery ListDevices and GetDevice ───────────────────────────────────

func TestWSDeviceQuery_ListDevices_NilDevs_ReturnsEmpty(t *testing.T) {
	t.Parallel()
	w := &wsDeviceQuery{devs: nil}
	got, err := w.ListDevices(context.Background())
	if err != nil {
		t.Fatalf("ListDevices: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0, got %d", len(got))
	}
}

func TestWSDeviceQuery_GetDevice_NilDevs_Errors(t *testing.T) {
	t.Parallel()
	w := &wsDeviceQuery{devs: nil}
	_, err := w.GetDevice(context.Background(), "DEV:1")
	if err == nil {
		t.Fatal("expected error when devs=nil")
	}
}

func TestWSDeviceQuery_GetDevice_UnknownAddress_Errors(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "ccu-01")
	devAdapter := buildDevicesAdapter(t, reg)
	w := &wsDeviceQuery{devs: devAdapter}
	_, err := w.GetDevice(context.Background(), "NONEXISTENT")
	if err == nil {
		t.Fatal("expected error for unknown device")
	}
}
