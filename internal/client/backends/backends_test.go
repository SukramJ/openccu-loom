// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package backends

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// jsonUnmarshal is a package-level alias so fakeScriptRunner.RunJSON
// can call encoding/json.Unmarshal without re-importing the package.
var jsonUnmarshal = json.Unmarshal

type fakeCaller struct {
	called  atomic.Int32
	lastArg atomic.Value
	reply   any
	err     error
}

func (f *fakeCaller) Call(_ context.Context, method string, args ...any) (any, error) {
	f.called.Add(1)
	f.lastArg.Store([]any{method, args})
	if f.err != nil {
		return nil, f.err
	}
	return f.reply, nil
}

// fakeScriptRunner is a test double for [ScriptRunner].
// rawJSON is returned verbatim by Run; RunJSON unmarshals it into v.
type fakeScriptRunner struct {
	called     atomic.Int32
	lastScript hmenum.RegaScript
	lastParams map[string]string
	rawJSON    string
	err        error
}

func (f *fakeScriptRunner) Run(_ context.Context, script hmenum.RegaScript, params map[string]string) (string, error) {
	f.called.Add(1)
	f.lastScript = script
	f.lastParams = params
	if f.err != nil {
		return "", f.err
	}
	return f.rawJSON, nil
}

func (f *fakeScriptRunner) RunJSON(ctx context.Context, script hmenum.RegaScript, params map[string]string, v any) error {
	raw, err := f.Run(ctx, script, params)
	if err != nil {
		return err
	}
	if raw == "" {
		return nil
	}
	return jsonUnmarshal([]byte(raw), v)
}

func TestCcuBackendListDevices(t *testing.T) {
	x := &fakeCaller{reply: []any{
		map[string]any{"ADDRESS": "0001", "TYPE": "HmIP-STH"},
	}}
	b := NewCcuBackend(x, nil, nil)
	devs, err := b.ListDevices(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(devs) != 1 || devs[0].Address != "0001" || devs[0].Type != "HmIP-STH" {
		t.Fatalf("devs=%+v", devs)
	}
}

func TestCcuBackendSetValuePassesThrough(t *testing.T) {
	x := &fakeCaller{reply: nil}
	b := NewCcuBackend(x, nil, nil)
	if err := b.SetValue(context.Background(), "0001:1", hmenum.ParameterState, true, hmenum.CommandPriorityHigh, hmenum.CommandRxModeUnset); err != nil {
		t.Fatalf("set: %v", err)
	}
	if x.called.Load() != 1 {
		t.Fatalf("calls=%d", x.called.Load())
	}
}

func TestCcuBackendFirmwareUpdateRequiresXML(t *testing.T) {
	b := NewCcuBackend(nil, &fakeCaller{}, nil)
	if err := b.UpdateFirmware(context.Background(), "0001"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err=%v", err)
	}
}

func TestCcuBackendFirmwareUpdateTriesInstallFirst(t *testing.T) {
	x := &fakeCaller{}
	b := NewCcuBackend(x, nil, nil)
	if err := b.UpdateFirmware(context.Background(), "0001"); err != nil {
		t.Fatalf("update: %v", err)
	}
	// installFirmware is the first attempt; on success only one call is made.
	if x.called.Load() != 1 {
		t.Fatalf("xml calls=%d, want 1", x.called.Load())
	}
}

func TestCuxdBackendFirmwareUnsupported(t *testing.T) {
	b := NewCuxdBackend(&fakeCaller{}, nil)
	if err := b.UpdateFirmware(context.Background(), "0001"); !errors.Is(err, ErrUnsupported) {
		t.Fatal("cuxd must refuse firmware update")
	}
}

func TestCuxdBackendKindMatchesContract(t *testing.T) {
	b := NewCuxdBackend(&fakeCaller{}, nil)
	if b.Kind() != KindCUxD {
		t.Fatalf("kind=%s", b.Kind())
	}
}

func TestFactoryWiresCcuForHmIPRF(t *testing.T) {
	b, err := Factory(hmenum.InterfaceHmIPRF, FactoryInput{XMLRPC: &fakeCaller{}})
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	if b.Kind() != KindCCU {
		t.Fatalf("kind=%s", b.Kind())
	}
}

func TestFactoryWiresCuxd(t *testing.T) {
	b, err := Factory(hmenum.InterfaceCUxD, FactoryInput{BINRPC: &fakeCaller{}})
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	if b.Kind() != KindCUxD {
		t.Fatalf("kind=%s", b.Kind())
	}
}

func TestFactoryWiresHomegear(t *testing.T) {
	b, err := FactoryWithKind(hmenum.InterfaceHmIPRF, KindHomegear, FactoryInput{XMLRPC: &fakeCaller{}})
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	if b.Kind() != KindHomegear {
		t.Fatalf("kind=%s", b.Kind())
	}
}

func TestCcuBackendReportValueUsageDispatchesXMLRPC(t *testing.T) {
	x := &fakeCaller{reply: true}
	b := NewCcuBackend(x, nil, nil)
	if err := b.ReportValueUsage(context.Background(), "ABCD1234:1", "PRESS_SHORT", 1); err != nil {
		t.Fatalf("ReportValueUsage: %v", err)
	}
	got, _ := x.lastArg.Load().([]any)
	if len(got) != 2 {
		t.Fatalf("expected method+args call, got %v", got)
	}
	if method, _ := got[0].(string); method != "reportValueUsage" {
		t.Fatalf("method=%s", method)
	}
	args, _ := got[1].([]any)
	if len(args) != 3 || args[0] != "ABCD1234:1" || args[1] != "PRESS_SHORT" || args[2] != 1 {
		t.Fatalf("args=%v", args)
	}
}

func TestCcuBackendReportValueUsageWithoutXMLRPC(t *testing.T) {
	b := NewCcuBackend(nil, nil, nil)
	if err := b.ReportValueUsage(context.Background(), "X", "PRESS_SHORT", 1); !errors.Is(err, ErrNotWired) {
		t.Fatalf("expected ErrNotWired, got %v", err)
	}
}

func TestCcuBackendDeleteDeviceDispatchesXMLRPC(t *testing.T) {
	t.Parallel()
	x := &fakeCaller{reply: nil}
	b := NewCcuBackend(x, nil, nil)
	if err := b.DeleteDevice(context.Background(), "0001ABCD", 0); err != nil {
		t.Fatalf("DeleteDevice: %v", err)
	}
	got, _ := x.lastArg.Load().([]any)
	if len(got) != 2 {
		t.Fatalf("expected method+args call, got %v", got)
	}
	if method, _ := got[0].(string); method != "deleteDevice" {
		t.Fatalf("method=%s, want deleteDevice", method)
	}
	args, _ := got[1].([]any)
	if len(args) != 2 || args[0] != "0001ABCD" || args[1] != 0 {
		t.Fatalf("args=%v, want [0001ABCD 0]", args)
	}
}

// TestCcuBackendDeleteDeviceForwardsFlags verifies the reset|force delete
// bitmask reaches the CCU on the wire rather than a hard-coded 0.
func TestCcuBackendDeleteDeviceForwardsFlags(t *testing.T) {
	t.Parallel()
	x := &fakeCaller{reply: nil}
	b := NewCcuBackend(x, nil, nil)
	flags := DeleteFlagReset | DeleteFlagForce
	if err := b.DeleteDevice(context.Background(), "0001ABCD", flags); err != nil {
		t.Fatalf("DeleteDevice: %v", err)
	}
	got, _ := x.lastArg.Load().([]any)
	args, _ := got[1].([]any)
	if len(args) != 2 || args[0] != "0001ABCD" || args[1] != flags {
		t.Fatalf("args=%v, want [0001ABCD %d]", args, flags)
	}
}

func TestCcuBackendDeleteDeviceWithoutXMLRPCErrors(t *testing.T) {
	t.Parallel()
	b := NewCcuBackend(nil, nil, nil)
	if err := b.DeleteDevice(context.Background(), "0001ABCD", 0); !errors.Is(err, ErrNotWired) {
		t.Fatalf("expected ErrNotWired, got %v", err)
	}
}

func TestCuxdBackendDeleteDeviceUnsupported(t *testing.T) {
	t.Parallel()
	b := NewCuxdBackend(&fakeCaller{}, nil)
	if err := b.DeleteDevice(context.Background(), "CUX0001", 0); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("expected ErrUnsupported, got %v", err)
	}
}

// TestCcuBackendRestoreConfigToDeviceDispatchesXMLRPC verifies the wire
// call is exactly `restoreConfigToDevice(address)` — the CCU method name
// rfd (BidCos-RF) and HMIPServer (HmIP-RF) share.
func TestCcuBackendRestoreConfigToDeviceDispatchesXMLRPC(t *testing.T) {
	t.Parallel()
	x := &fakeCaller{reply: nil}
	b := NewCcuBackend(x, nil, nil)
	if err := b.RestoreConfigToDevice(context.Background(), "0001ABCD"); err != nil {
		t.Fatalf("RestoreConfigToDevice: %v", err)
	}
	got, _ := x.lastArg.Load().([]any)
	if len(got) != 2 {
		t.Fatalf("expected method+args call, got %v", got)
	}
	if method, _ := got[0].(string); method != "restoreConfigToDevice" {
		t.Fatalf("method=%s, want restoreConfigToDevice", method)
	}
	args, _ := got[1].([]any)
	if len(args) != 1 || args[0] != "0001ABCD" {
		t.Fatalf("args=%v, want [0001ABCD]", args)
	}
}

// TestCcuBackendRestoreConfigToDevicePropagatesFault verifies an XML-RPC
// fault from the CCU (e.g. the device is momentarily unreachable) is
// propagated to the caller rather than swallowed.
func TestCcuBackendRestoreConfigToDevicePropagatesFault(t *testing.T) {
	t.Parallel()
	fault := &hmerr.XMLRPCFault{Code: -1, Message: "device unreachable"}
	x := &fakeCaller{err: fault}
	b := NewCcuBackend(x, nil, nil)
	err := b.RestoreConfigToDevice(context.Background(), "0001ABCD")
	var got *hmerr.XMLRPCFault
	if !errors.As(err, &got) || got.Code != -1 {
		t.Fatalf("expected *hmerr.XMLRPCFault{Code: -1}, got %v", err)
	}
}

func TestCcuBackendRestoreConfigToDeviceWithoutXMLRPCErrors(t *testing.T) {
	t.Parallel()
	b := NewCcuBackend(nil, nil, nil)
	if err := b.RestoreConfigToDevice(context.Background(), "0001ABCD"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("expected ErrUnsupported, got %v", err)
	}
}

func TestCuxdBackendRestoreConfigToDeviceUnsupported(t *testing.T) {
	t.Parallel()
	b := NewCuxdBackend(&fakeCaller{}, nil)
	if err := b.RestoreConfigToDevice(context.Background(), "CUX0001"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("expected ErrUnsupported, got %v", err)
	}
}

func TestHomegearBackendRestoreConfigToDeviceUnsupported(t *testing.T) {
	t.Parallel()
	b := NewHomegearBackend(&fakeCaller{}, nil)
	if err := b.RestoreConfigToDevice(context.Background(), "ABCD1234"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("expected ErrUnsupported, got %v", err)
	}
}

// TestCcuBackendListReplaceableDevicesDispatchesXMLRPC verifies the wire
// call is exactly `listReplaceableDevices(newAddress)` and that a returned
// device row decodes correctly.
func TestCcuBackendListReplaceableDevicesDispatchesXMLRPC(t *testing.T) {
	t.Parallel()
	x := &fakeCaller{reply: []any{
		map[string]any{"ADDRESS": "OLD001", "TYPE": "HM-Sec-SC"},
	}}
	b := NewCcuBackend(x, nil, nil)
	devs, err := b.ListReplaceableDevices(context.Background(), "NEW001")
	if err != nil {
		t.Fatalf("ListReplaceableDevices: %v", err)
	}
	if len(devs) != 1 || devs[0].Address != "OLD001" || devs[0].Type != "HM-Sec-SC" {
		t.Fatalf("devs=%+v", devs)
	}
	got, _ := x.lastArg.Load().([]any)
	if len(got) != 2 {
		t.Fatalf("expected method+args call, got %v", got)
	}
	if method, _ := got[0].(string); method != "listReplaceableDevices" {
		t.Fatalf("method=%s, want listReplaceableDevices", method)
	}
	args, _ := got[1].([]any)
	if len(args) != 1 || args[0] != "NEW001" {
		t.Fatalf("args=%v, want [NEW001]", args)
	}
}

// TestCcuBackendListReplaceableDevicesPropagatesFault verifies an XML-RPC
// fault from the CCU (e.g. a serial belonging to another interface) is
// propagated rather than swallowed.
func TestCcuBackendListReplaceableDevicesPropagatesFault(t *testing.T) {
	t.Parallel()
	fault := &hmerr.XMLRPCFault{Code: -1, Message: "unknown method"}
	x := &fakeCaller{err: fault}
	b := NewCcuBackend(x, nil, nil)
	_, err := b.ListReplaceableDevices(context.Background(), "NEW001")
	var got *hmerr.XMLRPCFault
	if !errors.As(err, &got) || got.Code != -1 {
		t.Fatalf("expected *hmerr.XMLRPCFault{Code: -1}, got %v", err)
	}
}

func TestCcuBackendListReplaceableDevicesWithoutXMLRPCErrors(t *testing.T) {
	t.Parallel()
	b := NewCcuBackend(nil, nil, nil)
	if _, err := b.ListReplaceableDevices(context.Background(), "NEW001"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("expected ErrUnsupported, got %v", err)
	}
}

// TestCcuBackendReplaceDeviceDispatchesXMLRPC verifies the wire call is
// exactly `replaceDevice(old, new)`.
func TestCcuBackendReplaceDeviceDispatchesXMLRPC(t *testing.T) {
	t.Parallel()
	x := &fakeCaller{reply: nil}
	b := NewCcuBackend(x, nil, nil)
	if err := b.ReplaceDevice(context.Background(), "OLD001", "NEW001"); err != nil {
		t.Fatalf("ReplaceDevice: %v", err)
	}
	got, _ := x.lastArg.Load().([]any)
	if len(got) != 2 {
		t.Fatalf("expected method+args call, got %v", got)
	}
	if method, _ := got[0].(string); method != "replaceDevice" {
		t.Fatalf("method=%s, want replaceDevice", method)
	}
	args, _ := got[1].([]any)
	if len(args) != 2 || args[0] != "OLD001" || args[1] != "NEW001" {
		t.Fatalf("args=%v, want [OLD001 NEW001]", args)
	}
}

// TestCcuBackendReplaceDevicePropagatesFault verifies an incompatible-pair
// fault from the CCU is propagated rather than swallowed.
func TestCcuBackendReplaceDevicePropagatesFault(t *testing.T) {
	t.Parallel()
	fault := &hmerr.XMLRPCFault{Code: -2, Message: "incompatible device types"}
	x := &fakeCaller{err: fault}
	b := NewCcuBackend(x, nil, nil)
	err := b.ReplaceDevice(context.Background(), "OLD001", "NEW001")
	var got *hmerr.XMLRPCFault
	if !errors.As(err, &got) || got.Code != -2 {
		t.Fatalf("expected *hmerr.XMLRPCFault{Code: -2}, got %v", err)
	}
}

func TestCcuBackendReplaceDeviceWithoutXMLRPCErrors(t *testing.T) {
	t.Parallel()
	b := NewCcuBackend(nil, nil, nil)
	if err := b.ReplaceDevice(context.Background(), "OLD001", "NEW001"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("expected ErrUnsupported, got %v", err)
	}
}

func TestCuxdBackendListReplaceableDevicesUnsupported(t *testing.T) {
	t.Parallel()
	b := NewCuxdBackend(&fakeCaller{}, nil)
	if _, err := b.ListReplaceableDevices(context.Background(), "NEW001"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("expected ErrUnsupported, got %v", err)
	}
}

func TestCuxdBackendReplaceDeviceUnsupported(t *testing.T) {
	t.Parallel()
	b := NewCuxdBackend(&fakeCaller{}, nil)
	if err := b.ReplaceDevice(context.Background(), "OLD001", "NEW001"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("expected ErrUnsupported, got %v", err)
	}
}

func TestHomegearBackendListReplaceableDevicesUnsupported(t *testing.T) {
	t.Parallel()
	b := NewHomegearBackend(&fakeCaller{}, nil)
	if _, err := b.ListReplaceableDevices(context.Background(), "NEW001"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("expected ErrUnsupported, got %v", err)
	}
}

func TestHomegearBackendReplaceDeviceUnsupported(t *testing.T) {
	t.Parallel()
	b := NewHomegearBackend(&fakeCaller{}, nil)
	if err := b.ReplaceDevice(context.Background(), "OLD001", "NEW001"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("expected ErrUnsupported, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// SearchDevices (BidCos-Wired wired-bus scan)
// ---------------------------------------------------------------------------

// TestCcuBackendSearchDevicesDispatchesXMLRPCNoArgsIntResult verifies the
// wire call is exactly `searchDevices()` with no arguments on a
// BidCos-Wired-scoped backend, and that an int result decodes as-is.
func TestCcuBackendSearchDevicesDispatchesXMLRPCNoArgsIntResult(t *testing.T) {
	t.Parallel()
	x := &fakeCaller{reply: 3}
	b := NewCcuBackendForInterface(hmenum.InterfaceBidCosWired, x, nil, nil)
	count, err := b.SearchDevices(context.Background())
	if err != nil {
		t.Fatalf("SearchDevices: %v", err)
	}
	if count != 3 {
		t.Fatalf("count=%d, want 3", count)
	}
	got, _ := x.lastArg.Load().([]any)
	if len(got) != 2 {
		t.Fatalf("expected method+args call, got %v", got)
	}
	if method, _ := got[0].(string); method != "searchDevices" {
		t.Fatalf("method=%s, want searchDevices", method)
	}
	args, _ := got[1].([]any)
	if len(args) != 0 {
		t.Fatalf("args=%v, want no arguments", args)
	}
}

// TestCcuBackendSearchDevicesCoercesFloat64Result verifies a float64 wire
// reply (as XML-RPC ints commonly decode) is coerced to int.
func TestCcuBackendSearchDevicesCoercesFloat64Result(t *testing.T) {
	t.Parallel()
	x := &fakeCaller{reply: float64(5)}
	b := NewCcuBackendForInterface(hmenum.InterfaceBidCosWired, x, nil, nil)
	count, err := b.SearchDevices(context.Background())
	if err != nil {
		t.Fatalf("SearchDevices: %v", err)
	}
	if count != 5 {
		t.Fatalf("count=%d, want 5", count)
	}
}

// TestCcuBackendSearchDevicesNonWiredInterfaceUnsupportedWithoutWireCall
// verifies the interface gate rejects every non-BidCos-Wired interface
// before any wire call is attempted — only hs485d implements searchDevices.
func TestCcuBackendSearchDevicesNonWiredInterfaceUnsupportedWithoutWireCall(t *testing.T) {
	t.Parallel()
	x := &fakeCaller{reply: 7}
	b := NewCcuBackendForInterface(hmenum.InterfaceBidCosRF, x, nil, nil)
	if _, err := b.SearchDevices(context.Background()); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("expected ErrUnsupported, got %v", err)
	}
	if x.called.Load() != 0 {
		t.Errorf("wire call must never be made for a non-wired interface, called=%d", x.called.Load())
	}
}

// TestCcuBackendSearchDevicesWithoutXMLRPCReturnsErrNotWired verifies a
// BidCos-Wired-scoped backend without an XML-RPC caller reports
// ErrNotWired rather than attempting a nil call.
func TestCcuBackendSearchDevicesWithoutXMLRPCReturnsErrNotWired(t *testing.T) {
	t.Parallel()
	b := NewCcuBackendForInterface(hmenum.InterfaceBidCosWired, nil, nil, nil)
	if _, err := b.SearchDevices(context.Background()); !errors.Is(err, ErrNotWired) {
		t.Fatalf("expected ErrNotWired, got %v", err)
	}
}

func TestCuxdBackendSearchDevicesUnsupported(t *testing.T) {
	t.Parallel()
	b := NewCuxdBackend(&fakeCaller{}, nil)
	if _, err := b.SearchDevices(context.Background()); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("expected ErrUnsupported, got %v", err)
	}
}

func TestHomegearBackendSearchDevicesUnsupported(t *testing.T) {
	t.Parallel()
	b := NewHomegearBackend(&fakeCaller{}, nil)
	if _, err := b.SearchDevices(context.Background()); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("expected ErrUnsupported, got %v", err)
	}
}

func TestCcuBackendSetTeamDispatchesXMLRPC(t *testing.T) {
	t.Parallel()
	x := &fakeCaller{}
	b := NewCcuBackend(x, nil, nil)
	if err := b.SetTeam(context.Background(), "ABC:1", "TEAM:1"); err != nil {
		t.Fatalf("SetTeam: %v", err)
	}
	got, _ := x.lastArg.Load().([]any)
	method, _ := got[0].(string)
	args, _ := got[1].([]any)
	if method != "setTeam" || len(args) != 2 || args[0] != "ABC:1" || args[1] != "TEAM:1" {
		t.Fatalf("wire call = %s %v, want setTeam [ABC:1 TEAM:1]", method, args)
	}
}

func TestCcuBackendSetTeamEmptyResetsTeam(t *testing.T) {
	t.Parallel()
	x := &fakeCaller{}
	b := NewCcuBackend(x, nil, nil)
	if err := b.SetTeam(context.Background(), "ABC:1", ""); err != nil {
		t.Fatalf("SetTeam: %v", err)
	}
	got, _ := x.lastArg.Load().([]any)
	args, _ := got[1].([]any)
	if len(args) != 2 || args[1] != "" {
		t.Fatalf("reset must send empty team, got %v", args)
	}
}

func TestCcuBackendSetTeamWithoutXMLRPCUnsupported(t *testing.T) {
	t.Parallel()
	b := NewCcuBackend(nil, &fakeCaller{}, nil)
	if err := b.SetTeam(context.Background(), "ABC:1", "TEAM:1"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("expected ErrUnsupported, got %v", err)
	}
}

func TestCcuBackendListTeamsDecodesStructArray(t *testing.T) {
	t.Parallel()
	x := &fakeCaller{reply: []any{
		map[string]any{"ADDRESS": "TEAM:1", "PARENT": "TEAM", "TEAM_TAG": "SMOKE_DETECTOR"},
	}}
	b := NewCcuBackend(x, nil, nil)
	teams, err := b.ListTeams(context.Background())
	if err != nil {
		t.Fatalf("ListTeams: %v", err)
	}
	if len(teams) != 1 || teams[0].Address != "TEAM:1" || teams[0].TeamTag != "SMOKE_DETECTOR" {
		t.Fatalf("teams=%+v", teams)
	}
}

func TestCuxdBackendTeamUnsupported(t *testing.T) {
	t.Parallel()
	b := NewCuxdBackend(&fakeCaller{}, nil)
	if err := b.SetTeam(context.Background(), "A:1", "T:1"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("SetTeam: expected ErrUnsupported, got %v", err)
	}
	if _, err := b.ListTeams(context.Background()); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("ListTeams: expected ErrUnsupported, got %v", err)
	}
}

func TestHomegearBackendTeamUnsupported(t *testing.T) {
	t.Parallel()
	b := NewHomegearBackend(&fakeCaller{}, nil)
	if err := b.SetTeam(context.Background(), "A:1", "T:1"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("SetTeam: expected ErrUnsupported, got %v", err)
	}
	if _, err := b.ListTeams(context.Background()); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("ListTeams: expected ErrUnsupported, got %v", err)
	}
}

func TestCcuBackendGetParamsetDescriptionMapsStructs(t *testing.T) {
	x := &fakeCaller{reply: map[string]any{
		"STATE": map[string]any{"TYPE": "BOOL", "OPERATIONS": 3},
	}}
	b := NewCcuBackend(x, nil, nil)
	out, err := b.GetParamsetDescription(context.Background(), "0001:1", hmenum.ParamsetKeyValues)
	if err != nil {
		t.Fatalf("desc: %v", err)
	}
	pd, ok := out["STATE"]
	if !ok || pd.Type != hmenum.ParameterTypeBool {
		t.Fatalf("out=%+v", out)
	}
	if pd.Operations != 3 {
		t.Fatalf("operations=%d", pd.Operations)
	}
}

// TestCcuBackendDetermineParameter verifies that DetermineParameter forwards
// to the "determineParameter" XML-RPC method and returns the value.
func TestCcuBackendDetermineParameter(t *testing.T) {
	x := &fakeCaller{reply: float64(21.5)}
	b := NewCcuBackend(x, nil, nil)
	got, err := b.DetermineParameter(context.Background(), "VCU1234567:1", "SET_POINT_TEMPERATURE")
	if err != nil {
		t.Fatalf("DetermineParameter: %v", err)
	}
	if got != float64(21.5) {
		t.Fatalf("DetermineParameter = %v; want 21.5", got)
	}
	if x.called.Load() != 1 {
		t.Fatalf("expected 1 call; got %d", x.called.Load())
	}
}

// TestCcuBackendDetermineParameterNotWired verifies ErrNotWired is returned
// when the XML caller is nil.
func TestCcuBackendDetermineParameterNotWired(t *testing.T) {
	b := NewCcuBackend(nil, nil, nil)
	_, err := b.DetermineParameter(context.Background(), "VCU:1", "STATE")
	if !errors.Is(err, ErrNotWired) {
		t.Fatalf("expected ErrNotWired; got %v", err)
	}
}

// TestCuxdBackendDetermineParameterUnsupported verifies that CUxD returns
// ErrUnsupported for DetermineParameter ( — CUxD has no such method).
func TestCuxdBackendDetermineParameterUnsupported(t *testing.T) {
	b := NewCuxdBackend(nil, nil)
	_, err := b.DetermineParameter(context.Background(), "VCU:1", "STATE")
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("expected ErrUnsupported; got %v", err)
	}
}

// fakeInitBackend is a minimal Operations implementation that records
// whether Initialize was called.
type fakeInitBackend struct {
	*CcuBackend
	initCalled bool
}

func (f *fakeInitBackend) Initialize(_ context.Context) error {
	f.initCalled = true
	return nil
}

// TestMaybeInitialize_CallsInitializerWhenPresent verifies that
// MaybeInitialize invokes Initialize on backends that implement Initializer.
func TestMaybeInitialize_CallsInitializerWhenPresent(t *testing.T) {
	b := &fakeInitBackend{CcuBackend: NewCcuBackend(&fakeCaller{}, nil, nil)}
	if err := MaybeInitialize(context.Background(), b); err != nil {
		t.Fatalf("MaybeInitialize: unexpected error: %v", err)
	}
	if !b.initCalled {
		t.Fatal("Initialize was not called on a backend implementing Initializer")
	}
}

// TestMaybeInitialize_NoopWhenNotImplemented verifies that MaybeInitialize
// is a safe no-op for backends that do not implement Initializer.
func TestMaybeInitialize_NoopWhenNotImplemented(t *testing.T) {
	// Use a bare Operations value that does NOT implement Initializer.
	// MaybeInitialize must return nil without panicking.
	b := NewCuxdBackend(&fakeCaller{}, nil)
	if err := MaybeInitialize(context.Background(), b); err != nil {
		t.Fatalf("MaybeInitialize on non-Initializer backend: %v", err)
	}
}

// TestFactoryWithKind_CcuImplementsInitializer verifies that the CcuBackend
// returned by FactoryWithKind satisfies the Initializer interface.
func TestFactoryWithKind_CcuImplementsInitializer(t *testing.T) {
	b, err := FactoryWithKind(hmenum.InterfaceHmIPRF, KindCCU, FactoryInput{XMLRPC: &fakeCaller{}})
	if err != nil {
		t.Fatalf("FactoryWithKind: %v", err)
	}
	if _, ok := b.(Initializer); !ok {
		t.Fatal("CcuBackend from FactoryWithKind does not implement Initializer")
	}
}

// TestFactorySelectsCcuForAllRFInterfaces verifies that BidCos-RF,
// BidCos-Wired, and VirtualDevices all yield KindCCU from Factory.
func TestFactorySelectsCcuForAllRFInterfaces(t *testing.T) {
	t.Parallel()
	rfInterfaces := []hmenum.Interface{
		hmenum.InterfaceBidCosRF,
		hmenum.InterfaceBidCosWired,
		hmenum.InterfaceVirtualDevices,
	}
	for _, iface := range rfInterfaces {
		t.Run(string(iface), func(t *testing.T) {
			t.Parallel()
			b, err := Factory(iface, FactoryInput{XMLRPC: &fakeCaller{}})
			if err != nil {
				t.Fatalf("Factory(%s): unexpected error: %v", iface, err)
			}
			if b.Kind() != KindCCU {
				t.Fatalf("Factory(%s): kind=%s, want ccu", iface, b.Kind())
			}
		})
	}
}

// TestFactoryErrorsWhenXMLRPCMissingForCCU verifies that omitting the XMLRPC
// caller returns an error for CCU-kind backends.
func TestFactoryErrorsWhenXMLRPCMissingForCCU(t *testing.T) {
	t.Parallel()
	_, err := Factory(hmenum.InterfaceHmIPRF, FactoryInput{BINRPC: &fakeCaller{}})
	if err == nil {
		t.Fatal("expected error when XMLRPC is nil for CCU backend, got nil")
	}
}

// TestFactoryErrorsWhenBINRPCMissingForCUxD verifies that omitting the BINRPC
// caller returns an error for CUxD backends.
func TestFactoryErrorsWhenBINRPCMissingForCUxD(t *testing.T) {
	t.Parallel()
	_, err := Factory(hmenum.InterfaceCUxD, FactoryInput{XMLRPC: &fakeCaller{}})
	if err == nil {
		t.Fatal("expected error when BINRPC is nil for CUxD backend, got nil")
	}
}

// TestFactoryErrorsWhenXMLRPCMissingForHomegear verifies that omitting the
// XMLRPC caller returns an error for Homegear backends.
func TestFactoryErrorsWhenXMLRPCMissingForHomegear(t *testing.T) {
	t.Parallel()
	_, err := FactoryWithKind(hmenum.InterfaceHmIPRF, KindHomegear, FactoryInput{BINRPC: &fakeCaller{}})
	if err == nil {
		t.Fatal("expected error when XMLRPC is nil for Homegear backend, got nil")
	}
}

// TestFactoryWithKindErrorsForUnknownKind verifies that an unrecognised Kind
// value causes FactoryWithKind to return an error.
func TestFactoryWithKindErrorsForUnknownKind(t *testing.T) {
	t.Parallel()
	unknown := Kind(99)
	_, err := FactoryWithKind(hmenum.InterfaceHmIPRF, unknown, FactoryInput{XMLRPC: &fakeCaller{}})
	if err == nil {
		t.Fatal("expected error for unknown Kind, got nil")
	}
}

// TestFactoryHomegearIgnoresJSONRPCCaller verifies that supplying both XMLRPC
// and JSONRPC to a Homegear factory call is accepted and the result is still
// KindHomegear — the JSON caller is unused.
func TestFactoryHomegearIgnoresJSONRPCCaller(t *testing.T) {
	t.Parallel()
	b, err := FactoryWithKind(hmenum.InterfaceHmIPRF, KindHomegear, FactoryInput{
		XMLRPC:  &fakeCaller{},
		JSONRPC: &fakeCaller{},
	})
	if err != nil {
		t.Fatalf("FactoryWithKind(Homegear): %v", err)
	}
	if b.Kind() != KindHomegear {
		t.Fatalf("kind=%s, want homegear", b.Kind())
	}
}

// TestKindForAllMVPInterfaces verifies that KindFor maps each MVP interface
// to the correct backend kind.
func TestKindForAllMVPInterfaces(t *testing.T) {
	t.Parallel()
	cases := []struct {
		iface hmenum.Interface
		want  Kind
	}{
		{hmenum.InterfaceHmIPRF, KindCCU},
		{hmenum.InterfaceBidCosRF, KindCCU},
		{hmenum.InterfaceBidCosWired, KindCCU},
		{hmenum.InterfaceVirtualDevices, KindCCU},
		{hmenum.InterfaceCUxD, KindCUxD},
	}
	for _, tc := range cases {
		t.Run(string(tc.iface), func(t *testing.T) {
			t.Parallel()
			got := KindFor(tc.iface)
			if got != tc.want {
				t.Fatalf("KindFor(%s)=%s, want %s", tc.iface, got, tc.want)
			}
		})
	}
}
