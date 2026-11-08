// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/store/visibility"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// TestIngestFromBackendPublishesOnlyComposedDevices pins the invariant the
// model registry gives every north-bound reader: a device it hands out is
// fully composed. The usage marks (custom-DP suppression, ignored and hidden
// parameters, operation-mode gating) run at the end of finishIngest; a
// reader that sees the device while its paramsets are still being hydrated
// reads every suppressed parameter as `usage=data_point`.
//
// That reader exists: `GET /snapshot` walks the registry with no readiness
// gate, and a client re-bootstrapping right after a daemon restart samples
// exactly this window. It then spawns entities for parameters a cold start
// never exposes, and they turn into orphans at the next cold start.
//
// The observation is taken from inside the backend, at the moment the
// pipeline asks for the channel's VALUES description: that is mid-hydration
// by construction, with no timing involved.
func TestIngestFromBackendPublishesOnlyComposedDevices(t *testing.T) {
	t.Parallel()

	const devAddr = "AABBCC51"
	c, _ := central.New(central.Config{Name: "ccu-pub-01"})
	p := NewDevicePipeline(c).WithVisibility(visibility.NewRegistry())

	var observedMidHydration atomic.Bool
	var observed atomic.Bool
	b := backendWithParams(devAddr, "HmIP-STH", "SOME_CHANNEL", map[string]hmproto.ParameterData{
		string(hmenum.ParameterInhibit): {
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
	})
	inner := b.getParamsetDescriptionFn
	b.getParamsetDescriptionFn = func(ctx context.Context, address string, key hmenum.ParamsetKey) (map[string]hmproto.ParameterData, error) {
		if _, ok := c.ModelRegistry.Get(devAddr); ok {
			observedMidHydration.Store(true)
		}
		observed.Store(true)
		return inner(ctx, address, key)
	}

	if err := p.IngestFromBackend(
		context.Background(), "HmIP-RF", hmenum.InterfaceHmIPRF,
		b, &fakeWriter{}, nil, slog.Default(),
	); err != nil {
		t.Fatalf("IngestFromBackend: %v", err)
	}
	if !observed.Load() {
		t.Fatal("backend never asked for a paramset description — the probe did not run")
	}
	if observedMidHydration.Load() {
		t.Fatal("device was readable from the model registry while its paramsets were still being hydrated")
	}

	dev, ok := c.ModelRegistry.Get(devAddr)
	if !ok {
		t.Fatal("device not published after a successful ingest")
	}
	ch := dev.Channel(devAddr + ":1")
	if ch == nil {
		t.Fatal("channel not found")
	}
	dp := ch.Parameter(hmenum.ParameterInhibit)
	if dp == nil {
		t.Fatal("INHIBIT must be stored as a DP")
	}
	// The published device carries its marks: the ignored parameter never
	// reads as a creatable data point once a reader can reach it.
	assertForcedUsageIgnored(t, dp, "INHIBIT")
}

// TestIngestNewDevicesPublishesOnlyComposedDevices is the hot-plug twin: a
// device announced at runtime goes through the same hydrate-then-mark
// sequence and must stay invisible until it is complete.
func TestIngestNewDevicesPublishesOnlyComposedDevices(t *testing.T) {
	t.Parallel()

	const devAddr = "AABBCC52"
	c, _ := central.New(central.Config{Name: "ccu-pub-02"})
	p := NewDevicePipeline(c).WithVisibility(visibility.NewRegistry())

	var observedMidHydration atomic.Bool
	var observed atomic.Bool
	b := backendWithParams(devAddr, "HmIP-STH", "SOME_CHANNEL", map[string]hmproto.ParameterData{
		string(hmenum.ParameterInhibit): {
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
	})
	inner := b.getParamsetDescriptionFn
	b.getParamsetDescriptionFn = func(ctx context.Context, address string, key hmenum.ParamsetKey) (map[string]hmproto.ParameterData, error) {
		if _, ok := c.ModelRegistry.Get(devAddr); ok {
			observedMidHydration.Store(true)
		}
		observed.Store(true)
		return inner(ctx, address, key)
	}
	descs, err := b.ListDevices(context.Background())
	if err != nil {
		t.Fatalf("ListDevices: %v", err)
	}

	addrs, err := p.IngestNewDevices(
		context.Background(), "HmIP-RF", hmenum.InterfaceHmIPRF,
		b, &fakeWriter{}, nil, descs, slog.Default(),
	)
	if err != nil {
		t.Fatalf("IngestNewDevices: %v", err)
	}
	if len(addrs) != 1 || addrs[0] != devAddr {
		t.Fatalf("IngestNewDevices returned %v, want [%s]", addrs, devAddr)
	}
	if !observed.Load() {
		t.Fatal("backend never asked for a paramset description — the probe did not run")
	}
	if observedMidHydration.Load() {
		t.Fatal("hot-plugged device was readable from the model registry while its paramsets were still being hydrated")
	}
	dev, ok := c.ModelRegistry.Get(devAddr)
	if !ok {
		t.Fatal("device not published after a successful hot-plug ingest")
	}
	if dp := dev.Channel(devAddr + ":1").Parameter(hmenum.ParameterInhibit); dp == nil {
		t.Fatal("INHIBIT must be stored as a DP")
	} else {
		assertForcedUsageIgnored(t, dp, "INHIBIT")
	}
}
