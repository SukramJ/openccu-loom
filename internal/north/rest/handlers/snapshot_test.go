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
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
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

// TestSnapshotPrograms_RuleSummary verifies snapshotPrograms surfaces the
// condition and activity rule summaries recorded on the hub program,
// mirroring the same mapping ListPrograms applies via toProgramSummary.
func TestSnapshotPrograms_RuleSummary(t *testing.T) {
	t.Parallel()
	h := hub.NewHub("test-ccu")
	prog := hub.NewProgram("test-ccu", "P-RULE", "Heater", "", false, nil)
	prog.SetRuleSummary("Wohnzimmer >= 20.00", "Bücherregal := 1.00")
	h.PutProgram(prog)
	idx := &testHubIndex{h: h}

	out := snapshotPrograms(idx)
	if len(out) != 1 {
		t.Fatalf("expected 1 program, got %d", len(out))
	}
	if out[0].ConditionSummary != "Wohnzimmer >= 20.00" {
		t.Errorf("ConditionSummary = %q, want %q", out[0].ConditionSummary, "Wohnzimmer >= 20.00")
	}
	if out[0].ActivitySummary != "Bücherregal := 1.00" {
		t.Errorf("ActivitySummary = %q, want %q", out[0].ActivitySummary, "Bücherregal := 1.00")
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

// --- ?include= nested channels / data_points ---

// newSnapshotDevice builds a device at addr with one channel (number 1, type
// chType) and one BinarySensor DP (STATE). Kind is set so Category() returns
// DataPointCategoryBinarySensor, matching the production ingest pipeline.
func newSnapshotDevice(addr, chType string) *device.Device {
	d := newTestDevice(addr, "HmIP-TEST")
	chAddr := addr + ":1"
	ch := d.AddChannel(chAddr, 1, chType, hmenum.ParamsetKeyValues)
	dp := generic.NewBinarySensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: chAddr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterState),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
		Kind: generic.KindBinarySensor,
	})
	ch.Put(dp)
	return d
}

// TestSnapshot_NoInclude_DeviceChannelsAbsent pins the omitempty contract:
// without ?include= the device_channels field must be absent from the JSON.
func TestSnapshot_NoInclude_DeviceChannelsAbsent(t *testing.T) {
	t.Parallel()
	d := newSnapshotDevice("0001ABCD", "SWITCH")
	idx := &stubDeviceIndex{devices: map[string]*device.Device{"0001ABCD": d}}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/snapshot", http.NoBody)
	w := httptest.NewRecorder()
	Snapshot(SnapshotDeps{Devices: idx}).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var env SnapshotEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(env.DeviceChannels) != 0 {
		t.Errorf("device_channels must be absent without ?include=, got %d entries", len(env.DeviceChannels))
	}
	if len(env.Devices) != 1 {
		t.Errorf("flat devices list must still contain 1 entry, got %d", len(env.Devices))
	}
}

// TestSnapshot_IncludeChannels_ChannelsPopulated verifies that ?include=channels
// nests each device's channels but leaves data_points empty.
func TestSnapshot_IncludeChannels_ChannelsPopulated(t *testing.T) {
	t.Parallel()
	d := newSnapshotDevice("0001ABCD", "SWITCH")
	idx := &stubDeviceIndex{devices: map[string]*device.Device{"0001ABCD": d}}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/snapshot?include=channels", http.NoBody)
	w := httptest.NewRecorder()
	Snapshot(SnapshotDeps{Devices: idx}).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var env SnapshotEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(env.DeviceChannels) != 1 {
		t.Fatalf("expected 1 device_channels entry, got %d", len(env.DeviceChannels))
	}
	dc := env.DeviceChannels[0]
	if dc.DeviceAddress != "0001ABCD" {
		t.Errorf("device_address = %q, want 0001ABCD", dc.DeviceAddress)
	}
	if len(dc.Channels) != 1 {
		t.Fatalf("expected 1 channel, got %d", len(dc.Channels))
	}
	if len(dc.Channels[0].DataPoints) != 0 {
		t.Errorf("channels-only include must not expand data_points, got %d", len(dc.Channels[0].DataPoints))
	}
}

// TestSnapshot_IncludeChannelsAndDataPoints_DataPointsExpanded verifies that
// ?include=channels,data_points nests data points with category populated.
func TestSnapshot_IncludeChannelsAndDataPoints_DataPointsExpanded(t *testing.T) {
	t.Parallel()
	d := newSnapshotDevice("0001ABCD", "SWITCH")
	idx := &stubDeviceIndex{devices: map[string]*device.Device{"0001ABCD": d}}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/snapshot?include=channels,data_points", http.NoBody)
	w := httptest.NewRecorder()
	Snapshot(SnapshotDeps{Devices: idx}).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var env SnapshotEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(env.DeviceChannels) != 1 {
		t.Fatalf("expected 1 device_channels entry, got %d", len(env.DeviceChannels))
	}
	ch := env.DeviceChannels[0].Channels[0]
	if len(ch.DataPoints) != 1 {
		t.Fatalf("expected 1 data_point, got %d", len(ch.DataPoints))
	}
	dp := ch.DataPoints[0]
	if dp.Category == "" {
		t.Error("data_point.category must not be empty when KindBinarySensor DP is present")
	}
}

// TestSnapshot_DataPointsAloneImpliesChannels verifies that ?include=data_points
// (without explicitly listing channels) still populates device_channels.
func TestSnapshot_DataPointsAloneImpliesChannels(t *testing.T) {
	t.Parallel()
	d := newSnapshotDevice("0001ABCD", "SWITCH")
	idx := &stubDeviceIndex{devices: map[string]*device.Device{"0001ABCD": d}}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/snapshot?include=data_points", http.NoBody)
	w := httptest.NewRecorder()
	Snapshot(SnapshotDeps{Devices: idx}).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var env SnapshotEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(env.DeviceChannels) == 0 {
		t.Error("?include=data_points must imply channels and populate device_channels")
	}
	if len(env.DeviceChannels[0].Channels) == 0 {
		t.Error("nested channels must be present when data_points include is requested")
	}
	if len(env.DeviceChannels[0].Channels[0].DataPoints) == 0 {
		t.Error("data_points must be expanded when ?include=data_points is requested")
	}
}

// TestSnapshot_NDJSON_WithInclude verifies that the NDJSON stream emits
// kind:"channel" and kind:"data_point" lines when ?include=channels,data_points
// is requested, and that each carries the correct parent coordinate.
func TestSnapshot_NDJSON_WithInclude(t *testing.T) {
	t.Parallel()
	d := newSnapshotDevice("0001ABCD", "SWITCH")
	idx := &stubDeviceIndex{devices: map[string]*device.Device{"0001ABCD": d}}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/snapshot?include=channels,data_points", http.NoBody)
	req.Header.Set("Accept", "application/x-ndjson")
	w := httptest.NewRecorder()
	Snapshot(SnapshotDeps{Devices: idx}).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	lines := splitNDJSON(t, w.Body.Bytes())

	var channelLine, dpLine map[string]any
	for _, l := range lines {
		switch l["kind"] {
		case "channel":
			channelLine = l
		case "data_point":
			dpLine = l
		}
	}
	if channelLine == nil {
		t.Fatal("no kind:channel line found in NDJSON stream")
	}
	if dpLine == nil {
		t.Fatal("no kind:data_point line found in NDJSON stream")
	}

	// Channel line must carry device_address.
	chData, _ := channelLine["data"].(map[string]any)
	if chData["device_address"] == "" || chData["device_address"] == nil {
		t.Errorf("channel line must carry device_address, got %v", chData["device_address"])
	}

	// Data-point line must carry channel_address.
	dpData, _ := dpLine["data"].(map[string]any)
	if dpData["channel_address"] == "" || dpData["channel_address"] == nil {
		t.Errorf("data_point line must carry channel_address, got %v", dpData["channel_address"])
	}
}

// --- Snapshot central filter ---

// multiCentralSnapshotIndex is a DeviceIndex stub that maps device addresses
// to named centrals, used by multi-CCU snapshot filter tests.
type multiCentralSnapshotIndex struct {
	devices  map[string]*device.Device
	centrals map[string]string
}

func (m *multiCentralSnapshotIndex) Devices() []*device.Device {
	out := make([]*device.Device, 0, len(m.devices))
	for _, d := range m.devices {
		out = append(out, d)
	}
	return out
}

func (m *multiCentralSnapshotIndex) Device(address string) (*device.Device, bool) {
	d, ok := m.devices[address]
	return d, ok
}

func (m *multiCentralSnapshotIndex) CentralOf(address string) string {
	return m.centrals[address]
}

func (m *multiCentralSnapshotIndex) SerialSuffix(central string) string {
	if central != "" {
		return "vccu0000000"
	}
	return ""
}

// TestSnapshot_CentralScope verifies that ?central=<name> scopes devices (and
// device_channels when included) plus the hub entities (programs, sysvars,
// which carry their owning central) to the named central, while rooms and
// functions remain fleet-wide because the model does not tag them by central.
func TestSnapshot_CentralScope(t *testing.T) {
	t.Parallel()

	homeAddr := "HOME0001"
	officeAddr := "OFFC0002"

	dHome := newSnapshotDevice(homeAddr, "SWITCH")
	dHome.Rooms = []string{"Living Room"}
	dHome.Functions = []string{"Lighting"}

	dOffice := newSnapshotDevice(officeAddr, "SWITCH")
	dOffice.Rooms = []string{"Meeting Room"}
	dOffice.Functions = []string{"Lighting"}

	devIdx := &multiCentralSnapshotIndex{
		devices: map[string]*device.Device{
			homeAddr:   dHome,
			officeAddr: dOffice,
		},
		centrals: map[string]string{
			homeAddr:   "home",
			officeAddr: "office",
		},
	}

	h := hub.NewHub("home")
	h.PutProgram(hub.NewProgram("home", "P1", "Morning", "", false, nil))
	h.PutSysvar(hub.NewSysvar("home", "Flag", "", hmenum.HubValueTypeLogic, nil))
	hubIdx := &testHubIndex{h: h}

	t.Run("central=home scopes devices", func(t *testing.T) {
		t.Parallel()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/snapshot?central=home", http.NoBody)
		w := httptest.NewRecorder()
		Snapshot(SnapshotDeps{Devices: devIdx, Hub: hubIdx}).ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
		}
		var env SnapshotEnvelope
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if len(env.Devices) != 1 {
			t.Fatalf("expected 1 device for central=home, got %d", len(env.Devices))
		}
		if env.Devices[0].Address != homeAddr {
			t.Errorf("expected home device %q, got %q", homeAddr, env.Devices[0].Address)
		}
	})

	t.Run("central=office scopes devices to office only", func(t *testing.T) {
		t.Parallel()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/snapshot?central=office", http.NoBody)
		w := httptest.NewRecorder()
		Snapshot(SnapshotDeps{Devices: devIdx}).ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
		var env SnapshotEnvelope
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if len(env.Devices) != 1 {
			t.Fatalf("expected 1 device for central=office, got %d", len(env.Devices))
		}
		if env.Devices[0].Address != officeAddr {
			t.Errorf("expected office device %q, got %q", officeAddr, env.Devices[0].Address)
		}
	})

	t.Run("no central returns all devices", func(t *testing.T) {
		t.Parallel()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/snapshot", http.NoBody)
		w := httptest.NewRecorder()
		Snapshot(SnapshotDeps{Devices: devIdx}).ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
		var env SnapshotEnvelope
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if len(env.Devices) != 2 {
			t.Fatalf("expected 2 devices without central filter, got %d", len(env.Devices))
		}
	})

	t.Run("rooms and functions are fleet-wide regardless of central", func(t *testing.T) {
		t.Parallel()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/snapshot?central=home", http.NoBody)
		w := httptest.NewRecorder()
		Snapshot(SnapshotDeps{Devices: devIdx}).ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
		var env SnapshotEnvelope
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		// Rooms and functions are not scoped by central; both devices contribute.
		if len(env.Rooms) != 2 {
			t.Errorf("expected 2 rooms (fleet-wide), got %d: %+v", len(env.Rooms), env.Rooms)
		}
		if len(env.Functions) != 1 {
			t.Errorf("expected 1 function (both devices share Lighting), got %d: %+v", len(env.Functions), env.Functions)
		}
	})

	t.Run("central=home with include=channels scopes device_channels", func(t *testing.T) {
		t.Parallel()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/snapshot?central=home&include=channels", http.NoBody)
		w := httptest.NewRecorder()
		Snapshot(SnapshotDeps{Devices: devIdx}).ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
		var env SnapshotEnvelope
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if len(env.DeviceChannels) != 1 {
			t.Fatalf("expected 1 device_channels entry for central=home, got %d", len(env.DeviceChannels))
		}
		if env.DeviceChannels[0].DeviceAddress != homeAddr {
			t.Errorf("expected device_channels[0] for %q, got %q", homeAddr, env.DeviceChannels[0].DeviceAddress)
		}
	})

	t.Run("central=home keeps matching programs and sysvars", func(t *testing.T) {
		t.Parallel()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/snapshot?central=home", http.NoBody)
		w := httptest.NewRecorder()
		Snapshot(SnapshotDeps{Hub: hubIdx}).ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
		var env SnapshotEnvelope
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		// Programs / sysvars carry their owning central, so a matching
		// filter keeps them (one home program + one home sysvar).
		if len(env.Programs) != 1 {
			t.Errorf("expected 1 program for central=home, got %d", len(env.Programs))
		}
		if len(env.Sysvars) != 1 {
			t.Errorf("expected 1 sysvar for central=home, got %d", len(env.Sysvars))
		}
	})

	t.Run("non-matching central drops all programs and sysvars", func(t *testing.T) {
		t.Parallel()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/snapshot?central=office", http.NoBody)
		w := httptest.NewRecorder()
		Snapshot(SnapshotDeps{Hub: hubIdx}).ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
		var env SnapshotEnvelope
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if len(env.Programs) != 0 {
			t.Errorf("expected 0 programs for central=office, got %d", len(env.Programs))
		}
		if len(env.Sysvars) != 0 {
			t.Errorf("expected 0 sysvars for central=office, got %d", len(env.Sysvars))
		}
	})

	t.Run("empty central returns all programs and sysvars", func(t *testing.T) {
		t.Parallel()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/snapshot", http.NoBody)
		w := httptest.NewRecorder()
		Snapshot(SnapshotDeps{Hub: hubIdx}).ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
		var env SnapshotEnvelope
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if len(env.Programs) != 1 {
			t.Fatalf("expected 1 program for no central filter, got %d", len(env.Programs))
		}
		if len(env.Sysvars) != 1 {
			t.Fatalf("expected 1 sysvar for no central filter, got %d", len(env.Sysvars))
		}
	})
}

// TestSnapshot_Anonymise_NestedChannelNames verifies that ?anonymize=1 with
// ?include=channels tokenises channel names while leaving addresses intact.
func TestSnapshot_Anonymise_NestedChannelNames(t *testing.T) {
	t.Parallel()
	d := newTestDevice("0001ABCD", "HmIP-TEST")
	ch := d.AddChannel("0001ABCD:1", 1, "SWITCH", hmenum.ParamsetKeyValues)
	ch.Name = "Bookshelf Lamp"

	idx := &stubDeviceIndex{devices: map[string]*device.Device{"0001ABCD": d}}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/snapshot?include=channels&anonymize=1", http.NoBody)
	w := httptest.NewRecorder()
	Snapshot(SnapshotDeps{Devices: idx}).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var env SnapshotEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(env.DeviceChannels) != 1 {
		t.Fatalf("expected 1 device_channels entry, got %d", len(env.DeviceChannels))
	}
	channels := env.DeviceChannels[0].Channels
	if len(channels) != 1 {
		t.Fatalf("expected 1 channel, got %d", len(channels))
	}
	ch0 := channels[0]
	if ch0.Name == "Bookshelf Lamp" {
		t.Error("channel name must be anonymised, but original value was returned")
	}
	if ch0.Address != "0001ABCD:1" {
		t.Errorf("channel address must not be anonymised, got %q", ch0.Address)
	}
}
