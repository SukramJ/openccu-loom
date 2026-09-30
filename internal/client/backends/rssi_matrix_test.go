// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package backends

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// TestCcuBackendRSSIInfoDecodesNestedMatrix verifies the wire call is
// exactly `rssiInfo()` without arguments and that the nested struct —
// device serial → partner serial → [A hears B, B hears A] — decodes with
// the no-information marker kept as-is and integer widths normalised.
func TestCcuBackendRSSIInfoDecodesNestedMatrix(t *testing.T) {
	t.Parallel()
	x := &fakeCaller{reply: map[string]any{
		"DEV0000001": map[string]any{
			"GW00000001": []any{-65, int32(-70)},
			"GW00000002": []any{int64(RSSINoInformation), -88},
			"BROKEN":     []any{-1},
		},
		"GW00000001": map[string]any{
			"DEV0000001": []any{float64(-70), -65},
		},
	}}
	b := NewCcuBackend(x, nil, nil)
	got, err := b.RSSIInfo(context.Background())
	if err != nil {
		t.Fatalf("RSSIInfo: %v", err)
	}
	call, _ := x.lastArg.Load().([]any)
	if method, _ := call[0].(string); method != "rssiInfo" {
		t.Fatalf("method=%v, want rssiInfo", call[0])
	}
	if args, _ := call[1].([]any); len(args) != 0 {
		t.Fatalf("args=%v, want none", args)
	}
	if v := got["DEV0000001"]["GW00000001"]; v != [2]int{-65, -70} {
		t.Errorf("DEV/GW1 = %v, want [-65 -70]", v)
	}
	if v := got["DEV0000001"]["GW00000002"]; v != [2]int{RSSINoInformation, -88} {
		t.Errorf("DEV/GW2 = %v, want [65536 -88]", v)
	}
	if _, ok := got["DEV0000001"]["BROKEN"]; ok {
		t.Error("malformed partner pair must be skipped")
	}
	if v := got["GW00000001"]["DEV0000001"]; v != [2]int{-70, -65} {
		t.Errorf("GW1/DEV = %v, want [-70 -65]", v)
	}
}

// TestCcuBackendRSSIInfoRejectsWrongShape verifies a top-level answer that
// is not a struct fails the call instead of yielding an empty matrix.
func TestCcuBackendRSSIInfoRejectsWrongShape(t *testing.T) {
	t.Parallel()
	b := NewCcuBackend(&fakeCaller{reply: []any{"x"}}, nil, nil)
	if _, err := b.RSSIInfo(context.Background()); err == nil {
		t.Fatal("expected a decode error for a non-struct answer")
	}
	b = NewCcuBackend(&fakeCaller{reply: map[string]any{"DEV": "x"}}, nil, nil)
	if _, err := b.RSSIInfo(context.Background()); err == nil {
		t.Fatal("expected a decode error for a non-struct device row")
	}
}

// TestCcuBackendRSSIInfoPropagatesFault verifies an XML-RPC fault reaches
// the caller.
func TestCcuBackendRSSIInfoPropagatesFault(t *testing.T) {
	t.Parallel()
	fault := &hmerr.XMLRPCFault{Code: -1, Message: "unknown method"}
	b := NewCcuBackend(&fakeCaller{err: fault}, nil, nil)
	_, err := b.RSSIInfo(context.Background())
	var got *hmerr.XMLRPCFault
	if !errors.As(err, &got) || got.Code != -1 {
		t.Fatalf("expected *hmerr.XMLRPCFault{Code: -1}, got %v", err)
	}
	if _, err := NewCcuBackend(nil, nil, nil).RSSIInfo(context.Background()); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unwired: expected ErrUnsupported, got %v", err)
	}
}

// TestCcuBackendSetBidcosInterfaceDispatchesXMLRPC pins the argument order
// the daemon expects: device address, gateway serial, roaming flag.
func TestCcuBackendSetBidcosInterfaceDispatchesXMLRPC(t *testing.T) {
	t.Parallel()
	x := &fakeCaller{}
	b := NewCcuBackend(x, nil, nil)
	if err := b.SetBidcosInterface(context.Background(), "DEV0000001", "GW00000002", true); err != nil {
		t.Fatalf("SetBidcosInterface: %v", err)
	}
	call, _ := x.lastArg.Load().([]any)
	if method, _ := call[0].(string); method != "setBidcosInterface" {
		t.Fatalf("method=%v, want setBidcosInterface", call[0])
	}
	args, _ := call[1].([]any)
	if len(args) != 3 || args[0] != "DEV0000001" || args[1] != "GW00000002" || args[2] != true {
		t.Fatalf("args=%v, want [DEV0000001 GW00000002 true]", args)
	}
}

// TestCcuBackendSetBidcosInterfacePropagatesFault verifies a daemon fault
// (e.g. an unknown gateway serial) reaches the caller.
func TestCcuBackendSetBidcosInterfacePropagatesFault(t *testing.T) {
	t.Parallel()
	fault := &hmerr.XMLRPCFault{Code: -2, Message: "unknown interface"}
	b := NewCcuBackend(&fakeCaller{err: fault}, nil, nil)
	err := b.SetBidcosInterface(context.Background(), "DEV", "GW", false)
	var got *hmerr.XMLRPCFault
	if !errors.As(err, &got) || got.Code != -2 {
		t.Fatalf("expected *hmerr.XMLRPCFault{Code: -2}, got %v", err)
	}
	if err := NewCcuBackend(nil, nil, nil).SetBidcosInterface(context.Background(), "DEV", "GW", false); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unwired: expected ErrUnsupported, got %v", err)
	}
}

// TestLiteBackendRSSIMatrixCallsDispatchXMLRPC pins the lite path to the
// same wire calls the CCU backend makes, and its unwired form to
// ErrNotWired.
func TestLiteBackendRSSIMatrixCallsDispatchXMLRPC(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	c := &liteScriptedCaller{replies: map[string]any{
		"rssiInfo": map[string]any{"DEV": map[string]any{"GW": []any{-60, RSSINoInformation}}},
	}}
	b := NewLiteBackend(hmenum.InterfaceBidCosRF, c, nil)
	got, err := b.RSSIInfo(ctx)
	if err != nil {
		t.Fatalf("RSSIInfo: %v", err)
	}
	if got["DEV"]["GW"] != [2]int{-60, RSSINoInformation} {
		t.Fatalf("matrix = %v", got)
	}
	if err := b.SetBidcosInterface(ctx, "DEV", "GW", false); err != nil {
		t.Fatalf("SetBidcosInterface: %v", err)
	}
	calls := c.recorded()
	if len(calls) != 2 || calls[0].method != "rssiInfo" || len(calls[0].args) != 0 {
		t.Fatalf("calls = %+v, want rssiInfo() first", calls)
	}
	if calls[1].method != "setBidcosInterface" || len(calls[1].args) != 3 ||
		calls[1].args[0] != "DEV" || calls[1].args[1] != "GW" || calls[1].args[2] != false {
		t.Fatalf("calls[1] = %+v, want setBidcosInterface(DEV, GW, false)", calls[1])
	}
	unwired := NewLiteBackend(hmenum.InterfaceBidCosRF, nil, nil)
	if _, err := unwired.RSSIInfo(ctx); !errors.Is(err, ErrNotWired) {
		t.Fatalf("unwired RSSIInfo err = %v, want ErrNotWired", err)
	}
	if err := unwired.SetBidcosInterface(ctx, "DEV", "GW", false); !errors.Is(err, ErrNotWired) {
		t.Fatalf("unwired SetBidcosInterface err = %v, want ErrNotWired", err)
	}
}

// TestNonCCUBackendsRSSIMatrixUnsupported verifies CUxD and Homegear refuse
// both calls with ErrUnsupported and never reach the wire.
func TestNonCCUBackendsRSSIMatrixUnsupported(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cx := &fakeCaller{}
	hx := &fakeCaller{}
	for name, b := range map[string]Operations{
		"cuxd":     NewCuxdBackend(cx, nil),
		"homegear": NewHomegearBackend(hx, nil),
	} {
		if _, err := b.RSSIInfo(ctx); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s RSSIInfo err = %v, want ErrUnsupported", name, err)
		}
		if err := b.SetBidcosInterface(ctx, "DEV", "GW", false); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s SetBidcosInterface err = %v, want ErrUnsupported", name, err)
		}
	}
	if cx.called.Load() != 0 || hx.called.Load() != 0 {
		t.Fatal("unsupported backends must not reach the wire")
	}
}
