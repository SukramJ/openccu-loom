// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// NoPushUpdates pipeline invariant: when the backend reports
// Capabilities.RPCCallback==false the pipeline must stamp
// Spec.NoPushUpdates=true on every produced DP, VALUES and MASTER
// alike. The mirror case (RPCCallback==true) must leave the flag
// unset. The pipeline is the only place that can observe the backend
// capability at ingest time, so this is the only site where the stamp
// can be pinned.

package adapter

import (
	"context"
	"log/slog"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// noPushFakeOps wraps paramsetFakeOps but reports a Capabilities profile
// without RPCCallback — emulating a poll-only backend.
type noPushFakeOps struct {
	paramsetFakeOps
}

func (n *noPushFakeOps) Capabilities() backends.Capabilities {
	c := backends.CapabilityFor(backends.KindCCU)
	c.RPCCallback = false
	return c
}

// newNoPushHydratingBackend returns a noPushFakeOps that lists one device
// with one channel, exposes a single LEVEL float parameter in VALUES and a
// SINGLE_EXECUTION bool in MASTER, and returns empty value maps.
func newNoPushHydratingBackend() *noPushFakeOps {
	return &noPushFakeOps{
		paramsetFakeOps: paramsetFakeOps{
			listDevicesFn: func(_ context.Context) ([]hmproto.DeviceDescription, error) {
				return []hmproto.DeviceDescription{
					{Address: "NPUSH001", Type: "HmIP-STH"},
					{Address: "NPUSH001:1", Parent: "NPUSH001", Type: "LEVEL"},
				}, nil
			},
			getParamsetDescriptionFn: func(_ context.Context, _ string, key hmenum.ParamsetKey) (map[string]hmproto.ParameterData, error) {
				switch key { //nolint:exhaustive // only the paramset keys relevant to this test fixture
				case hmenum.ParamsetKeyValues:
					return map[string]hmproto.ParameterData{
						string(hmenum.ParameterLevel): {
							Type:       hmenum.ParameterTypeFloat,
							Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
						},
					}, nil
				case hmenum.ParamsetKeyMaster:
					return map[string]hmproto.ParameterData{
						"ARR_TIMEOUT": {
							Type:       hmenum.ParameterTypeFloat,
							Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
						},
					}, nil
				}
				return nil, nil
			},
			getParamsetFn: func(_ context.Context, _ string, _ hmenum.ParamsetKey) (map[string]any, error) {
				return map[string]any{}, nil
			},
		},
	}
}

// TestPipelineNoPushUpdatesForPollOnlyBackend verifies that when the pipeline
// ingests devices from a backend whose Capabilities.RPCCallback is false,
// every VALUES and MASTER data point carries NoPushUpdates=true.
func TestPipelineNoPushUpdatesForPollOnlyBackend(t *testing.T) {
	t.Parallel()

	c, _ := central.New(central.Config{Name: "nopush-01"})
	p := NewDevicePipeline(c)
	b := newNoPushHydratingBackend()

	if err := p.IngestFromBackend(
		context.Background(), "HmIP-RF", hmenum.InterfaceHmIPRF,
		b, &fakeWriter{}, nil, slog.Default(),
	); err != nil {
		t.Fatalf("IngestFromBackend: %v", err)
	}

	dev, ok := c.ModelRegistry.Get("NPUSH001")
	if !ok {
		t.Fatal("device NPUSH001 not in registry")
	}
	ch := dev.Channel("NPUSH001:1")
	if ch == nil {
		t.Fatal("channel NPUSH001:1 not found")
	}

	valDP := ch.Parameter(hmenum.ParameterLevel)
	if valDP == nil {
		t.Fatal("LEVEL data point not found on VALUES paramset")
	}
	pdp, ok := valDP.(*generic.Float)
	if !ok {
		t.Fatalf("LEVEL DP is not a *generic.Float (type %T)", valDP)
	}
	if !pdp.NoPushUpdates {
		t.Error("LEVEL (VALUES) NoPushUpdates must be true when RPCCallback=false")
	}

	masterDP := ch.MasterParameter("ARR_TIMEOUT")
	if masterDP == nil {
		t.Fatal("ARR_TIMEOUT data point not found on MASTER paramset")
	}
	mpdp, ok := masterDP.(*generic.Float)
	if !ok {
		t.Fatalf("ARR_TIMEOUT DP is not a *generic.Float (type %T)", masterDP)
	}
	if !mpdp.NoPushUpdates {
		t.Error("ARR_TIMEOUT (MASTER) NoPushUpdates must be true when RPCCallback=false")
	}
}

// TestPipelineNoPushUpdatesNotSetForPushBackend verifies that a backend
// with Capabilities.RPCCallback==true does NOT set NoPushUpdates.
func TestPipelineNoPushUpdatesNotSetForPushBackend(t *testing.T) {
	t.Parallel()

	c, _ := central.New(central.Config{Name: "ccu-push-01"})
	p := NewDevicePipeline(c)
	// newHydratingBackend returns paramsetFakeOps with Capabilities=CapabilityFor(KindCCU)
	// → RPCCallback is true (push-capable).
	b := newHydratingBackend()

	if err := p.IngestFromBackend(
		context.Background(), "HmIP-RF", hmenum.InterfaceHmIPRF,
		b, &fakeWriter{}, nil, slog.Default(),
	); err != nil {
		t.Fatalf("IngestFromBackend: %v", err)
	}

	dev, ok := c.ModelRegistry.Get("0001ABCD")
	if !ok {
		t.Fatal("device not in registry")
	}
	ch := dev.Channel("0001ABCD:1")
	if ch == nil {
		t.Fatal("channel not found")
	}

	valDP := ch.Parameter(hmenum.ParameterLevel)
	if valDP == nil {
		t.Fatal("LEVEL data point not found")
	}
	pdp, ok := valDP.(*generic.Float)
	if !ok {
		t.Fatalf("LEVEL DP is not a *generic.Float (type %T)", valDP)
	}
	if pdp.NoPushUpdates {
		t.Error("LEVEL (VALUES) NoPushUpdates must be false when RPCCallback=true")
	}
}
