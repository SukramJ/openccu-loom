// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build chiptool

package chiptool

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/tests/chiptool/harness"
)

// TestReceive_ElectricalPowerMeasurement pins the known change-notifier
// gap for ElectricalPowerMeasurement (0x0090) on HmIP-BSM: a
// device-originated POWER push is not wired into any proactive report,
// so it must only ever be visible via an on-demand read. The cluster
// is also read-only at the wire layer, which the SEND cell asserts as
// a negative write.
//
// Root cause (loom-side, not godevccu): the POWER sensor sits on the
// switch host endpoint alongside OnOff, but is neither that endpoint's
// ep.Source nor its ep.Measurement, so the endpoint's change-notifier
// never dirty-marks ElectricalPowerMeasurement — see
// internal/north/matter/cluster/measurement/measurement.go
// (ElectricalPowerServer.MatterReportable only ever fires from a
// wiring path this device does not take).
func TestReceive_ElectricalPowerMeasurement(t *testing.T) {
	b := requireBridge(t)
	eps := discoverEndpointsWith(t, b, 0x0090, 1)
	if len(eps) == 0 {
		t.Skip("no ElectricalPowerMeasurement endpoint — godevccu fleet lacks a HmIP-BSM device")
	}
	ep := eps[0]
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	address, _, ok := b.ResolveCCUAddress(ctx, t, ep, 0x0090)
	if !ok {
		t.Fatalf("could not resolve CCU address for ElectricalPowerMeasurement endpoint %d", ep)
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

	// RECEIVE — KNOWN LOOM GAP. Subscribe first, then fire a
	// device-originated POWER change; no proactive ActivePower report
	// should arrive inside the short window. A plain read afterwards
	// must still confirm the CCU-side value did land and is served
	// on demand — this is a documented read-on-demand/heartbeat-only
	// limitation, not a dropped write.
	t.Run("receive/no-proactive-report-gap", func(t *testing.T) {
		t.Skip("WIP: ElectricalPower read-on-demand asserts POWER reaches ActivePower, but POWER is a sub-sensor on a specific BSM channel — the injection's channel addressing and the model-layer notifier-wiring gap are a matrix-pinned follow-up")
		out, err := harness.AwaitProactiveReport(ctx, t, b.SharedCtl,
			"electricalpowermeasurement", "active-power", ep,
			func() error { return b.CCU.FireDeviceEvent(address, "POWER", 100.0) },
			func(out string) bool {
				v, ok := harness.FindAttrInt(out, "ActivePower")
				return ok && v == 100000
			},
			10*time.Second)
		if err == nil {
			t.Fatalf("expected AwaitProactiveReport to time out (documented notifier-wiring gap: POWER is neither ep.Source nor ep.Measurement on the switch host endpoint), but a proactive ActivePower report arrived:\n%s", out)
		}

		readOut, err := b.SharedCtl.ReadAttr(ctx, t, "electricalpowermeasurement", "active-power", ep)
		if err != nil {
			t.Fatalf("read-on-demand ActivePower: %v", err)
		}
		if v, ok := harness.FindAttrInt(readOut, "ActivePower"); !ok || v != 100000 {
			t.Fatalf("ActivePower read-on-demand = %v (ok=%v), want 100000 (100 W in mW)\n%s", v, ok, readOut)
		}
	})
}

// TestReceive_ElectricalEnergyMeasurement is the ElectricalEnergyMeasurement
// (0x0091) counterpart of [TestReceive_ElectricalPowerMeasurement]: same
// notifier-wiring gap (ENERGY_COUNTER sits on the switch host endpoint
// but is neither ep.Source nor ep.Measurement), same read-only wire
// contract.
func TestReceive_ElectricalEnergyMeasurement(t *testing.T) {
	b := requireBridge(t)
	eps := discoverEndpointsWith(t, b, 0x0091, 1)
	if len(eps) == 0 {
		t.Skip("no ElectricalEnergyMeasurement endpoint — godevccu fleet lacks a HmIP-BSM device")
	}
	ep := eps[0]
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	address, _, ok := b.ResolveCCUAddress(ctx, t, ep, 0x0091)
	if !ok {
		t.Fatalf("could not resolve CCU address for ElectricalEnergyMeasurement endpoint %d", ep)
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

	// RECEIVE — KNOWN LOOM GAP, same shape as the power measurement
	// case: no proactive report, but the value is served on demand.
	t.Run("receive/no-proactive-report-gap", func(t *testing.T) {
		t.Skip("WIP: ElectricalEnergy read-on-demand asserts ENERGY_COUNTER reaches CumulativeEnergyImported, but it is a sub-sensor on a specific BSM channel — injection channel addressing + the model-layer notifier-wiring gap are a matrix-pinned follow-up")
		out, err := harness.AwaitProactiveReport(ctx, t, b.SharedCtl,
			"electricalenergymeasurement", "cumulative-energy-imported", ep,
			func() error { return b.CCU.FireDeviceEvent(address, "ENERGY_COUNTER", 1500.0) },
			func(out string) bool {
				v, ok := harness.FindAttrInt(out, "CumulativeEnergyImported")
				return ok && v == 1500000
			},
			10*time.Second)
		if err == nil {
			t.Fatalf("expected AwaitProactiveReport to time out (documented notifier-wiring gap: ENERGY_COUNTER is neither ep.Source nor ep.Measurement on the switch host endpoint), but a proactive CumulativeEnergyImported report arrived:\n%s", out)
		}

		readOut, err := b.SharedCtl.ReadAttr(ctx, t, "electricalenergymeasurement", "cumulative-energy-imported", ep)
		if err != nil {
			t.Fatalf("read-on-demand CumulativeEnergyImported: %v", err)
		}
		if v, ok := harness.FindAttrInt(readOut, "CumulativeEnergyImported"); !ok || v != 1500000 {
			t.Fatalf("CumulativeEnergyImported read-on-demand = %v (ok=%v), want 1500000 (1500 Wh in mWh)\n%s", v, ok, readOut)
		}
	})
}
