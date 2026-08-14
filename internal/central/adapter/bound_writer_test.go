// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// channelWriterAdapter tests reuse the fakeOperations stub defined in
// device_admin_unpair_test.go (same package) since both live in
// package adapter and the interface is large.

// putsCapturingFakeOps wraps fakeOperations to capture PutParamset calls.
type putsCapturingFakeOps struct {
	fakeOperations
	putErr   error
	lastAddr string
	lastKey  hmenum.ParamsetKey
	lastVals map[string]any
	putCalls int
}

func (f *putsCapturingFakeOps) PutParamset(
	_ context.Context, address string, key hmenum.ParamsetKey, values map[string]any,
	_ hmenum.CommandPriority, _ hmenum.CommandRxMode,
) error {
	f.putCalls++
	f.lastAddr = address
	f.lastKey = key
	f.lastVals = values
	return f.putErr
}

// TestChannelWriterAdapterPutParamsetRoutes verifies that PutParamset
// calls through to the underlying backend with the correct arguments.
func TestChannelWriterAdapterPutParamsetRoutes(t *testing.T) {
	t.Parallel()

	bw := newBoundWriter("ccu-01", "HmIP-RF", &fakeWriter{})
	fakeB := &putsCapturingFakeOps{}
	a := &channelWriterAdapter{bw: bw, backend: fakeB}

	vals := map[string]any{"TEMPERATURE": 21.5}
	if err := a.PutParamset(
		context.Background(),
		"0001ABCD:1",
		hmenum.ParamsetKeyMaster,
		vals,
		hmenum.CommandPriorityHigh,
	); err != nil {
		t.Fatalf("PutParamset: %v", err)
	}

	if fakeB.putCalls != 1 {
		t.Fatalf("putCalls = %d, want 1", fakeB.putCalls)
	}
	if fakeB.lastAddr != "0001ABCD:1" {
		t.Errorf("addr = %q, want 0001ABCD:1", fakeB.lastAddr)
	}
	if fakeB.lastKey != hmenum.ParamsetKeyMaster {
		t.Errorf("key = %v, want ParamsetKeyMaster", fakeB.lastKey)
	}
	if fakeB.lastVals["TEMPERATURE"] != 21.5 {
		t.Errorf("value = %v, want 21.5", fakeB.lastVals["TEMPERATURE"])
	}
}

// TestChannelWriterAdapterPutParamsetPropagatesError verifies that a
// backend error is surfaced to the caller.
func TestChannelWriterAdapterPutParamsetPropagatesError(t *testing.T) {
	t.Parallel()

	bw := newBoundWriter("ccu-01", "HmIP-RF", &fakeWriter{})
	fakeB := &putsCapturingFakeOps{putErr: errors.New("backend down")}
	a := &channelWriterAdapter{bw: bw, backend: fakeB}

	err := a.PutParamset(context.Background(), "addr:1", hmenum.ParamsetKeyMaster, nil, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Fatal("expected error from backend")
	}
}

// TestChannelWriterAdapterSetValueRoutes verifies that SetValue routes
// through the bound writer.
func TestChannelWriterAdapterSetValueRoutes(t *testing.T) {
	t.Parallel()

	fw := &fakeWriter{}
	bw := newBoundWriter("ccu-01", "HmIP-RF", fw)
	fakeB := &putsCapturingFakeOps{}
	a := &channelWriterAdapter{bw: bw, backend: fakeB}

	if err := a.SetValue(context.Background(), "0001ABCD:1", hmenum.ParameterState, true, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetValue: %v", err)
	}
	if fw.calls.Load() != 1 {
		t.Fatalf("fakeWriter calls = %d, want 1", fw.calls.Load())
	}
}
