// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package cover

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// wdStubWriter records the wire LEVEL value that the WindowDrive
// path emitted, so tests can assert the -0.005 / 0.0 sentinel was
// applied correctly.
type wdStubWriter struct {
	lastValue any
}

func (w *wdStubWriter) SetValue(_ context.Context, _ string, _ hmenum.Parameter, value any, _ hmenum.CommandPriority) error {
	w.lastValue = value
	return nil
}

func (w *wdStubWriter) PutParamset(_ context.Context, _ string, _ hmenum.ParamsetKey, _ map[string]any, _ hmenum.CommandPriority) error {
	return nil
}

// newWindowDriveCover assembles a HM-Sec-Win cover with a LEVEL DP on
// the channel and a stub writer wired in.
func newWindowDriveCover(t *testing.T) (*Cover, *wdStubWriter) {
	t.Helper()
	dev := device.New(device.Config{
		InterfaceID: "BidCos-RF",
		Interface:   hmenum.InterfaceBidCosRF,
		Address:     "OEQ0001",
		Model:       "HM-Sec-Win",
		Name:        "Fenster",
	})
	ch := dev.AddChannel("OEQ0001:1", 1, "", hmenum.ParamsetKeyValues)
	w := &wdStubWriter{}
	dp := generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "BidCos-RF",
			ChannelAddress: "OEQ0001:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "LEVEL",
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
			Min:        json.RawMessage(`-0.005`),
			Max:        json.RawMessage(`1.0`),
		},
		Writer: w,
		Kind:   generic.KindNumberFloat,
	})
	ch.Put(dp)
	cov := New(Config{
		Channel:      ch,
		Writer:       w,
		Capabilities: custom.CoverCapabilities{SupportsPosition: true, SupportsStop: true},
		WindowDrive:  true,
	})
	return cov, w
}

func TestWindowDriveSetPositionFullyClosedWritesNegativeSentinel(t *testing.T) {
	t.Parallel()
	cov, w := newWindowDriveCover(t)
	if err := cov.SetPosition(context.Background(), 0.0, hmenum.CommandPriorityLow); err != nil {
		t.Fatal(err)
	}
	got, ok := w.lastValue.(float64)
	if !ok || got != -0.005 {
		t.Fatalf("SetPosition(0.0) wire value = %v (%T), want -0.005 (float64)", w.lastValue, w.lastValue)
	}
}

func TestWindowDriveSetPositionSlightlyOpenSnapsToZero(t *testing.T) {
	t.Parallel()
	cov, w := newWindowDriveCover(t)
	// 0 < target ≤ 0.01 must collapse to wire 0.0 — gasket-safe.
	if err := cov.SetPosition(context.Background(), 0.005, hmenum.CommandPriorityLow); err != nil {
		t.Fatal(err)
	}
	got, ok := w.lastValue.(float64)
	if !ok || got != 0.0 {
		t.Fatalf("SetPosition(0.005) wire value = %v, want 0.0", w.lastValue)
	}
}

func TestWindowDriveSetPositionPassThroughForOpen(t *testing.T) {
	t.Parallel()
	cov, w := newWindowDriveCover(t)
	if err := cov.SetPosition(context.Background(), 0.5, hmenum.CommandPriorityLow); err != nil {
		t.Fatal(err)
	}
	got, ok := w.lastValue.(float64)
	if !ok || got != 0.5 {
		t.Fatalf("SetPosition(0.5) wire value = %v, want 0.5", w.lastValue)
	}
}

func TestWindowDrivePositionRemapsClosedSentinel(t *testing.T) {
	t.Parallel()
	cov, _ := newWindowDriveCover(t)
	cov.OnLevel(-0.005)
	pos, ok := cov.Position()
	if !ok {
		t.Fatal("Position not observed")
	}
	if pos.Level() != 0.0 {
		t.Fatalf("wire -0.005 must surface as 0 (closed), got %v", pos.Level())
	}
}

func TestWindowDrivePositionRemapsZeroToSlightlyOpen(t *testing.T) {
	t.Parallel()
	cov, _ := newWindowDriveCover(t)
	cov.OnLevel(0.0)
	pos, ok := cov.Position()
	if !ok {
		t.Fatal("Position not observed")
	}
	if pos.Level() != 0.01 {
		t.Fatalf("wire 0.0 must surface as 0.01 (slightly open), got %v", pos.Level())
	}
}

func TestNonWindowDriveCoverIsUnaffected(t *testing.T) {
	t.Parallel()
	// Standard cover (HmIP-BROLL): SetPosition(0) writes 0.0, not -0.005.
	dev := device.New(device.Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "VCU0001",
		Model:       "HmIP-BROLL",
		Name:        "Rolladen",
	})
	ch := dev.AddChannel("VCU0001:4", 4, "", hmenum.ParamsetKeyValues)
	w := &wdStubWriter{}
	dp := generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: "VCU0001:4",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "LEVEL",
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
			Min:        json.RawMessage(`0`),
			Max:        json.RawMessage(`1.0`),
		},
		Writer: w,
		Kind:   generic.KindNumberFloat,
	})
	ch.Put(dp)
	cov := New(Config{
		Channel:      ch,
		Writer:       w,
		Capabilities: custom.CoverCapabilities{SupportsPosition: true, SupportsStop: true},
		// WindowDrive: false (default)
	})
	if err := cov.SetPosition(context.Background(), 0.0, hmenum.CommandPriorityLow); err != nil {
		t.Fatal(err)
	}
	if got := w.lastValue.(float64); got != 0.0 {
		t.Fatalf("non-WindowDrive SetPosition(0) must write 0.0, got %v", got)
	}
}
