// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package alarm

import (
	"encoding/json"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/alarm/codes"
	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// w2AlmSeedCodeGatedZone persists an armable zone whose code policy
// requires a code for arm and for disarm.
//
// The policy is only effective while an applicable enabled PIN exists
// for the zone — the facade resolves the "codes exist" half and passes
// an empty code through when none do (codes.Facade.Validate) — so the
// caller must seed one as well; see [w2AlmSeedPIN].
func w2AlmSeedCodeGatedZone(h *intentsHarness, id, name string) {
	h.t.Helper()
	requireDisarm := true
	cfg := engine.ZoneConfig{
		Modes: map[hmenum.AlarmMode]engine.ModeConfig{hmenum.AlarmModeFull: {}},
		CodePolicy: engine.CodePolicy{
			RequireArm:    true,
			RequireDisarm: &requireDisarm,
		},
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		h.t.Fatalf("marshal zone config: %v", err)
	}
	now := intentsTestStart.UnixMilli()
	if err := h.svc.Stores().Zones.Upsert(h.ctx, sqlitestore.AlarmZoneRow{
		ID: id, Name: name, ConfigJSON: string(b), CreatedAtMS: now, UpdatedAtMS: now,
	}); err != nil {
		h.t.Fatalf("seed zone: %v", err)
	}
}

// w2AlmSeedPIN persists one enabled PIN code for zoneID granting arm
// and disarm, so the zone's code policy resolves to "a code exists and
// is required" rather than to inert.
func w2AlmSeedPIN(h *intentsHarness, zoneID string) {
	h.t.Helper()
	hash, err := codes.HashPIN("4711")
	if err != nil {
		h.t.Fatalf("HashPIN: %v", err)
	}
	perms, err := json.Marshal(codes.Perms{Arm: true, Disarm: true})
	if err != nil {
		h.t.Fatalf("marshal perms: %v", err)
	}
	zones, err := json.Marshal([]string{zoneID})
	if err != nil {
		h.t.Fatalf("marshal zones: %v", err)
	}
	now := intentsTestStart.UnixMilli()
	if err := h.svc.Stores().Codes.Upsert(h.ctx, sqlitestore.AlarmCodeRow{
		ID: "w2alm-pin", Name: "Operator", Kind: string(codes.KindPIN), Hash: hash,
		PermsJSON: string(perms), ZonesJSON: string(zones), Enabled: true,
		CreatedAtMS: now, UpdatedAtMS: now,
	}); err != nil {
		h.t.Fatalf("seed pin code: %v", err)
	}
}

// TestW2AlmHardwareIntentSourcesKeepTheEngineCodeBypass pins the
// hardware-source vocabulary across the intent-router / engine seam.
//
// The engine closes an authorization decision on string equality: a
// press attributed to [engine.CodeSourceKeypad] or
// [engine.CodeSourceRemote] arrives pre-authenticated by its slot or
// binding match, so resolveCode drops the zone's code requirement. The
// producer of that token is the intent router in this package, which
// carries no PIN it could supply instead. A token spelled differently
// on the producing side therefore loses the bypass silently: the
// engine demands a code the hardware cannot give, refuses with
// ErrInvalidCode, and every bound keypad and remote key stops acting
// on a code-gated zone with nothing failing at compile time.
//
// The test drives the real intent router over a real engine and the
// real codes facade, on a zone that requires a code for arm and for
// disarm and has an enabled PIN, so the requirement is live. It fails
// both ways: the presses must act despite the requirement, and the
// counter-case below shows the same zone does refuse an anonymous
// code-free verb, so a pass cannot come from an inert policy.
func TestW2AlmHardwareIntentSourcesKeepTheEngineCodeBypass(t *testing.T) {
	t.Parallel()

	t.Run("keypad", func(t *testing.T) {
		t.Parallel()
		src := &fakeCodeSource{rows: []CodeRow{{
			ID: "c1", Name: "Alice", Kind: CodeKindKeypadSlot, Enabled: true,
			Perms:   CodePerms{Arm: true, Disarm: true},
			Binding: CodeBinding{Central: intentsTestCentral, DeviceAddress: "WKP0001", Slot: 1, ArmMode: "full", ZoneID: "eg"},
		}}}
		h := newIntentsHarness(t, src)
		w2AlmSeedCodeGatedZone(h, "eg", "Erdgeschoss")
		w2AlmSeedPIN(h, "eg")
		h.start()

		h.svc.intents.onEvent(h.ctx, intentsTestCentral, wkpEvent("WKP0001:0", hmenum.ParameterCodeID, hmtypes.IntValue(1)))
		h.svc.intents.onEvent(h.ctx, intentsTestCentral, wkpEvent("WKP0001:0", hmenum.ParameterCodeState, hmtypes.IntValue(1)))
		h.svc.intents.onEvent(h.ctx, intentsTestCentral, wkpEvent("WKP0001:1", hmenum.ParameterPressLock, hmtypes.BoolValue(true)))

		if got := h.zoneState("eg"); got != hmenum.AlarmZoneStateArmed {
			t.Fatalf("zone state after a bound keypad press = %s, want armed: the router's "+
				"source token no longer matches engine.CodeSourceKeypad, so the engine "+
				"code-gated a source that carries no code", got)
		}
		h.wantNoJournalEvent("code_action_failed")
	})

	t.Run("remote", func(t *testing.T) {
		t.Parallel()
		src := &fakeCodeSource{rows: []CodeRow{{
			ID: "r1", Name: "Remote", Kind: CodeKindRemoteKey, Enabled: true,
			Perms:   CodePerms{Disarm: true},
			Binding: CodeBinding{Central: intentsTestCentral, ChannelAddress: "REMOTE01:1", Parameter: "PRESS_LONG", Action: "disarm", ZoneID: "eg"},
		}}}
		h := newIntentsHarness(t, src)
		w2AlmSeedCodeGatedZone(h, "eg", "Erdgeschoss")
		w2AlmSeedPIN(h, "eg")
		h.start()
		if _, err := h.svc.Engine().Arm(h.ctx, "eg", engine.ArmRequest{
			Mode: hmenum.AlarmModeFull, By: "tester", Source: engine.CodeSourceRESTOperator,
		}); err != nil {
			t.Fatalf("arm: %v", err)
		}

		h.svc.intents.onEvent(h.ctx, intentsTestCentral, wkpEvent("REMOTE01:1", hmenum.ParameterPressLong, hmtypes.BoolValue(true)))

		if got := h.zoneState("eg"); got != hmenum.AlarmZoneStateDisarmed {
			t.Fatalf("zone state after a bound remote press = %s, want disarmed: the router's "+
				"source token no longer matches engine.CodeSourceRemote, so the engine "+
				"code-gated a source that carries no code", got)
		}
		h.wantNoJournalEvent("code_action_failed")
	})

	// The counter-case: the very same zone refuses a code-free verb
	// from an anonymous source. Without it, a policy that had silently
	// become inert would let both cases above pass.
	t.Run("anonymous source stays gated", func(t *testing.T) {
		t.Parallel()
		h := newIntentsHarness(t, &fakeCodeSource{})
		w2AlmSeedCodeGatedZone(h, "eg", "Erdgeschoss")
		w2AlmSeedPIN(h, "eg")
		h.start()

		_, err := h.svc.Engine().Arm(h.ctx, "eg", engine.ArmRequest{Mode: hmenum.AlarmModeFull, By: "mqtt", Source: "mqtt"})
		if err == nil {
			t.Fatal("anonymous code-free arm succeeded: the zone's code policy is inert, " +
				"so the bypass assertions above prove nothing")
		}
		if got := h.zoneState("eg"); got != hmenum.AlarmZoneStateDisarmed {
			t.Fatalf("zone state after a refused anonymous arm = %s, want disarmed", got)
		}
	})
}
