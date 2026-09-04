// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package light

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// hmLgtCountingWriter counts SetValue calls so a suppressed command is
// distinguishable from a command that reached the CCU.
type hmLgtCountingWriter struct {
	stubWriter
	writes int
}

func (w *hmLgtCountingWriter) SetValue(ctx context.Context, addr string, p hmenum.Parameter, value any, prio hmenum.CommandPriority) error {
	w.writes++
	return w.stubWriter.SetValue(ctx, addr, p, value, prio)
}

// The MQTT state payload must report the level through the shared value
// object [custom.Brightness.Byte], not through a second rounding rule of
// its own. Two forms of one rule disagree on half the CCU's LEVEL grid,
// and the disagreement is not cosmetic: the byte Home Assistant echoes
// back is compared against the commanded byte, so a published byte that
// no level maps to re-arms a redundant CCU write.
//
// The test asserts the observable end of that: over the whole grid, the
// published byte fed back through SetLevel must not produce a write.
func TestHmLgtStateBrightnessRoundTripsWithoutRearmingAWrite(t *testing.T) {
	t.Parallel()

	rearmed := make([]float64, 0, 8)
	for i := range 1001 {
		level := float64(i) / 1000.0
		w := &hmLgtCountingWriter{}
		l, dp := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})
		dp.OnEvent(level)

		state := l.State()
		ls, isLight := state.(*payload.LightState)
		if !isLight {
			t.Fatalf("level %.3f: State() has type %T, want the light state payload", level, state)
		}
		if ls.Brightness == nil {
			t.Fatalf("level %.3f: State() reported no brightness", level)
		}
		published := *ls.Brightness
		if published == 0 {
			// A level below 1/255 quantises to byte 0 on every rule, and
			// echoing 0 is a genuine off command — not a rounding re-arm.
			continue
		}

		// The published byte is what Home Assistant echoes back on the
		// command topic; payload.go divides it by 255 again.
		if err := l.SetLevel(context.Background(), float64(published)/255.0, hmenum.CommandPriorityHigh); err != nil {
			t.Fatalf("level %.3f: echo SetLevel: %v", level, err)
		}
		if w.writes != 0 {
			rearmed = append(rearmed, level)
		}
	}
	if len(rearmed) != 0 {
		head := rearmed
		if len(head) > 5 {
			head = head[:5]
		}
		t.Fatalf("the published brightness re-armed a CCU write on %d of 1001 levels (first: %v) — State() and custom.Brightness.Byte() are two different rules", len(rearmed), head)
	}
}

// The single-source property itself, stated directly: the published byte
// is the value object's byte.
func TestHmLgtStateBrightnessIsTheSharedValueObjectByte(t *testing.T) {
	t.Parallel()

	for _, level := range []float64{0.002, 0.003, 0.006, 0.5, 0.999, 1.0} {
		w := &stubWriter{}
		l, dp := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})
		dp.OnEvent(level)

		ls, ok := l.State().(*payload.LightState)
		if !ok || ls.Brightness == nil {
			t.Fatalf("level %v: no brightness in state payload", level)
		}
		want := int(custom.NewBrightness(level).Byte())
		if *ls.Brightness != want {
			t.Fatalf("level %v: State() published brightness %d, custom.Brightness.Byte() is %d", level, *ls.Brightness, want)
		}
	}
}
