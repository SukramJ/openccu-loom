// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// registryWithChannel builds a two-central registry where only ccu-01
// owns the device, so the central-scope checks have something to reject.
func registryWithChannel(t *testing.T) *central.Registry {
	t.Helper()
	reg := central.NewRegistry()
	for _, name := range []string{"ccu-01", "ccu-02"} {
		c, err := central.New(central.Config{Name: name})
		if err != nil {
			t.Fatalf("central.New(%s): %v", name, err)
		}
		if err := reg.Register(c); err != nil {
			t.Fatalf("reg.Register(%s): %v", name, err)
		}
	}
	owner, _ := reg.Get("ccu-01")
	dev := device.New(device.Config{
		InterfaceID: "HmIP-RF", Interface: hmenum.InterfaceHmIPRF,
		Address: "0001ABCD", Model: "HmIP-STH", Name: "Flur",
	})
	dev.AddChannel("0001ABCD:1", 1, "CLIMATE_TRANSCEIVER", hmenum.ParamsetKeyValues)
	owner.ModelRegistry.Put(dev)
	return reg
}

// TestDevicesAdapterChannelMeta pins the metadata the export endpoint
// stamps onto its snapshot. Without it the exported configuration
// carried an empty model and channel_type and no central scope.
func TestDevicesAdapterChannelMeta(t *testing.T) {
	t.Parallel()
	a := NewDevicesAdapter(registryWithChannel(t))

	devAddr, model, chType, centralName, ok := a.ChannelMeta("0001ABCD:1")
	if !ok {
		t.Fatal("ChannelMeta reported the known channel as unknown")
	}
	if devAddr != "0001ABCD" || model != "HmIP-STH" ||
		chType != "CLIMATE_TRANSCEIVER" || centralName != "ccu-01" {
		t.Fatalf("meta = %q/%q/%q/%q", devAddr, model, chType, centralName)
	}

	if _, _, _, _, ok := a.ChannelMeta("0001ABCD:9"); ok {
		t.Error("a channel the device does not have must not resolve")
	}
	if _, _, _, _, ok := a.ChannelMeta("MISSING:1"); ok {
		t.Error("an unknown device must not resolve")
	}
}

// TestConfigExportDomainRejectsForeignCentral pins the multi-CCU scope:
// an import payload naming a different central must not write through to
// the channel a same-named device happens to have elsewhere.
func TestConfigExportDomainRejectsForeignCentral(t *testing.T) {
	t.Parallel()
	reg := registryWithChannel(t)
	d := NewConfigExportDomain(reg, NewParamsetsDomain(reg, nil))

	err := d.WriteParamset(context.Background(), "ccu-02", "0001ABCD:1", "MASTER",
		map[string]any{"TEMPERATURE_OFFSET": 1.0})
	if !errors.Is(err, ErrConfigExportCentralMismatch) {
		t.Fatalf("err = %v, want ErrConfigExportCentralMismatch", err)
	}

	if _, err := d.ReadParamset(context.Background(), "ccu-02", "0001ABCD:1", "MASTER"); !errors.Is(err, ErrConfigExportCentralMismatch) {
		t.Fatalf("read err = %v, want ErrConfigExportCentralMismatch", err)
	}
}

// TestConfigExportDomainRejectsUnknownParamset keeps the surface to the
// two keys the export format can describe — a LINK paramset needs a peer
// address the payload has no field for.
func TestConfigExportDomainRejectsUnknownParamset(t *testing.T) {
	t.Parallel()
	reg := registryWithChannel(t)
	d := NewConfigExportDomain(reg, NewParamsetsDomain(reg, nil))

	if _, err := d.ReadParamset(context.Background(), "ccu-01", "0001ABCD:1", "LINK"); !errors.Is(err, ErrConfigExportParamsetKey) {
		t.Fatalf("err = %v, want ErrConfigExportParamsetKey", err)
	}
}

// TestConfigExportDomainReachesTheParamsetPath pins that a correctly
// scoped call reaches the shared paramset domain rather than being
// rejected — the endpoint answered 503 in every production build because
// nothing implemented this service at all.
func TestConfigExportDomainReachesTheParamsetPath(t *testing.T) {
	t.Parallel()
	reg := registryWithChannel(t)
	d := NewConfigExportDomain(reg, NewParamsetsDomain(reg, nil))

	// No ValueWriter is wired, so the call reaches the backend resolve and
	// fails there — which is the proof it got past the scope checks.
	_, err := d.ReadParamset(context.Background(), "ccu-01", "0001ABCD:1", "MASTER")
	if !errors.Is(err, ErrNoParamsetBackend) {
		t.Fatalf("err = %v, want it to reach the paramset backend resolve", err)
	}
	// An empty central name means "whichever central owns it" — what a
	// single-CCU export payload carries.
	_, err = d.ReadParamset(context.Background(), "", "0001ABCD:1", "MASTER")
	if !errors.Is(err, ErrNoParamsetBackend) {
		t.Fatalf("err = %v, want it to reach the paramset backend resolve", err)
	}
}
