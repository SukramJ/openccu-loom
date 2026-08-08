// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package engine_test

import (
	"errors"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// This file covers the PanicTrigger verb (notes/concepts/alarm-concept.md §7): a
// panic input chooses loud or silent independent of arm state, and a
// configured silent policy can never be forced loud by the caller.

func TestPanicTrigger_LoudByDefaultUsesTheAcousticPolicy(t *testing.T) {
	h := newHarness(t)
	h.seedStandardZone()
	h.start()

	if err := h.eng.PanicTrigger(h.ctx, "eg", false, "tester", "wall-button"); err != nil {
		t.Fatalf("panic trigger: %v", err)
	}
	h.wantState("eg", hmenum.AlarmZoneStateTriggered)
	if fire := h.outputs.lastFire(t); fire.Opts.Policy.Silent {
		t.Fatalf("policy = %+v, want loud", fire.Opts.Policy)
	}
	triggered := h.sink.triggered()
	if len(triggered) != 1 || triggered[0].Cause != "panic" {
		t.Fatalf("triggered events = %+v, want one panic-cause entry", triggered)
	}
}

func TestPanicTrigger_SilentSuppressesAcousticOutputs(t *testing.T) {
	h := newHarness(t)
	h.seedStandardZone()
	h.start()

	if err := h.eng.PanicTrigger(h.ctx, "eg", true, "tester", "duress"); err != nil {
		t.Fatalf("panic trigger: %v", err)
	}
	if fire := h.outputs.lastFire(t); !fire.Opts.Policy.Silent {
		t.Fatalf("policy = %+v, want silent", fire.Opts.Policy)
	}
}

func TestPanicTrigger_ConfiguredSilentPolicyCannotBeForcedLoud(t *testing.T) {
	h := newHarness(t)
	cfg := defaultZoneConfig()
	cfg.PanicOutputs = engine.OutputPolicy{Silent: true}
	h.seedZone("eg", "Erdgeschoss", cfg)
	h.start()

	if err := h.eng.PanicTrigger(h.ctx, "eg", false, "tester", "wall-button"); err != nil {
		t.Fatalf("panic trigger: %v", err)
	}
	if fire := h.outputs.lastFire(t); !fire.Opts.Policy.Silent {
		t.Fatalf("policy = %+v, want silent — a configured silent panic policy is never overridden loud", fire.Opts.Policy)
	}
}

func TestPanicTrigger_UnknownZoneReturnsError(t *testing.T) {
	h := newHarness(t)
	h.seedStandardZone()
	h.start()

	if err := h.eng.PanicTrigger(h.ctx, "nope", false, "tester", "test"); !errors.Is(err, engine.ErrUnknownZone) {
		t.Fatalf("err = %v, want ErrUnknownZone", err)
	}
}

func TestPanicTrigger_BeforeStartReturnsInvalidState(t *testing.T) {
	h := newHarness(t)
	h.seedStandardZone()
	h.build() // constructed, not started

	if err := h.eng.PanicTrigger(h.ctx, "eg", false, "tester", "test"); !errors.Is(err, engine.ErrInvalidState) {
		t.Fatalf("err = %v, want ErrInvalidState", err)
	}
}

func TestPanicTrigger_FromArmedResumesArmedAfterTheEpisode(t *testing.T) {
	h := newHarness(t)
	h.seedStandardZone()
	h.start()
	h.armFull()

	if err := h.eng.PanicTrigger(h.ctx, "eg", false, "tester", "wall-button"); err != nil {
		t.Fatalf("panic trigger: %v", err)
	}
	h.wantState("eg", hmenum.AlarmZoneStateTriggered)

	h.advance(60 * time.Second) // full mode's configured trigger time
	h.wantState("eg", hmenum.AlarmZoneStateArmed)
	if got := h.mustSnapshot("eg").Mode; got != hmenum.AlarmModeFull {
		t.Fatalf("mode after the panic episode = %s, want full (resumed)", got)
	}
}
