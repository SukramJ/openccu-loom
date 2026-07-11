// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build chiptool

package chiptool

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/tests/chiptool/harness"
)

// TestReceive_ElectricalPowerMeasurement exercises ElectricalPowerMeasurement
// (0x0090) on HmIP-BSM in both directions: a device-originated POWER push must
// reach the controller as a proactive ActivePower report, and a controller
// write must be rejected (the cluster is read-only at the wire layer).
//
// The POWER sensor sits on the BSM's sibling meter channel and is attached
// cross-channel onto the switch host endpoint; the endpoint's OnOff notifier
// only marks its own cluster, so the ElectricalPowerServer forwards the POWER
// sensor's own notifier and the bridge wires it to the ActivePower reportable
// path (internal/north/matter/cluster/measurement/measurement.go +
// internal/north/matter/bridge/subscribe.go wireMeasurementListenersLocked).
func TestReceive_ElectricalPowerMeasurement(t *testing.T) {
	b := requireBridge(t)
	eps := discoverEndpointsWith(t, b, 0x0090, 0)
	if len(eps) == 0 {
		t.Skip("no ElectricalPowerMeasurement endpoint — godevccu fleet lacks a HmIP-BSM device")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// Prefer the POWER row: ElectricalPower rides on the switch host endpoint
	// (channel with STATE) AND the meter channel, so the device-scoped resolve
	// must pick the channel POWER actually lives on to inject against.
	address, dpKey, ok := b.ResolveCCUAddress(ctx, t, eps[0], 0x0090, "POWER")
	if !ok {
		t.Fatalf("could not resolve CCU address for ElectricalPowerMeasurement endpoint %d", eps[0])
	}

	// Pick the endpoint whose ActivePower actually tracks the POWER
	// parameter. The cluster materialises on several endpoints of the
	// same device (the switch host plus one generic endpoint per meter
	// quantity — CURRENT / FREQUENCY / POWER / VOLTAGE all share
	// ElectricalPowerMeasurement), and endpoint numbering is
	// store-history-dependent, so "lowest endpoint with the cluster"
	// may be a sibling quantity whose ActivePower is permanently null.
	// Fire the parameter once and use the endpoint that shows it.
	ep, ok := electricalEndpointFor(ctx, t, b, eps,
		"electricalpowermeasurement", "active-power",
		func() error { return b.CCU.FireDeviceEvent(address, dpKey, 100.0) },
		func(out string) bool {
			v, ok := harness.FindAttrInt(out, "ActivePower")
			return ok && v == 100000
		})
	if !ok {
		t.Fatalf("no ElectricalPowerMeasurement endpoint in %v reflects the injected POWER value", eps)
	}

	// SEND — negative. ElectricalPowerServer.MatterWrite unconditionally
	// answers errReadOnly regardless of attribute/value, which the
	// dispatcher's writeErrorStatus classifier maps to UnsupportedWrite.
	t.Run("send/active-power-write-rejected", func(t *testing.T) {
		out, _ := b.SharedCtl.WriteAttr(ctx, t, "electricalpowermeasurement", "active-power", "100000", ep)
		statusHex, ok := harness.WriteStatus(out)
		if !ok {
			t.Fatalf("no write status in chip-tool output:\n%s", out)
		}
		if statusHex != "0x88" {
			t.Fatalf("write ActivePower status = %s, want 0x88 (UNSUPPORTED_WRITE)\n%s", statusHex, out)
		}
	})

	// RECEIVE — a device-originated POWER push must reach the controller as a
	// proactive ActivePower report (100 W → 100000 mW). Subscribe first, then
	// fire the change so the report can only come from the change-notifier, not
	// the subscribe's own initial read.
	t.Run("receive/active-power", func(t *testing.T) {
		out, err := harness.AwaitProactiveReport(ctx, t, b.SharedCtl,
			"electricalpowermeasurement", "active-power", ep,
			func() error { return b.CCU.FireDeviceEvent(address, dpKey, 100.0) },
			func(out string) bool {
				v, ok := harness.FindAttrInt(out, "ActivePower")
				return ok && v == 100000
			},
			30*time.Second)
		if err != nil {
			t.Fatalf("await proactive ActivePower=100000: %v\n%s", err, out)
		}
	})
}

// TestReceive_ElectricalEnergyMeasurement is the ElectricalEnergyMeasurement
// (0x0091) counterpart of [TestReceive_ElectricalPowerMeasurement]: an
// ENERGY_COUNTER push on the sibling meter channel must reach the controller as
// a proactive CumulativeEnergyImported report, same read-only wire contract.
func TestReceive_ElectricalEnergyMeasurement(t *testing.T) {
	b := requireBridge(t)
	eps := discoverEndpointsWith(t, b, 0x0091, 0)
	if len(eps) == 0 {
		t.Skip("no ElectricalEnergyMeasurement endpoint — godevccu fleet lacks a HmIP-BSM device")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// Prefer the ENERGY_COUNTER row — the cluster rides on both the switch host
	// endpoint and the meter channel; inject against the channel it lives on.
	address, dpKey, ok := b.ResolveCCUAddress(ctx, t, eps[0], 0x0091, "ENERGY_COUNTER")
	if !ok {
		t.Fatalf("could not resolve CCU address for ElectricalEnergyMeasurement endpoint %d", eps[0])
	}

	// Same endpoint disambiguation as the power test: pick the endpoint
	// whose CumulativeEnergyImported actually tracks ENERGY_COUNTER.
	// The probe matcher is value-only: a plain chip-tool READ renders
	// the EnergyMeasurementStruct differently than the report logger
	// (the strict "Data = …" shape below is report-specific), and the
	// injected 1'500'000 mWh is unambiguous within a single-attribute
	// read either way.
	ep, ok := electricalEndpointFor(ctx, t, b, eps,
		"electricalenergymeasurement", "cumulative-energy-imported",
		func() error { return b.CCU.FireDeviceEvent(address, dpKey, 1500.0) },
		func(out string) bool {
			return strings.Contains(out, "1500000")
		})
	if !ok {
		t.Fatalf("no ElectricalEnergyMeasurement endpoint in %v reflects the injected ENERGY_COUNTER value", eps)
	}

	// SEND — negative. ElectricalEnergyServer.MatterWrite unconditionally
	// answers errReadOnly the same way ElectricalPowerServer does.
	// CumulativeEnergyImported's wire schema is EnergyMeasurementStruct,
	// so the write value is chip-tool's JSON struct form for field 0
	// ("Energy" -> "energy") — the server rejects it before ever
	// looking at the payload, so the exact field values are immaterial.
	t.Run("send/cumulative-energy-imported-write-rejected", func(t *testing.T) {
		out, _ := b.SharedCtl.WriteAttr(ctx, t, "electricalenergymeasurement", "cumulative-energy-imported", `{"energy": 1500000}`, ep)
		statusHex, ok := harness.WriteStatus(out)
		if !ok {
			t.Fatalf("no write status in chip-tool output:\n%s", out)
		}
		if statusHex != "0x88" {
			t.Fatalf("write CumulativeEnergyImported status = %s, want 0x88 (UNSUPPORTED_WRITE)\n%s", statusHex, out)
		}
	})

	// RECEIVE — an ENERGY_COUNTER push must reach the controller as a proactive
	// CumulativeEnergyImported report (1500 Wh → 1500000 mWh). Subscribe first,
	// then fire so the report can only come from the change-notifier.
	//
	// chip-tool's DataModelLogger has no friendly name for
	// ElectricalEnergyMeasurement.CumulativeEnergyImported (0x0091:0x0001) in
	// the pinned build — unlike ElectricalPower.ActivePower — so it logs the
	// decoded value as a raw "Data = …" on that cluster/attribute path. Match
	// the cluster + attribute + value rather than an attribute name.
	t.Run("receive/cumulative-energy-imported", func(t *testing.T) {
		out, err := harness.AwaitProactiveReport(ctx, t, b.SharedCtl,
			"electricalenergymeasurement", "cumulative-energy-imported", ep,
			func() error { return b.CCU.FireDeviceEvent(address, dpKey, 1500.0) },
			func(out string) bool {
				return strings.Contains(out, "Cluster: 0x0000_0091 Attribute 0x0000_0001") &&
					strings.Contains(out, "Data = 1500000")
			},
			30*time.Second)
		if err != nil {
			t.Fatalf("await proactive CumulativeEnergyImported=1500000: %v\n%s", err, out)
		}
	})
}

// electricalEndpointFor selects, among the endpoints advertising an
// electrical-measurement cluster, the one whose target attribute
// actually tracks the injected CCU parameter. fire pushes the
// device-originated value once; the candidates are then probed with a
// plain chip-tool read and the first endpoint whose output satisfies
// match wins. Returns false when no candidate reflects the value —
// that means the projection itself is broken, not the numbering, and
// callers should fail loudly.
func electricalEndpointFor(
	ctx context.Context, t *testing.T, b *harness.Bridge,
	eps []uint16, cluster, attr string,
	fire func() error, match func(out string) bool,
) (uint16, bool) {
	t.Helper()
	if err := fire(); err != nil {
		t.Fatalf("inject device-originated change: %v", err)
	}
	// Give the event a moment to propagate godevccu → daemon model.
	select {
	case <-time.After(500 * time.Millisecond):
	case <-ctx.Done():
		t.Fatalf("context done while settling: %v", ctx.Err())
	}
	probed := make(map[uint16]string, len(eps))
	for _, ep := range eps {
		out, err := b.SharedCtl.ReadAttr(ctx, t, cluster, attr, ep)
		if err != nil {
			probed[ep] = "read error: " + err.Error()
			continue
		}
		if match(out) {
			return ep, true
		}
		probed[ep] = out
	}
	// Self-diagnosing failure: dump what each candidate actually
	// answered so a matcher/format drift is attributable from the CI
	// log alone.
	for ep, out := range probed {
		if len(out) > 2000 {
			out = out[len(out)-2000:]
		}
		t.Logf("endpoint %d %s/%s probe output (tail):\n%s", ep, cluster, attr, out)
	}
	return 0, false
}
