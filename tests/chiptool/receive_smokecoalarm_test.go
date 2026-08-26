// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

//go:build chiptool

package chiptool

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/tests/chiptool/harness"
)

// TestReceive_SmokeCOAlarm exercises SmokeCOAlarm (0x005C) on HmIP-SWSD
// (siren.SmokeSiren). smokeCOServer rejects every write/invoke
// (internal/model/custom/siren/matter.go: MatterWrite always returns
// errMatterUnknownAttribute, MatterInvoke always returns
// errMatterUnknownCommand — SmokeCOAlarm has no HM-side command mapped
// onto SelfTestRequest), so SEND is a negative case. RECEIVE drives the
// SmokeState/ExpressedState AlarmStateEnum off SMOKE_DETECTOR_ALARM_STATUS
// (Normal=0 / Warning=1 / Critical=2 — see smokeStatusToAlarmState).
func TestReceive_SmokeCOAlarm(t *testing.T) {
	b := requireBridge(t)
	eps := discoverEndpointsWith(t, b, 0x005C, 1)
	if len(eps) == 0 {
		t.Skip("no SmokeCOAlarm endpoint — godevccu fleet lacks a smoke-detector device")
	}
	ep := eps[0]
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	address, _, ok := b.ResolveCCUAddress(ctx, t, ep, 0x005C)
	if !ok {
		t.Fatalf("could not resolve CCU address for SmokeCOAlarm endpoint %d", ep)
	}

	// SEND (negative) — every SmokeCOAlarm attribute is read-only at the
	// wire layer (schema.AttributeWritable gates the write before it ever
	// reaches smokeCOServer.MatterWrite); the write must be rejected.
	t.Run("negative/write-smoke-state", func(t *testing.T) {
		out, _ := b.SharedCtl.WriteAttr(ctx, t, "smokecoalarm", "smoke-state", "0", ep)
		hex, parsed := harness.WriteStatus(out)
		if !parsed {
			t.Fatalf("no write status parsed from smokecoalarm write:\n%s", out)
		}
		if hex == "0x0" {
			t.Fatalf("smokecoalarm smoke-state write unexpectedly succeeded (status 0x0):\n%s", out)
		}
	})

	// SEND (negative) — SelfTestRequest (the cluster's only client
	// command) is never mapped onto a HM command; MatterInvoke rejects
	// every cmdID with errMatterUnknownCommand.
	t.Run("negative/invoke-self-test-request", func(t *testing.T) {
		out, _ := b.SharedCtl.Invoke(ctx, t, "smokecoalarm", "self-test-request", ep)
		hex, parsed := harness.WriteStatus(out)
		if !parsed {
			t.Fatalf("no invoke status parsed from smokecoalarm self-test-request:\n%s", out)
		}
		if hex == "0x0" {
			t.Fatalf("smokecoalarm self-test-request unexpectedly succeeded (status 0x0):\n%s", out)
		}
	})

	// RECEIVE — a device-originated PRIMARY_ALARM must reach the
	// controller as a proactive SmokeState=Critical(2) report. Subscribe
	// first, then fire, so the notifier (not the subscribe's own initial
	// read) is what's under test.
	t.Run("receive/smoke-state-primary-alarm", func(t *testing.T) {
		out, err := harness.AwaitProactiveReport(ctx, t, b.SharedCtl,
			"smokecoalarm", "smoke-state", ep,
			func() error { return b.FireDeviceEventEnum(t, address, "SMOKE_DETECTOR_ALARM_STATUS", "PRIMARY_ALARM") },
			smokecoalarmWantSmokeState(2),
			30*time.Second)
		if err != nil {
			t.Fatalf("await proactive SmokeState=Critical(2): %v\n%s", err, out)
		}
	})

	// RECEIVE — a peer-triggered SECONDARY_ALARM maps to Warning(1),
	// distinct from the Critical(2) pre-state the previous cell left
	// behind.
	t.Run("receive/smoke-state-secondary-alarm", func(t *testing.T) {
		out, err := harness.AwaitProactiveReport(ctx, t, b.SharedCtl,
			"smokecoalarm", "smoke-state", ep,
			func() error {
				return b.FireDeviceEventEnum(t, address, "SMOKE_DETECTOR_ALARM_STATUS", "SECONDARY_ALARM")
			},
			smokecoalarmWantSmokeState(1),
			30*time.Second)
		if err != nil {
			t.Fatalf("await proactive SmokeState=Warning(1): %v\n%s", err, out)
		}
	})

	// RECEIVE — the alarm clearing (IDLE_OFF) maps back to Normal(0),
	// distinct from the Warning(1) pre-state above.
	t.Run("receive/smoke-state-idle-off", func(t *testing.T) {
		out, err := harness.AwaitProactiveReport(ctx, t, b.SharedCtl,
			"smokecoalarm", "smoke-state", ep,
			func() error { return b.FireDeviceEventEnum(t, address, "SMOKE_DETECTOR_ALARM_STATUS", "IDLE_OFF") },
			smokecoalarmWantSmokeState(0),
			30*time.Second)
		if err != nil {
			t.Fatalf("await proactive SmokeState=Normal(0): %v\n%s", err, out)
		}
	})

	// RECEIVE — ExpressedState carries ExpressedStateEnum, not the
	// AlarmStateEnum that SmokeState uses: 0=Normal, 1=SmokeAlarm,
	// 2=CoAlarm (matter.js smoke-co-alarm-cluster.element.ts). So a smoke
	// alarm on this CO-less device expresses SmokeAlarm(1); pushing
	// Critical(2) — as mirroring SmokeState did — reports a
	// carbon-monoxide alarm on a device with no CO sensor, and CoAlarm's
	// conformance is "CO", which this FeatureMap does not carry.
	// Re-firing PRIMARY_ALARM must proactively push SmokeAlarm(1),
	// distinct from the Normal(0) pre-state left by the previous cell.
	t.Run("receive/expressed-state-primary-alarm", func(t *testing.T) {
		out, err := harness.AwaitProactiveReport(ctx, t, b.SharedCtl,
			"smokecoalarm", "expressed-state", ep,
			func() error { return b.FireDeviceEventEnum(t, address, "SMOKE_DETECTOR_ALARM_STATUS", "PRIMARY_ALARM") },
			func(out string) bool {
				v, ok := harness.FindAttrUint(out, "ExpressedState")
				return ok && v == 1
			},
			30*time.Second)
		if err != nil {
			t.Fatalf("await proactive ExpressedState=SmokeAlarm(1): %v\n%s", err, out)
		}
	})
}

// smokecoalarmWantSmokeState returns an [harness.AwaitProactiveReport]
// want-predicate matching a SmokeState AlarmStateEnum readout of want
// (0=Normal, 1=Warning, 2=Critical).
func smokecoalarmWantSmokeState(want int64) func(out string) bool {
	return func(out string) bool {
		v, ok := harness.FindAttrUint(out, "SmokeState")
		return ok && v == want
	}
}
