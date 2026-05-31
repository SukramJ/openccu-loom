// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// testInterfaceIndex is an InterfaceIndex stub for snapshot tests.
type testInterfaceIndex struct {
	ifaces []InterfaceState
}

func (ti *testInterfaceIndex) Interfaces() []InterfaceState { return ti.ifaces }
func (ti *testInterfaceIndex) Interface(id string) (InterfaceState, bool) {
	for _, i := range ti.ifaces {
		if i.ID == id {
			return i, true
		}
	}
	return InterfaceState{}, false
}
func (ti *testInterfaceIndex) Reconnect(_ context.Context, _ string) error { return nil }

func TestSnapshot_EmptyDeps_Returns200(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/snapshot", http.NoBody)
	w := httptest.NewRecorder()
	Snapshot(SnapshotDeps{}).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var env SnapshotEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if env.GeneratedAt == "" {
		t.Error("generated_at must not be empty")
	}
}

func TestSnapshot_WithDevicesAndHub_Returns200(t *testing.T) {
	t.Parallel()
	d := device.New(device.Config{
		Address:     "0001ABCD",
		Model:       "HmIP-BSM",
		Interface:   hmenum.InterfaceHmIPRF,
		InterfaceID: "HmIP-RF@ccu01",
		Name:        "My Switch",
	})
	d.Rooms = []string{"Office"}
	d.Functions = []string{"Lighting"}
	idx := &stubDeviceIndex{devices: map[string]*device.Device{"0001ABCD": d}}

	h := hub.NewHub("ccu01")
	h.PutProgram(&hub.Program{HubDataPoint: hub.HubDataPoint{Name: "Morning"}, ID: "P1"})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/snapshot", http.NoBody)
	w := httptest.NewRecorder()
	Snapshot(SnapshotDeps{
		Devices: idx,
		Hub:     &testHubIndex{h: h},
	}).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var env SnapshotEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(env.Devices) != 1 {
		t.Fatalf("expected 1 device, got %d", len(env.Devices))
	}
	if len(env.Programs) != 1 {
		t.Fatalf("expected 1 program, got %d", len(env.Programs))
	}
	if len(env.Rooms) != 1 || env.Rooms[0].Name != "Office" {
		t.Fatalf("unexpected rooms: %+v", env.Rooms)
	}
	if len(env.Functions) != 1 || env.Functions[0].Name != "Lighting" {
		t.Fatalf("unexpected functions: %+v", env.Functions)
	}
}

func TestSnapshot_WithInterfaceIndex(t *testing.T) {
	t.Parallel()
	ifaceIdx := &testInterfaceIndex{
		ifaces: []InterfaceState{
			{ID: "HmIP-RF", Name: "HmIP RF", Connected: true, Interface: "HmIP-RF"},
			{ID: "BidCos-RF", Name: "BidCos RF", Connected: false, Interface: "BidCos-RF"},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/snapshot", http.NoBody)
	w := httptest.NewRecorder()
	Snapshot(SnapshotDeps{Interfaces: ifaceIdx}).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var env SnapshotEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(env.Interfaces) != 2 {
		t.Fatalf("expected 2 interfaces, got %d", len(env.Interfaces))
	}
	// Sorted alphabetically by ID.
	if env.Interfaces[0].ID != "BidCos-RF" {
		t.Errorf("expected BidCos-RF first after sort, got %q", env.Interfaces[0].ID)
	}
}

func TestSnapshot_Anonymise_QueryParam(t *testing.T) {
	t.Parallel()
	d := device.New(device.Config{
		Address:     "0001ABCD",
		Model:       "HmIP-BSM",
		Interface:   hmenum.InterfaceHmIPRF,
		InterfaceID: "HmIP-RF@ccu01",
		Name:        "My Private Switch",
	})
	d.Rooms = []string{"Bedroom"}
	idx := &stubDeviceIndex{devices: map[string]*device.Device{"0001ABCD": d}}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/snapshot?anonymize=1", http.NoBody)
	w := httptest.NewRecorder()
	Snapshot(SnapshotDeps{Devices: idx}).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var env SnapshotEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(env.Devices) != 1 {
		t.Fatalf("expected 1 device")
	}
	if env.Devices[0].Name == "My Private Switch" {
		t.Error("device name must be anonymised, but original name was returned")
	}
	if len(env.Rooms) == 1 && env.Rooms[0].Name == "Bedroom" {
		t.Error("room name must be anonymised, but original name was returned")
	}
}

func TestSnapshot_AnonymiseUKSpelling(t *testing.T) {
	t.Parallel()
	d := device.New(device.Config{
		Address:     "0001ABCD",
		Model:       "HmIP-BSM",
		Interface:   hmenum.InterfaceHmIPRF,
		InterfaceID: "HmIP-RF@ccu01",
		Name:        "Private",
	})
	idx := &stubDeviceIndex{devices: map[string]*device.Device{"0001ABCD": d}}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/snapshot?anonymise=true", http.NoBody)
	w := httptest.NewRecorder()
	Snapshot(SnapshotDeps{Devices: idx}).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var env SnapshotEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(env.Devices) == 1 && env.Devices[0].Name == "Private" {
		t.Error("UK-spelling anonymise=true must also anonymise names")
	}
}

func TestSnapshot_AnonymisePrograms(t *testing.T) {
	t.Parallel()
	h := hub.NewHub("ccu01")
	h.PutProgram(&hub.Program{
		HubDataPoint: hub.HubDataPoint{Name: "My Secret Program", Description: "secret info"},
		ID:           "P1",
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/snapshot?anonymize=1", http.NoBody)
	w := httptest.NewRecorder()
	Snapshot(SnapshotDeps{Hub: &testHubIndex{h: h}}).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var env SnapshotEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(env.Programs) != 1 {
		t.Fatalf("expected 1 program")
	}
	if env.Programs[0].Name == "My Secret Program" {
		t.Error("program name must be anonymised")
	}
	if env.Programs[0].Description != "" {
		t.Error("program description must be cleared by anonymisation")
	}
}

func TestSnapshot_AnonymiseSysvars(t *testing.T) {
	t.Parallel()
	h := hub.NewHub("ccu01")
	h.PutSysvar(hub.NewSysvar("ccu01", "SecretFlag", "", hmenum.HubValueTypeLogic, nil))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/snapshot?anonymize=1", http.NoBody)
	w := httptest.NewRecorder()
	Snapshot(SnapshotDeps{Hub: &testHubIndex{h: h}}).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var env SnapshotEnvelope
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	if len(env.Sysvars) == 1 && env.Sysvars[0].Name == "SecretFlag" {
		t.Error("sysvar name must be anonymised")
	}
}

func TestSnapshot_WithSysvars(t *testing.T) {
	t.Parallel()
	h := hub.NewHub("ccu01")
	h.PutSysvar(hub.NewSysvar("ccu01", "Flag", "", hmenum.HubValueTypeLogic, nil))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/snapshot", http.NoBody)
	w := httptest.NewRecorder()
	Snapshot(SnapshotDeps{Hub: &testHubIndex{h: h}}).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var env SnapshotEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(env.Sysvars) != 1 {
		t.Fatalf("expected 1 sysvar, got %d", len(env.Sysvars))
	}
}

// --- wantsAnonymise unit tests ---

func TestWantsAnonymise_TrueValues(t *testing.T) {
	t.Parallel()
	for _, v := range []string{"1", "true", "True", "TRUE", "yes", "Yes"} {
		req := httptest.NewRequest(http.MethodGet, "/snap?anonymize="+v, http.NoBody)
		if !wantsAnonymise(req) {
			t.Errorf("wantsAnonymise should be true for anonymize=%s", v)
		}
	}
}

func TestWantsAnonymise_FalseValues(t *testing.T) {
	t.Parallel()
	for _, v := range []string{"0", "false", "no", ""} {
		req := httptest.NewRequest(http.MethodGet, "/snap?anonymize="+v, http.NoBody)
		if wantsAnonymise(req) {
			t.Errorf("wantsAnonymise should be false for anonymize=%q", v)
		}
	}
}

func TestWantsAnonymise_NilRequest(t *testing.T) {
	t.Parallel()
	if wantsAnonymise(nil) {
		t.Error("wantsAnonymise(nil) must return false")
	}
}

func TestWantsAnonymise_UKSpellings(t *testing.T) {
	t.Parallel()
	for _, kv := range []string{"anonymize=Yes", "anonymise=yes"} {
		req := httptest.NewRequest(http.MethodGet, "/snap?"+kv, http.NoBody)
		if !wantsAnonymise(req) {
			t.Errorf("wantsAnonymise should be true for %s", kv)
		}
	}
}

// --- anonToken tests ---

func TestAnonToken_EmptyInput_ReturnsEmpty(t *testing.T) {
	t.Parallel()
	if got := anonToken("device", ""); got != "" {
		t.Fatalf("expected empty for empty value, got %q", got)
	}
}

func TestAnonToken_StableHash(t *testing.T) {
	t.Parallel()
	a := anonToken("room", "Bedroom")
	b := anonToken("room", "Bedroom")
	if a != b {
		t.Fatalf("anonToken must be deterministic: %q != %q", a, b)
	}
}

func TestAnonToken_DifferentKindsDifferentTokens(t *testing.T) {
	t.Parallel()
	a := anonToken("device", "foo")
	b := anonToken("sysvar", "foo")
	if a == b {
		t.Fatalf("same value under different kinds should yield different tokens, got %q", a)
	}
}

// --- snapshotPrograms with nil hub ---

func TestSnapshotPrograms_NilHub(t *testing.T) {
	t.Parallel()
	idx := &testHubIndex{h: nil}
	out := snapshotPrograms(idx)
	if out != nil {
		t.Fatalf("expected nil slice for nil hub, got %+v", out)
	}
}

// --- snapshotSysvars with nil hub ---

func TestSnapshotSysvars_NilHub(t *testing.T) {
	t.Parallel()
	idx := &testHubIndex{h: nil}
	out := snapshotSysvars(idx)
	if out != nil {
		t.Fatalf("expected nil slice for nil hub, got %+v", out)
	}
}

// --- snapshotRooms and snapshotFunctions ---

func TestSnapshotRooms_MultipleDevices(t *testing.T) {
	t.Parallel()
	d1 := newTestDevice("A1", "M1")
	d1.Rooms = []string{"Kitchen", "Hall"}
	d2 := newTestDevice("A2", "M2")
	d2.Rooms = []string{"Kitchen"}
	idx := &stubDeviceIndex{devices: map[string]*device.Device{"A1": d1, "A2": d2}}
	rooms := snapshotRooms(idx)
	if len(rooms) != 2 {
		t.Fatalf("expected 2 rooms, got %d", len(rooms))
	}
	for _, r := range rooms {
		if r.Name == "Kitchen" && r.DeviceCount != 2 {
			t.Errorf("Kitchen should have 2 devices, got %d", r.DeviceCount)
		}
	}
}

func TestSnapshotFunctions_MultipleDevices(t *testing.T) {
	t.Parallel()
	d1 := newTestDevice("A1", "M1")
	d1.Functions = []string{"Lighting"}
	d2 := newTestDevice("A2", "M2")
	d2.Functions = []string{"Lighting", "Security"}
	idx := &stubDeviceIndex{devices: map[string]*device.Device{"A1": d1, "A2": d2}}
	fns := snapshotFunctions(idx)
	if len(fns) != 2 {
		t.Fatalf("expected 2 functions, got %d", len(fns))
	}
}
