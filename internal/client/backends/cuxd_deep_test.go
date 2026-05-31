// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package backends

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ---------------------------------------------------------------------------
// ListDevices — requires bin wired
// ---------------------------------------------------------------------------

func TestCuxdBackendBINRPCRequiredForListDevices(t *testing.T) {
	t.Parallel()
	b := NewCuxdBackend(nil, nil)
	_, err := b.ListDevices(context.Background())
	if !errors.Is(err, ErrNotWired) {
		t.Fatalf("expected ErrNotWired, got %v", err)
	}
}

func TestCuxdBackendListDevicesDecodes(t *testing.T) {
	t.Parallel()
	bin := &fakeCaller{reply: []any{
		map[string]any{"ADDRESS": "CUX0001", "TYPE": "CUxD-Exec"},
		map[string]any{"ADDRESS": "CUX0001:1", "TYPE": "SWITCH", "PARENT": "CUX0001"},
	}}
	b := NewCuxdBackend(bin, nil)
	devs, err := b.ListDevices(context.Background())
	if err != nil {
		t.Fatalf("ListDevices: %v", err)
	}
	if len(devs) != 2 {
		t.Fatalf("len=%d, want 2", len(devs))
	}
	if devs[0].Address != "CUX0001" {
		t.Fatalf("devs[0].Address=%s", devs[0].Address)
	}
	if devs[1].Parent != "CUX0001" {
		t.Fatalf("devs[1].Parent=%s", devs[1].Parent)
	}

	// Verify the BIN-RPC caller was used.
	method, _, ok := loadArgs(bin)
	if !ok || method != "listDevices" {
		t.Fatalf("method=%s, want listDevices", method)
	}
}

// ---------------------------------------------------------------------------
// Init — delegates to Announcer (not a direct BIN-RPC call)
// ---------------------------------------------------------------------------

func TestCuxdBackendInitDispatchesThroughAnnouncer(t *testing.T) {
	t.Parallel()
	ann := &recordingAnnouncer{}
	bin := &fakeCaller{}
	b := NewCuxdBackend(bin, ann)

	if err := b.Init(context.Background(), "CUxD", "http://10.0.0.1:8129"); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if ann.inits != 1 {
		t.Fatalf("announcer.inits=%d, want 1", ann.inits)
	}
	// The BIN-RPC caller must NOT be hit during Init.
	if bin.called.Load() != 0 {
		t.Fatalf("bin caller was hit %d times during Init, want 0", bin.called.Load())
	}
}

func TestCuxdBackendDeinitDispatchesThroughAnnouncer(t *testing.T) {
	t.Parallel()
	ann := &recordingAnnouncer{}
	b := NewCuxdBackend(&fakeCaller{}, ann)
	if err := b.Deinit(context.Background(), "CUxD"); err != nil {
		t.Fatalf("Deinit: %v", err)
	}
	if ann.deinits != 1 {
		t.Fatalf("announcer.deinits=%d, want 1", ann.deinits)
	}
}

func TestCuxdBackendInitWithoutAnnouncerIsNoop(t *testing.T) {
	t.Parallel()
	b := NewCuxdBackend(&fakeCaller{}, nil)
	if err := b.Init(context.Background(), "CUxD", "http://cb"); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := b.Deinit(context.Background(), "CUxD"); err != nil {
		t.Fatalf("Deinit: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Ping — goes through BIN-RPC
// ---------------------------------------------------------------------------

func TestCuxdBackendPingDispatchesViaBINRPC(t *testing.T) {
	t.Parallel()
	bin := &fakeCaller{reply: nil}
	b := NewCuxdBackend(bin, nil)

	if err := b.Ping(context.Background(), "CUxD"); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	method, args, ok := loadArgs(bin)
	if !ok || method != "ping" {
		t.Fatalf("method=%s, want ping", method)
	}
	if len(args) != 1 || args[0] != "CUxD" {
		t.Fatalf("args=%v", args)
	}
}

// ---------------------------------------------------------------------------
// GetParamsetDescription — must go through BIN-RPC, not XML-RPC
// ---------------------------------------------------------------------------

func TestCuxdBackendGetParamsetDescriptionDispatchesViaBINRPC(t *testing.T) {
	t.Parallel()
	bin := &fakeCaller{reply: map[string]any{
		"STATE": map[string]any{"TYPE": "BOOL", "OPERATIONS": 3},
	}}
	// Pass a second fakeCaller as xmlrpc; it should never be called.
	// CuxdBackend has no xml field — the test just verifies bin is the
	// active transport.
	b := NewCuxdBackend(bin, nil)

	out, err := b.GetParamsetDescription(context.Background(), "CUX0001:1", hmenum.ParamsetKeyValues)
	if err != nil {
		t.Fatalf("GetParamsetDescription: %v", err)
	}
	if _, ok := out["STATE"]; !ok {
		t.Fatalf("STATE missing from %v", out)
	}

	method, args, ok := loadArgs(bin)
	if !ok || method != "getParamsetDescription" {
		t.Fatalf("method=%s, want getParamsetDescription", method)
	}
	if len(args) != 2 || args[0] != "CUX0001:1" || args[1] != "VALUES" {
		t.Fatalf("args=%v", args)
	}
}

// ---------------------------------------------------------------------------
// Capabilities contract
// ---------------------------------------------------------------------------

func TestCuxdBackendCapabilitiesContract(t *testing.T) {
	t.Parallel()
	b := NewCuxdBackend(&fakeCaller{}, nil)
	caps := b.Capabilities()

	if !caps.RPCCallback {
		t.Error("RPCCallback must be true for KindCUxD")
	}
	if !caps.PingPong {
		t.Error("PingPong must be true for KindCUxD")
	}
	if !caps.ListDevices {
		t.Error("ListDevices must be true for KindCUxD")
	}
	// CUxD has no JSON-RPC layer — firmware update is not supported.
	if caps.FirmwareUpdate {
		t.Error("FirmwareUpdate must be false for KindCUxD")
	}
	if caps.GetAllPrograms {
		t.Error("GetAllPrograms must be false for KindCUxD")
	}
	if caps.GetAllSysvars {
		t.Error("GetAllSysvars must be false for KindCUxD")
	}
}

// ---------------------------------------------------------------------------
// Link operations — all must return ErrUnsupported
// ---------------------------------------------------------------------------

func TestCuxdBackendLinkOperationsUnsupported(t *testing.T) {
	t.Parallel()
	b := NewCuxdBackend(&fakeCaller{}, nil)
	ctx := context.Background()

	if _, err := b.GetLinks(ctx, "CUX0001:1"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("GetLinks: want ErrUnsupported, got %v", err)
	}
	if _, err := b.GetLinkPeers(ctx, "CUX0001:1"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("GetLinkPeers: want ErrUnsupported, got %v", err)
	}
	if err := b.AddLink(ctx, "CUX0001:1", "CUX0002:1", "x", "y"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("AddLink: want ErrUnsupported, got %v", err)
	}
	if err := b.RemoveLink(ctx, "CUX0001:1", "CUX0002:1"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("RemoveLink: want ErrUnsupported, got %v", err)
	}
	if _, err := b.GetLinkParamsetDescription(ctx, "CUX0001:1", "CUX0002:1"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("GetLinkParamsetDescription: want ErrUnsupported, got %v", err)
	}
	if _, err := b.GetLinkParamset(ctx, "CUX0001:1", "CUX0002:1"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("GetLinkParamset: want ErrUnsupported, got %v", err)
	}
	if err := b.PutLinkParamset(ctx, "CUX0001:1", "CUX0002:1", map[string]any{}); !errors.Is(err, ErrUnsupported) {
		t.Errorf("PutLinkParamset: want ErrUnsupported, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// ReportValueUsage — ErrUnsupported (CUxD has no central-link concept)
// ---------------------------------------------------------------------------

func TestCuxdBackendReportValueUsageUnsupported(t *testing.T) {
	t.Parallel()
	b := NewCuxdBackend(&fakeCaller{}, nil)
	if err := b.ReportValueUsage(context.Background(), "CUX0001:1", "PRESS_SHORT", 1); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("expected ErrUnsupported, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// GetParamset / PutParamset / SetValue / GetValue go through BIN-RPC
// ---------------------------------------------------------------------------

func TestCuxdBackendGetParamsetViaBINRPC(t *testing.T) {
	t.Parallel()
	bin := &fakeCaller{reply: map[string]any{"STATE": false}}
	b := NewCuxdBackend(bin, nil)
	out, err := b.GetParamset(context.Background(), "CUX0001:1", hmenum.ParamsetKeyValues)
	if err != nil {
		t.Fatalf("GetParamset: %v", err)
	}
	if out["STATE"].(bool) != false {
		t.Fatalf("STATE=%v", out["STATE"])
	}
	method, _, ok := loadArgs(bin)
	if !ok || method != "getParamset" {
		t.Fatalf("method=%s", method)
	}
}

func TestCuxdBackendSetValueViaBINRPC(t *testing.T) {
	t.Parallel()
	bin := &fakeCaller{reply: nil}
	b := NewCuxdBackend(bin, nil)
	if err := b.SetValue(context.Background(), "CUX0001:1", hmenum.ParameterState, true, hmenum.CommandPriorityHigh, hmenum.CommandRxModeUnset); err != nil {
		t.Fatalf("SetValue: %v", err)
	}
	method, args, ok := loadArgs(bin)
	if !ok || method != "setValue" {
		t.Fatalf("method=%s", method)
	}
	// Priority must NOT leak to the wire.
	if len(args) != 3 {
		t.Fatalf("arg count=%d (priority must not be on wire)", len(args))
	}
	if args[0] != "CUX0001:1" || args[1] != string(hmenum.ParameterState) || args[2] != true {
		t.Fatalf("args=%v", args)
	}
}

func TestCuxdBackendGetValueViaBINRPC(t *testing.T) {
	t.Parallel()
	bin := &fakeCaller{reply: 42}
	b := NewCuxdBackend(bin, nil)
	v, err := b.GetValue(context.Background(), "CUX0001:1", hmenum.ParameterState)
	if err != nil {
		t.Fatalf("GetValue: %v", err)
	}
	if v.(int) != 42 {
		t.Fatalf("v=%v", v)
	}
	method, _, ok := loadArgs(bin)
	if !ok || method != "getValue" {
		t.Fatalf("method=%s", method)
	}
}

// ---------------------------------------------------------------------------
// Kind
// ---------------------------------------------------------------------------

func TestCuxdBackendKindIsKindCUxD(t *testing.T) {
	t.Parallel()
	b := NewCuxdBackend(&fakeCaller{}, nil)
	if b.Kind() != KindCUxD {
		t.Fatalf("Kind=%s, want cuxd", b.Kind())
	}
}

// ---------------------------------------------------------------------------
// Operations interface compliance
// ---------------------------------------------------------------------------

func TestCuxdBackendOperationsCompliance(t *testing.T) {
	var _ Operations = (*CuxdBackend)(nil)
}
