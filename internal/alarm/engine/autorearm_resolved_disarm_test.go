// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package engine_test

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// disarmingValidator resolves any code, and while it resolves it drives
// the zone into its post-trigger disarmed state. Code resolution runs
// without the engine lock precisely so this can happen in production:
// the zone the verb read as triggered is disarmed by the time the
// resolved disarm applies.
type disarmingValidator struct {
	during func()
	once   bool
}

func (v *disarmingValidator) Validate(context.Context, string, string, string, string) (identity string, duress bool, err error) {
	if !v.once {
		v.once = true
		v.during()
	}
	return "Alice", false, nil
}

// A disarm whose code resolved while the zone returned to disarmed must
// cancel the auto-rearm the post-trigger disarm just scheduled, and
// journal that cancellation — the same duty the already-disarmed branch
// carries. Without it the zone re-arms behind the operator who just
// disarmed it.
func TestAutoRearm_ResolvedDisarmOnAReturnedZoneCancelsThePendingRearm(t *testing.T) {
	h := newHarness(t)
	seedAutoRearmZone(h)

	v := &disarmingValidator{}
	v.during = func() {
		// The trigger window elapses while the code is being verified:
		// the zone lands in post-trigger disarmed with a fresh
		// auto-rearm timer.
		h.advance(60 * time.Second)
		h.eng.HandleSensorEvent(h.ctx, "window", false)
	}
	h.startWithValidator(v)

	h.armFull()
	h.eng.HandleSensorEvent(h.ctx, "window", true)
	h.wantState("eg", hmenum.AlarmZoneStateTriggered)

	if err := h.eng.DisarmWithCode(h.ctx, "eg", "tester", "test", "1234"); err != nil {
		t.Fatalf("DisarmWithCode: %v", err)
	}
	if !v.once {
		t.Fatal("the validator was never consulted, so the resolved-disarm branch was not reached")
	}
	if !h.journal.has("auto_rearm_scheduled") {
		t.Fatalf("the auto-rearm was never scheduled, so there was nothing to cancel; got %v", h.journal.events())
	}
	if !h.journal.has("auto_rearm_cancelled") {
		t.Fatalf("missing auto_rearm_cancelled journal entry; got %v", h.journal.events())
	}

	// The cancelled timer must not fire.
	h.advance(time.Minute)
	h.wantState("eg", hmenum.AlarmZoneStateDisarmed)
}

var _ engine.CodeValidator = (*disarmingValidator)(nil)
