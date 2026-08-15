// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"log/slog"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/naming"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// TestIngestCachesChannelPostfixOnEveryChannelSharingAParameter pins the
// cached data-point name against the live recompute REST performs.
//
// The postfix is derived from the sibling channels that already carry
// the parameter, so naming a data point before it lands on its channel
// makes the first two channels of a multi-channel switch cache the same
// postfix-free name. Two Home Assistant entities of one device then
// share a name, and MQTT (cache) and REST (recompute) disagree.
func TestIngestCachesChannelPostfixOnEveryChannelSharingAParameter(t *testing.T) {
	t.Parallel()

	c, _ := central.New(central.Config{Name: "ccu-01"})
	p := NewDevicePipeline(c).
		WithVisibility(newProductionVisibilityGate()).
		WithNames(map[string]string{"0001ABCD": "Wohnzimmer"})

	b := &paramsetFakeOps{
		listDevicesFn: func(_ context.Context) ([]hmproto.DeviceDescription, error) {
			return []hmproto.DeviceDescription{
				{Address: "0001ABCD", Type: "HM-LC-Sw2"},
				{Address: "0001ABCD:1", Parent: "0001ABCD", Type: "SWITCH"},
				{Address: "0001ABCD:2", Parent: "0001ABCD", Type: "SWITCH"},
				{Address: "0001ABCD:3", Parent: "0001ABCD", Type: "SWITCH"},
			}, nil
		},
		getParamsetDescriptionFn: func(_ context.Context, addr string, key hmenum.ParamsetKey) (map[string]hmproto.ParameterData, error) {
			if key != hmenum.ParamsetKeyValues || addr == "0001ABCD" {
				return nil, nil
			}
			return map[string]hmproto.ParameterData{
				string(hmenum.ParameterState): {
					Type:       hmenum.ParameterTypeBool,
					Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
				},
			}, nil
		},
		getParamsetFn: func(_ context.Context, _ string, _ hmenum.ParamsetKey) (map[string]any, error) {
			return map[string]any{}, nil
		},
	}

	if err := p.IngestFromBackend(
		context.Background(), "HmIP-RF", hmenum.InterfaceHmIPRF,
		b, &fakeWriter{}, nil, slog.Default(),
	); err != nil {
		t.Fatalf("IngestFromBackend: %v", err)
	}

	dev, ok := c.ModelRegistry.Get("0001ABCD")
	if !ok {
		t.Fatal("device not in registry after IngestFromBackend")
	}
	for _, want := range []struct {
		address string
		postfix string
	}{
		{"0001ABCD:1", "ch1"},
		{"0001ABCD:2", "ch2"},
		{"0001ABCD:3", "ch3"},
	} {
		ch := dev.Channel(want.address)
		if ch == nil {
			t.Fatalf("channel %s missing", want.address)
		}
		dp := ch.Parameter(hmenum.ParameterState)
		if dp == nil {
			t.Fatalf("%s: STATE data point missing", want.address)
		}
		named, ok := dp.(interface{ NameData() naming.NameData })
		if !ok {
			t.Fatalf("%s: STATE data point caches no name", want.address)
		}
		if got := named.NameData().ChannelPostfix; got != want.postfix {
			t.Errorf("%s: cached ChannelPostfix=%q want %q", want.address, got, want.postfix)
		}
		// The live recompute REST performs must agree with the cache.
		live := device.BuildDataPointName(ch, string(hmenum.ParameterState), "")
		if live.ChannelPostfix != want.postfix {
			t.Errorf("%s: live ChannelPostfix=%q want %q", want.address, live.ChannelPostfix, want.postfix)
		}
		if named.NameData().FullName() != live.FullName() {
			t.Errorf("%s: cached FullName=%q but live recompute yields %q",
				want.address, named.NameData().FullName(), live.FullName())
		}
	}
}

// TestIngestStampsTheDeviceModelOnEveryDataPoint pins the device-aware
// quantity chain through the real bring-up. generic.Spec.DeviceModel
// documents itself as pipeline-supplied, and Quantity()/Signature()
// silently degrade to the parameter-only path when it is empty — the
// HmIP-SWDO.STATE → window override and its twelve siblings become
// unreachable, and signatures collide across device models.
func TestIngestStampsTheDeviceModelOnEveryDataPoint(t *testing.T) {
	t.Parallel()

	c, _ := central.New(central.Config{Name: "ccu-01"})
	p := NewDevicePipeline(c).
		WithVisibility(newProductionVisibilityGate()).
		WithNames(map[string]string{"0001ABCD": "Fenster"})

	b := &paramsetFakeOps{
		listDevicesFn: func(_ context.Context) ([]hmproto.DeviceDescription, error) {
			return []hmproto.DeviceDescription{
				{Address: "0001ABCD", Type: "HmIP-SWDO"},
				{Address: "0001ABCD:1", Parent: "0001ABCD", Type: "SHUTTER_CONTACT"},
			}, nil
		},
		getParamsetDescriptionFn: func(_ context.Context, addr string, key hmenum.ParamsetKey) (map[string]hmproto.ParameterData, error) {
			if key != hmenum.ParamsetKeyValues || addr != "0001ABCD:1" {
				return nil, nil
			}
			return map[string]hmproto.ParameterData{
				string(hmenum.ParameterState): {
					Type:       hmenum.ParameterTypeBool,
					Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
				},
			}, nil
		},
		getParamsetFn: func(_ context.Context, _ string, _ hmenum.ParamsetKey) (map[string]any, error) {
			return map[string]any{}, nil
		},
	}

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
	dp := dev.Channel("0001ABCD:1").Parameter(hmenum.ParameterState)
	if dp == nil {
		t.Fatal("STATE data point missing")
	}
	q, ok := dp.(interface{ Quantity() hmenum.Quantity })
	if !ok {
		t.Fatalf("STATE data point %T exposes no Quantity", dp)
	}
	if got := q.Quantity(); got != hmenum.QuantityWindow {
		t.Errorf("Quantity() = %q, want %q — the per-model override needs the device model",
			got, hmenum.QuantityWindow)
	}
}
