// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package light

import (
	"context"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/wire"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// noWriteSentinel is a value stubWriter.last never naturally takes (every
// SetLevel target lands in [0,1]) — used to detect that MatterInvoke did
// NOT reach Writer.SetValue for the gated no-op branches below.
const noWriteSentinel = -1.0

// ─── MoveToLevel (0x00) ExecuteIfOff gate ──────────────────────────────────

// TestLevelInvokeMoveToLevelWhileOffIsNoOp verifies that plain MoveToLevel
// on an off (here: unobserved) light without the ExecuteIfOff option set is
// a silent Success no-op — no SetLevel call reaches the writer. Mirrors
// matter.js LevelControlServer.ts:596 (#optionsAllowExecution) /
// LevelControlServer.ts:245 (returns without acting).
func TestLevelInvokeMoveToLevelWhileOffIsNoOp(t *testing.T) {
	w := &stubWriter{last: noWriteSentinel}
	l, _ := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})
	srv := levelServer(t, l)

	req := wire.MoveToLevelRequest{Level: 100}
	if _, err := srv.MatterInvoke(context.Background(), matterCmdMoveToLevel, req, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MoveToLevel while off: unexpected error %v", err)
	}
	if w.last != noWriteSentinel {
		t.Fatalf("MoveToLevel while off wrote %v, want no write (sentinel %v)", w.last, noWriteSentinel)
	}
}

// TestLevelInvokeMoveToLevelWhileOffExecuteIfOffExecutes verifies that
// setting both the OptionsMask and OptionsOverride ExecuteIfOff bit (bit 0)
// makes the gate allow execution even while off, per matter.js
// LevelControlServer.ts:581 (#calculateEffectiveOptions).
func TestLevelInvokeMoveToLevelWhileOffExecuteIfOffExecutes(t *testing.T) {
	w := &stubWriter{}
	l, _ := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})
	srv := levelServer(t, l)

	req := wire.MoveToLevelRequest{Level: 100, OptionsMask: 1, OptionsOverride: 1}
	if _, err := srv.MatterInvoke(context.Background(), matterCmdMoveToLevel, req, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MoveToLevel with ExecuteIfOff: unexpected error %v", err)
	}
	// 100/254 ≈ 0.3937
	if w.last < 0.38 || w.last > 0.40 {
		t.Fatalf("MoveToLevel with ExecuteIfOff wrote %v, want ~0.3937", w.last)
	}
}

// TestLevelInvokeMoveToLevelWhileOffPartialOptionsIsNoOp verifies that the
// ExecuteIfOff gate requires BOTH OptionsMask bit 0 AND OptionsOverride bit
// 0 — either bit alone leaves the effective option unset (Options attribute
// is a constant 0 on this projection), so the command stays a no-op.
func TestLevelInvokeMoveToLevelWhileOffPartialOptionsIsNoOp(t *testing.T) {
	cases := []struct {
		name            string
		optionsMask     uint8
		optionsOverride uint8
	}{
		{"mask-only", 1, 0},
		{"override-only", 0, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := &stubWriter{last: noWriteSentinel}
			l, _ := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})
			srv := levelServer(t, l)

			req := wire.MoveToLevelRequest{Level: 100, OptionsMask: tc.optionsMask, OptionsOverride: tc.optionsOverride}
			if _, err := srv.MatterInvoke(context.Background(), matterCmdMoveToLevel, req, hmenum.CommandPriorityHigh); err != nil {
				t.Fatalf("MoveToLevel partial options: unexpected error %v", err)
			}
			if w.last != noWriteSentinel {
				t.Fatalf("MoveToLevel partial options (%s) wrote %v, want no write", tc.name, w.last)
			}
		})
	}
}

// ─── MoveToLevel / MoveToLevelWithOnOff level cropping ─────────────────────

// TestLevelInvokeMoveToLevelCropsBelowMin verifies that Level=0 crops to
// MinLevel (1) while the light stays on — matter.js
// LevelControlServer.ts:249 cropValueRange(level, minLevel, maxLevel).
func TestLevelInvokeMoveToLevelCropsBelowMin(t *testing.T) {
	w := &stubWriter{}
	l, _ := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})
	l.OnLevel(0.5) // light is on
	srv := levelServer(t, l)

	req := wire.MoveToLevelRequest{Level: 0}
	if _, err := srv.MatterInvoke(context.Background(), matterCmdMoveToLevel, req, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MoveToLevel(0): unexpected error %v", err)
	}
	// Cropped to MinLevel=1 → 1/254 ≈ 0.0039; the light stays on (not 0).
	if w.last < 0.003 || w.last > 0.005 {
		t.Fatalf("MoveToLevel(0) wrote %v, want ~0.0039 (cropped to MinLevel, still on)", w.last)
	}
}

// TestLevelInvokeMoveToLevelCropsAboveMax verifies that Level=255 crops to
// MaxLevel (254), saturating CurrentLevel to 1.0.
func TestLevelInvokeMoveToLevelCropsAboveMax(t *testing.T) {
	w := &stubWriter{}
	l, _ := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})
	l.OnLevel(0.5) // light is on
	srv := levelServer(t, l)

	req := wire.MoveToLevelRequest{Level: 255}
	if _, err := srv.MatterInvoke(context.Background(), matterCmdMoveToLevel, req, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MoveToLevel(255): unexpected error %v", err)
	}
	if w.last != 1.0 {
		t.Fatalf("MoveToLevel(255) wrote %v, want 1.0 (cropped to MaxLevel)", w.last)
	}
}

// ─── MoveToLevelWithOnOff (0x04) bypasses the ExecuteIfOff gate ────────────

// TestLevelInvokeMoveToLevelWithOnOffWhileOffExecutes verifies that the
// WithOnOff variant is never gated on ExecuteIfOff — it always executes,
// coupling the OnOff state to the target level instead (matter.js
// LevelControlServer.ts:500 couple()).
func TestLevelInvokeMoveToLevelWithOnOffWhileOffExecutes(t *testing.T) {
	w := &stubWriter{}
	l, _ := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})
	srv := levelServer(t, l)

	req := wire.MoveToLevelRequest{Level: 100}
	if _, err := srv.MatterInvoke(context.Background(), matterCmdMoveToLevelWithOnOff, req, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MoveToLevelWithOnOff while off: unexpected error %v", err)
	}
	// 100/254 ≈ 0.3937 — not gated, so it executes despite the light being off.
	if w.last < 0.38 || w.last > 0.40 {
		t.Fatalf("MoveToLevelWithOnOff while off wrote %v, want ~0.3937", w.last)
	}
}

// TestLevelInvokeMoveToLevelWithOnOffMinLevelTurnsOff verifies the
// MinLevel↔Off coupling (Matter §1.6.4.1.2): a WithOnOff target that lands
// on MinLevel (1) after cropping maps to SetLevel(0) — HM off. Both the
// exact MinLevel request and a below-MinLevel request that crops to it
// take this path.
func TestLevelInvokeMoveToLevelWithOnOffMinLevelTurnsOff(t *testing.T) {
	for _, level := range []uint8{1, 0} {
		t.Run("", func(t *testing.T) {
			w := &stubWriter{}
			l, _ := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})
			l.OnLevel(0.5) // light is on; the command must still turn it off
			srv := levelServer(t, l)

			req := wire.MoveToLevelRequest{Level: level}
			if _, err := srv.MatterInvoke(context.Background(), matterCmdMoveToLevelWithOnOff, req, hmenum.CommandPriorityHigh); err != nil {
				t.Fatalf("MoveToLevelWithOnOff(%d): unexpected error %v", level, err)
			}
			if w.last != 0.0 {
				t.Fatalf("MoveToLevelWithOnOff(%d) wrote %v, want 0.0 (MinLevel→Off coupling)", level, w.last)
			}
		})
	}
}

// ─── Step (0x02) / StepWithOnOff (0x06) MinLevel floor ─────────────────────

// TestLevelInvokeStepDownFloorsAtMinLevelNotOff drives Step (0x02) with the
// tag-keyed map[uint8]any shape decodeGenericTagMap produces on the real
// wire path (see internal/north/matter/bridge/fields_reader.go), rather
// than the string-keyed in-package shape. A plain Step can never turn the
// device off — the target clamps to MinLevel (1), mirroring matter.js
// Transitions.ts:139's min/max property clamp.
func TestLevelInvokeStepDownFloorsAtMinLevelNotOff(t *testing.T) {
	w := &stubWriter{}
	l, _ := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})
	l.OnLevel(0.1) // current ≈ 25/254; on
	srv := levelServer(t, l)

	fields := map[uint8]any{0: uint64(wire.LevelStepModeDown), 1: uint64(200)}
	if _, err := srv.MatterInvoke(context.Background(), matterCmdStep, fields, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("Step down: unexpected error %v", err)
	}
	// 1/254 ≈ 0.0039 — floors at MinLevel, light stays on.
	if w.last < 0.003 || w.last > 0.005 {
		t.Fatalf("Step down wrote %v, want ~0.0039 (MinLevel, not 0)", w.last)
	}
}

// TestLevelInvokeStepWithOnOffDownAtMinLevelTurnsOff verifies that
// StepWithOnOff (0x06) clamped to MinLevel maps to SetLevel(0) — Matter
// §1.6.7.6: new CurrentLevel == minimum → OnOff FALSE.
func TestLevelInvokeStepWithOnOffDownAtMinLevelTurnsOff(t *testing.T) {
	w := &stubWriter{}
	l, _ := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})
	l.OnLevel(0.1) // current ≈ 25/254; on
	srv := levelServer(t, l)

	fields := map[uint8]any{0: uint64(wire.LevelStepModeDown), 1: uint64(200)}
	if _, err := srv.MatterInvoke(context.Background(), matterCmdStepWithOnOff, fields, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("StepWithOnOff down: unexpected error %v", err)
	}
	if w.last != 0.0 {
		t.Fatalf("StepWithOnOff down wrote %v, want 0.0 (MinLevel→Off coupling)", w.last)
	}
}

// ─── Step (0x02) ExecuteIfOff gate ──────────────────────────────────────────

// TestLevelInvokeStepWhileOffGate verifies that plain Step (0x02) is gated
// like plain MoveToLevel: a no-op while off without options, executing once
// the tag-keyed OptionsMask (tag 3) / OptionsOverride (tag 4) bits are both
// set. Mirrors matter.js LevelControlServer.ts:387.
func TestLevelInvokeStepWhileOffGate(t *testing.T) {
	t.Run("no options is a no-op", func(t *testing.T) {
		w := &stubWriter{last: noWriteSentinel}
		l, _ := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})
		srv := levelServer(t, l)

		fields := map[uint8]any{0: uint64(wire.LevelStepModeUp), 1: uint64(20)}
		if _, err := srv.MatterInvoke(context.Background(), matterCmdStep, fields, hmenum.CommandPriorityHigh); err != nil {
			t.Fatalf("Step while off: unexpected error %v", err)
		}
		if w.last != noWriteSentinel {
			t.Fatalf("Step while off wrote %v, want no write", w.last)
		}
	})

	t.Run("ExecuteIfOff executes", func(t *testing.T) {
		w := &stubWriter{last: noWriteSentinel}
		l, _ := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})
		srv := levelServer(t, l)

		fields := map[uint8]any{0: uint64(1), 1: uint64(20), 3: uint64(1), 4: uint64(1)}
		if _, err := srv.MatterInvoke(context.Background(), matterCmdStep, fields, hmenum.CommandPriorityHigh); err != nil {
			t.Fatalf("Step while off with ExecuteIfOff: unexpected error %v", err)
		}
		if w.last == noWriteSentinel {
			t.Fatalf("Step while off with ExecuteIfOff did not write")
		}
	})
}

// ─── Move (0x01) / MoveWithOnOff (0x05) zero-rate rejection ────────────────

// TestLevelInvokeMoveZeroRateRejected verifies that Move / MoveWithOnOff
// reject an explicit Rate of 0 with an InvalidCommand-flavoured error,
// mirroring matter.js LevelControlServer.ts:271 (#assertRateValue). A null
// or absent rate falls back to DefaultMoveRate there and stays a no-op
// Success here.
func TestLevelInvokeMoveZeroRateRejected(t *testing.T) {
	for _, cmdID := range []uint32{matterCmdMove, matterCmdMoveWithOnOff} {
		t.Run("", func(t *testing.T) {
			l, _ := newLightRig(t, "HmIP-BDT:4", &stubWriter{}, custom.LightCapabilities{Dimmable: true})
			srv := levelServer(t, l)

			fields := map[uint8]any{1: uint64(0)}
			_, err := srv.MatterInvoke(context.Background(), cmdID, fields, hmenum.CommandPriorityHigh)
			if err == nil || !strings.Contains(err.Error(), "invalid command argument") {
				t.Fatalf("Move cmd 0x%02X with rate=0: err = %v, want an \"invalid command argument\" error", cmdID, err)
			}
		})
	}
}

// TestLevelInvokeMoveAbsentOrNullRateIsNoOp verifies that Move without a
// rate field, and Move with an explicit TLV-null rate (tag present, value
// nil), both stay a Success no-op.
func TestLevelInvokeMoveAbsentOrNullRateIsNoOp(t *testing.T) {
	t.Run("absent fields", func(t *testing.T) {
		l, _ := newLightRig(t, "HmIP-BDT:4", &stubWriter{}, custom.LightCapabilities{Dimmable: true})
		srv := levelServer(t, l)
		if _, err := srv.MatterInvoke(context.Background(), matterCmdMove, nil, hmenum.CommandPriorityHigh); err != nil {
			t.Fatalf("Move(nil fields): unexpected error %v", err)
		}
	})

	t.Run("null rate", func(t *testing.T) {
		l, _ := newLightRig(t, "HmIP-BDT:4", &stubWriter{}, custom.LightCapabilities{Dimmable: true})
		srv := levelServer(t, l)
		fields := map[uint8]any{1: nil}
		if _, err := srv.MatterInvoke(context.Background(), matterCmdMove, fields, hmenum.CommandPriorityHigh); err != nil {
			t.Fatalf("Move(rate=null): unexpected error %v", err)
		}
	})
}

// ─── DataVersion ─────────────────────────────────────────────────────────

// TestGatedMoveToLevelDoesNotBumpDataVersion verifies that a gated
// MoveToLevel no-op (light off, no ExecuteIfOff option) does not increment
// MatterDataVersion — only an actually-applied SetLevel bumps the counter.
func TestGatedMoveToLevelDoesNotBumpDataVersion(t *testing.T) {
	l, _ := newLightRig(t, "HmIP-BDT:4", &stubWriter{}, custom.LightCapabilities{Dimmable: true})
	before := l.MatterDataVersion()

	srv := levelServer(t, l)
	req := wire.MoveToLevelRequest{Level: 100}
	if _, err := srv.MatterInvoke(context.Background(), matterCmdMoveToLevel, req, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MoveToLevel while off: unexpected error %v", err)
	}
	if after := l.MatterDataVersion(); after != before {
		t.Fatalf("gated MoveToLevel bumped MatterDataVersion: before=%d after=%d", before, after)
	}
}
