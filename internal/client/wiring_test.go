// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package client

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

type fakeCall struct {
	calls atomic.Int32
	reply any
	err   error
}

func (f *fakeCall) Call(_ context.Context, _ string, _ []any) (any, error) {
	f.calls.Add(1)
	return f.reply, f.err
}

func TestBackendCallerDispatchesViaReliability(t *testing.T) {
	ic, err := New(Config{
		CentralName: "ccu-01", Interface: hmenum.InterfaceHmIPRF,
		Caller: &fakeCall{reply: "ok"},
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	defer ic.Close()

	bc := NewBackendCaller(ic, hmenum.CommandPriorityLow)
	out, err := bc.Call(context.Background(), "listMethods")
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if out != "ok" {
		t.Fatalf("out=%v", out)
	}
}

func TestValueWriterRoutesByCentralInterface(t *testing.T) {
	w := NewValueWriter()
	if err := w.SetValue(context.Background(), "ccu-01", "HmIP-RF", "0001:1",
		hmenum.ParameterState, true, hmenum.CommandPriorityHigh); !errors.Is(err, ErrNoBackend) {
		t.Fatalf("unwired err=%v", err)
	}
	// Use a real backend with a fake caller.
	bc := backends.NewCcuBackend(&fakeCcuCall{}, nil, nil)
	w.Register("ccu-01", "HmIP-RF", bc)
	if err := w.SetValue(context.Background(), "ccu-01", "HmIP-RF", "0001:1",
		hmenum.ParameterState, true, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("set: %v", err)
	}
	if _, ok := w.Backend("ccu-01", "HmIP-RF"); !ok {
		t.Fatal("backend lookup failed")
	}
	w.Deregister("ccu-01", "HmIP-RF")
	if _, ok := w.Backend("ccu-01", "HmIP-RF"); ok {
		t.Fatal("expected dropped")
	}
}

type fakeCcuCall struct{}

func (f *fakeCcuCall) Call(_ context.Context, _ string, _ ...any) (any, error) { return nil, nil }

func (f *fakeCcuCall) CallAt(
	_ context.Context, _ hmenum.CommandPriority, _ string, _ ...any,
) (any, error) {
	return nil, nil
}

// stubRetrier2 records CancelInterface calls; lives here to avoid a
// name collision with the stubRetrier in write_options_test.go (same
// package but different names).
type stubRetrier2 struct {
	cancelInterfaceCalls int
	cancelDeviceCalls    []string
}

func (r *stubRetrier2) CancelDevice(addr string) int {
	r.cancelDeviceCalls = append(r.cancelDeviceCalls, addr)
	return 1
}

func (r *stubRetrier2) CancelInterface() int {
	r.cancelInterfaceCalls++
	return r.cancelInterfaceCalls
}

// TestValueWriterCancelInterfaceDelegatesToRetrier verifies that
// CancelInterface on a ValueWriter forwards to the installed retrier
// and returns 0 when no retrier is installed (Task #33).
func TestValueWriterCancelInterfaceDelegatesToRetrier(t *testing.T) {
	// Without retrier: must return 0 and not panic.
	w := NewValueWriter()
	if got := w.CancelInterface("ccu", "HmIP-RF"); got != 0 {
		t.Fatalf("no retrier: CancelInterface = %d, want 0", got)
	}

	// With retrier: must forward to its CancelInterface.
	r := &stubRetrier2{}
	w.SetRetrier(r)
	got := w.CancelInterface("ccu", "HmIP-RF")
	if got != 1 {
		t.Fatalf("with retrier: CancelInterface = %d, want 1", got)
	}
	if r.cancelInterfaceCalls != 1 {
		t.Fatalf("retrier.CancelInterface called %d times, want 1", r.cancelInterfaceCalls)
	}
}
