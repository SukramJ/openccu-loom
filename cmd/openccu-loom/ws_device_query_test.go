// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	clientpkg "github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/configui"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// scopedBackendStub records which central's backend a config edit session read
// and wrote, so a session scoped to one CCU can be shown to hit that CCU and
// not the domain's first-match default. It embeds the package's full
// backends.Operations stub and overrides only the two paramset calls.
type scopedBackendStub struct {
	*testBackendOps
	label     string
	putCalled bool
}

func (b *scopedBackendStub) GetParamset(_ context.Context, _ string, _ hmenum.ParamsetKey) (map[string]any, error) {
	return map[string]any{"CENTRAL": b.label}, nil
}

func (b *scopedBackendStub) PutParamset(_ context.Context, _ string, _ hmenum.ParamsetKey, _ map[string]any, _ hmenum.CommandPriority, _ hmenum.CommandRxMode) error {
	b.putCalled = true
	return nil
}

// TestWSConfigSession_ReadsAndWritesTheScopedCentral pins that a config edit
// session honours central_name on BOTH the value read and the save, not only
// the description read. Two centrals hold the same channel address; the domain
// resolves an address to the first matching central (registry order is
// name-sorted), so an unscoped read/write lands on ccu-a. A session scoped to
// ccu-b must read and write ccu-b — otherwise a session can read one CCU and
// silently write another.
func TestWSConfigSession_ReadsAndWritesTheScopedCentral(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "ccu-a", "ccu-b")
	const channel = "COLLIDE01:1"
	for _, name := range []string{"ccu-a", "ccu-b"} {
		cu, ok := reg.Get(name)
		if !ok {
			t.Fatalf("%s not in registry", name)
		}
		cu.ModelRegistry.Put(device.New(device.Config{
			Address:     "COLLIDE01",
			InterfaceID: "BidCos-RF",
			Interface:   hmenum.InterfaceBidCosRF,
			Model:       "HM-LC-Sw1-Pl",
		}))
	}

	writer := clientpkg.NewValueWriter()
	backendA := &scopedBackendStub{testBackendOps: &testBackendOps{}, label: "ccu-a"}
	backendB := &scopedBackendStub{testBackendOps: &testBackendOps{}, label: "ccu-b"}
	wireID := hmtypes.ParseWireInterfaceID("BidCos-RF")
	writer.Register("ccu-a", wireID, backendA)
	writer.Register("ccu-b", wireID, backendB)

	paramsets := adapter.NewParamsetsDomain(reg, writer)
	q := &wsDeviceQuery{paramsets: paramsets, registry: reg, writer: writer}
	pw := &wsParamsetWriter{domain: paramsets}
	keyB := configui.SessionKey{CentralName: "ccu-b", ChannelAddress: channel, ParamsetKey: hmenum.ParamsetKeyMaster}

	got, err := q.GetParamset(context.Background(), keyB)
	if err != nil {
		t.Fatalf("GetParamset: %v", err)
	}
	if got["CENTRAL"] != "ccu-b" {
		t.Fatalf("session scoped to ccu-b read %v, want ccu-b's backend", got["CENTRAL"])
	}

	if err := pw.PutParamset(context.Background(), keyB, map[string]any{"FOO": 1}); err != nil {
		t.Fatalf("PutParamset: %v", err)
	}
	if !backendB.putCalled {
		t.Error("write scoped to ccu-b did not reach ccu-b's backend")
	}
	if backendA.putCalled {
		t.Error("write scoped to ccu-b leaked to ccu-a (the default first-match central)")
	}
}

// coercingBackendStub answers a MASTER descriptor declaring one INTEGER
// parameter and records the values the write put on the wire, so the test can
// see what type actually left the daemon.
type coercingBackendStub struct {
	*testBackendOps
	mu    sync.Mutex
	wrote map[string]any
}

func (b *coercingBackendStub) GetParamsetDescription(
	_ context.Context, _ string, _ hmenum.ParamsetKey,
) (map[string]hmproto.ParameterData, error) {
	return map[string]hmproto.ParameterData{
		"DBL_PRESS_TIME": {
			Type:       hmenum.ParameterTypeInteger,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
			Min:        []byte(`0`),
			Max:        []byte(`100`),
		},
	}, nil
}

func (b *coercingBackendStub) GetParamset(_ context.Context, _ string, _ hmenum.ParamsetKey) (map[string]any, error) {
	return map[string]any{"DBL_PRESS_TIME": 1}, nil
}

func (b *coercingBackendStub) PutParamset(
	_ context.Context, _ string, _ hmenum.ParamsetKey, values map[string]any,
	_ hmenum.CommandPriority, _ hmenum.CommandRxMode,
) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.wrote = values
	return nil
}

func (b *coercingBackendStub) written() map[string]any {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.wrote
}

// TestWSConfigSessionScopedSaveKeepsTheDomainGuarantees pins what the
// central-scoped branch of the session save must not lose.
//
// Honouring central_name is one requirement; going around the paramset domain
// to do it is not the way to meet it. The domain is where a write is coerced
// against the parameter descriptor and where the change-log row is written. A
// session value arrives as decoded JSON, so every number is a float64, and the
// XML-RPC encoder maps float64 to <double> — an INTEGER parameter sent from
// the scoped branch faulted on the device. The audit row went missing in
// exactly the multi-CCU case where "which CCU did this change land on" is the
// question being asked.
func TestWSConfigSessionScopedSaveKeepsTheDomainGuarantees(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "ccu-a", "ccu-b")
	const channel = "COLLIDE02:1"
	for _, name := range []string{"ccu-a", "ccu-b"} {
		cu, ok := reg.Get(name)
		if !ok {
			t.Fatalf("%s not in registry", name)
		}
		cu.ModelRegistry.Put(device.New(device.Config{
			Address:     "COLLIDE02",
			InterfaceID: "BidCos-RF",
			Interface:   hmenum.InterfaceBidCosRF,
			Model:       "HM-PB-2-WM55",
		}))
	}

	writer := clientpkg.NewValueWriter()
	backendA := &scopedBackendStub{testBackendOps: &testBackendOps{}, label: "ccu-a"}
	backendB := &coercingBackendStub{testBackendOps: &testBackendOps{}}
	wireID := hmtypes.ParseWireInterfaceID("BidCos-RF")
	writer.Register("ccu-a", wireID, backendA)
	writer.Register("ccu-b", wireID, backendB)

	rec := audit.NewBuffer(16)
	pw := &wsParamsetWriter{
		domain: adapter.NewParamsetsDomain(reg, writer).SetAuditRecorder(rec),
	}
	keyB := configui.SessionKey{CentralName: "ccu-b", ChannelAddress: channel, ParamsetKey: hmenum.ParamsetKeyMaster}

	if err := pw.PutParamset(context.Background(), keyB, map[string]any{"DBL_PRESS_TIME": float64(7)}); err != nil {
		t.Fatalf("PutParamset: %v", err)
	}
	if backendA.putCalled {
		t.Error("write scoped to ccu-b leaked to ccu-a (the default first-match central)")
	}

	got, ok := backendB.written()["DBL_PRESS_TIME"]
	if !ok {
		t.Fatalf("the write never reached ccu-b (wrote=%v)", backendB.written())
	}
	switch got.(type) {
	case int, int32, int64:
	default:
		t.Errorf("DBL_PRESS_TIME reached the CCU as %T (%v), want an integer — a float travels as "+
			"<double> and the CCU faults on an INTEGER parameter", got, got)
	}

	entries := rec.List(0)
	if len(entries) != 1 {
		t.Fatalf("change log holds %d entries, want 1 — a scoped save must leave the same trail as an "+
			"unscoped one", len(entries))
	}
	if entries[0].Action != audit.ActionParamsetWrite || entries[0].DeviceAddress != "COLLIDE02" {
		t.Errorf("change-log entry = %+v, want a paramset write on COLLIDE02", entries[0])
	}
}

// ── GetParamsetDescription ────────────────────────────────────────────────────

func TestWsDeviceQuery_GetParamsetDescription_NilParamsets_Errors(t *testing.T) {
	t.Parallel()
	w := &wsDeviceQuery{
		paramsets: nil,
		writer:    (*clientpkg.ValueWriter)(nil),
	}
	_, err := w.GetParamsetDescription(context.Background(), configui.SessionKey{})
	if err == nil {
		t.Fatal("expected error when paramsets=nil")
	}
}

func TestWsDeviceQuery_GetParamsetDescription_NilRegistry_Errors(t *testing.T) {
	t.Parallel()
	// paramsets and writer non-nil; registry nil → second guard.
	reg := buildTestRegistry(t, "ccu-01")
	paramsets := adapter.NewParamsetsDomain(reg, nil)
	w := &wsDeviceQuery{
		paramsets: paramsets,
		writer:    (*clientpkg.ValueWriter)(nil),
		registry:  nil, // nil registry → triggers second guard
	}
	_, err := w.GetParamsetDescription(context.Background(), configui.SessionKey{
		ChannelAddress: "ABC123:1",
	})
	if err == nil {
		t.Fatal("expected error when registry=nil")
	}
}

func TestWsDeviceQuery_GetParamsetDescription_UnknownDevice_Errors(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "ccu-01")
	paramsets := adapter.NewParamsetsDomain(reg, nil)
	w := &wsDeviceQuery{
		paramsets: paramsets,
		writer:    (*clientpkg.ValueWriter)(nil),
		registry:  reg,
	}
	_, err := w.GetParamsetDescription(context.Background(), configui.SessionKey{
		CentralName:    "ccu-01",
		ChannelAddress: "UNKNOWN123:1",
		ParamsetKey:    hmenum.ParamsetKeyMaster,
	})
	// Device not found in empty model registry → error.
	if err == nil {
		t.Fatal("expected error for unknown device")
	}
}

func TestWsDeviceQuery_GetParamsetDescription_EmptyParamsetKey_DefaultsMaster(t *testing.T) {
	t.Parallel()
	// Verify that an empty ParamsetKey defaults to MASTER before lookup fails.
	reg := buildTestRegistry(t, "ccu-01")
	paramsets := adapter.NewParamsetsDomain(reg, nil)
	w := &wsDeviceQuery{
		paramsets: paramsets,
		writer:    (*clientpkg.ValueWriter)(nil),
		registry:  reg,
	}
	_, err := w.GetParamsetDescription(context.Background(), configui.SessionKey{
		CentralName:    "ccu-01",
		ChannelAddress: "ANY:1",
		ParamsetKey:    "", // empty → defaults to MASTER inside GetParamsetDescription
	})
	// Device not found → error, but the empty-key path was exercised.
	if err == nil {
		t.Fatal("expected error for unknown device (testing empty-key default path)")
	}
}

// TestWsDeviceQuery_GetParamsetDescription_DeviceFound_NoBackend_Errors
// exercises the path where the device is in the ModelRegistry but no
// backend is registered in the writer for that central/interface pair.
func TestWsDeviceQuery_GetParamsetDescription_DeviceFound_NoBackend_Errors(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "ccu-01")

	// Put a minimal device into the Unit's ModelRegistry.
	cu, ok := reg.Get("ccu-01")
	if !ok {
		t.Fatal("ccu-01 not found in registry")
	}
	dev := device.New(device.Config{
		Address:     "DEV001",
		InterfaceID: "BidCos-RF",
		Interface:   hmenum.InterfaceBidCosRF,
		Model:       "HM-LC-Sw1-Pl",
	})
	cu.ModelRegistry.Put(dev)

	paramsets := adapter.NewParamsetsDomain(reg, nil)
	writer := clientpkg.NewValueWriter() // no backends registered → Backend() returns ok=false

	w := &wsDeviceQuery{
		paramsets: paramsets,
		writer:    writer,
		registry:  reg,
	}
	_, err := w.GetParamsetDescription(context.Background(), configui.SessionKey{
		CentralName:    "ccu-01",
		ChannelAddress: "DEV001:1",
		ParamsetKey:    hmenum.ParamsetKeyMaster,
	})
	// Device found but no backend → error.
	if err == nil {
		t.Fatal("expected error when no backend is registered for the device's interface")
	}
}

// ── GetDevice identity fields ────────────────────────────────────────────────

// TestWsDeviceQuery_GetDevice_IdentityFieldsAreSerialisable pins that the
// operator-assigned identity the WS device payload carries — the device's
// rooms and functions, each channel's name — arrives as data a client can
// read. The fields live behind accessors on the model, and taking the
// accessor without calling it puts a func value into the map: it compiles,
// and the command then fails at encode time with "unsupported type", which
// is invisible to every test that only inspects the map.
func TestWsDeviceQuery_GetDevice_IdentityFieldsAreSerialisable(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "ccu-01")
	cu, ok := reg.Get("ccu-01")
	if !ok {
		t.Fatal("ccu-01 not found in registry")
	}
	dev := device.New(device.Config{
		Address:     "WSIDENT01",
		InterfaceID: "BidCos-RF",
		Interface:   hmenum.InterfaceBidCosRF,
		Model:       "HM-LC-Sw1-Pl",
		Name:        "Stehlampe",
		Rooms:       []string{"Wohnzimmer"},
		Functions:   []string{"Licht"},
	})
	ch := dev.AddChannel("WSIDENT01:1", 1, "SWITCH", hmenum.ParamsetKeyValues)
	ch.SetName("Stehlampe Schalter")
	cu.ModelRegistry.Put(dev)

	w := &wsDeviceQuery{devs: adapter.NewDevicesAdapter(reg)}
	got, err := w.GetDevice(context.Background(), "WSIDENT01")
	if err != nil {
		t.Fatalf("GetDevice: %v", err)
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("the WS device payload does not encode: %v", err)
	}
	var decoded struct {
		Rooms     []string `json:"rooms"`
		Functions []string `json:"functions"`
		Channels  []struct {
			Name string `json:"name"`
		} `json:"channels"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode WS device payload: %v", err)
	}
	if len(decoded.Rooms) != 1 || decoded.Rooms[0] != "Wohnzimmer" {
		t.Errorf("rooms = %v, want [Wohnzimmer]", decoded.Rooms)
	}
	if len(decoded.Functions) != 1 || decoded.Functions[0] != "Licht" {
		t.Errorf("functions = %v, want [Licht]", decoded.Functions)
	}
	if len(decoded.Channels) != 1 || decoded.Channels[0].Name != "Stehlampe Schalter" {
		t.Errorf("channels = %+v, want one channel named Stehlampe Schalter", decoded.Channels)
	}
}
