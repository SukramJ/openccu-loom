// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"context"
	"math"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/custom/cover"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/parameter"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// levelCombinedWriter captures the LEVEL_COMBINED SetValue a blind emits,
// so the assertion reads the value the daemon would actually put on the
// wire rather than one the test computed for itself.
type levelCombinedWriter struct {
	mu     sync.Mutex
	values []string
}

func (w *levelCombinedWriter) SetValue(_ context.Context, _ string, p hmenum.Parameter, value any, _ hmenum.CommandPriority) error {
	if p != hmenum.ParameterLevelCombined {
		return nil
	}
	s, _ := value.(string)
	w.mu.Lock()
	defer w.mu.Unlock()
	w.values = append(w.values, s)
	return nil
}

func (w *levelCombinedWriter) PutParamset(_ context.Context, _ string, _ hmenum.ParamsetKey, _ map[string]any, _ hmenum.CommandPriority) error {
	return nil
}

func (w *levelCombinedWriter) levelCombinedOnly(t *testing.T) string {
	t.Helper()
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.values) != 1 {
		t.Fatalf("expected exactly one LEVEL_COMBINED write, got %d", len(w.values))
	}
	return w.values[0]
}

// newLevelCombinedBlind builds an HM blind through the production
// constructor, so SetCombined runs the real write path.
func newLevelCombinedBlind(w cover.Writer) *cover.Blind {
	d := device.New(device.Config{InterfaceID: "BidCos-RF", Address: "LEQ0000001"})
	const addr = "LEQ0000001:1"
	ch := d.AddChannel(addr, 1, "BLIND", hmenum.ParamsetKeyValues)
	mk := func(p hmenum.Parameter) *generic.Float {
		return generic.NewFloat(generic.Spec{
			Key: hmtypes.DataPointKey{
				ChannelAddress: addr,
				ParamsetKey:    hmenum.ParamsetKeyValues,
				Parameter:      string(p),
			},
			Descriptor: hmproto.ParameterData{
				Type:       hmenum.ParameterTypeFloat,
				Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
			},
			Writer: w,
		})
	}
	ch.Put(mk(hmenum.ParameterLevel))
	ch.Put(mk(hmenum.ParameterLevel2))
	return cover.NewBlind(cover.BlindConfig{
		Channel:      ch,
		Writer:       w,
		Capabilities: custom.CoverCapabilities{SupportsTilt: true},
		Kind:         cover.BlindKindHM,
	})
}

// levelCombinedRows are commanded (level, tilt) pairs on the 0.5 % wire
// grid. 0.29 / 0.57 / 0.58 are the positions whose ×200 product lands
// just below its integer in binary64, which is where a truncating
// encoder parts company with a rounding one.
var levelCombinedRows = []struct{ level, tilt float64 }{
	{0.0, 0.0},
	{0.29, 0.57},
	{0.58, 0.29},
	{0.5, 0.45},
	{0.005, 0.995},
	{1.0, 1.0},
}

// TestBlindLevelCombinedUsesTheCanonicalEncoder pins the LEVEL_COMBINED
// byte encoding across the two places that hold a definition of it: the
// blind's own write path in internal/model/custom/cover, and the
// canonical encoder parameter.ConvertHMLevelToCPV. Neither side is
// compared against a literal, so the test fails as soon as the two
// definitions of the rule drift apart.
func TestBlindLevelCombinedUsesTheCanonicalEncoder(t *testing.T) {
	for _, row := range levelCombinedRows {
		w := &levelCombinedWriter{}
		b := newLevelCombinedBlind(w)
		if err := b.SetCombined(context.Background(), row.level, row.tilt, hmenum.CommandPriorityHigh); err != nil {
			t.Fatalf("level=%v tilt=%v: SetCombined: %v", row.level, row.tilt, err)
		}
		got := w.levelCombinedOnly(t)
		want := parameter.ConvertHMLevelToCPV(row.level) + "," + parameter.ConvertHMLevelToCPV(row.tilt)
		if got != want {
			t.Errorf("level=%v tilt=%v: LEVEL_COMBINED=%q, canonical encoder says %q", row.level, row.tilt, got, want)
		}
	}
}

// TestBlindLevelCombinedSurvivesTheShippedDecoder feeds the string the
// blind wrote into the decoder that actually runs on the callback path
// (backends.ParseCombinedParameter) and requires the commanded position
// back within half a wire quantum. A truncating encoder fails here even
// when both encoder copies agree with each other: 0.29 encodes to 0x39
// and decodes to 0.285, a full quantum below the command.
func TestBlindLevelCombinedSurvivesTheShippedDecoder(t *testing.T) {
	// The wire grid is 1/200; half a quantum is the largest error a
	// correctly rounded encoder can produce on a grid-exact input.
	const halfQuantum = 0.0025
	for _, row := range levelCombinedRows {
		w := &levelCombinedWriter{}
		b := newLevelCombinedBlind(w)
		if err := b.SetCombined(context.Background(), row.level, row.tilt, hmenum.CommandPriorityHigh); err != nil {
			t.Fatalf("level=%v tilt=%v: SetCombined: %v", row.level, row.tilt, err)
		}
		// Both definitions of the encoding are held to the same
		// decode tolerance: the blind's write path and the canonical
		// encoder each have to survive the round trip on their own.
		sources := map[string]string{
			"blind write path":  w.levelCombinedOnly(t),
			"canonical encoder": parameter.ConvertHMLevelToCPV(row.level) + "," + parameter.ConvertHMLevelToCPV(row.tilt),
		}
		for src, got := range sources {
			decoded, ok := backends.ParseCombinedParameter(string(hmenum.ParameterLevelCombined), got)
			if !ok {
				t.Fatalf("%s: level=%v tilt=%v: decoder rejected %q", src, row.level, row.tilt, got)
			}
			level, okL := decoded[string(hmenum.ParameterLevel)].(float64)
			slats, okS := decoded[string(hmenum.ParameterLevelSlats)].(float64)
			if !okL || !okS {
				t.Fatalf("%s: level=%v tilt=%v: decoded %q to %v, want LEVEL + LEVEL_SLATS floats", src, row.level, row.tilt, got, decoded)
			}
			if math.Abs(level-row.level) > halfQuantum {
				t.Errorf("%s: level=%v: wrote %q, decoder reads %v (off by %v, more than half a quantum)", src, row.level, got, level, math.Abs(level-row.level))
			}
			if math.Abs(slats-row.tilt) > halfQuantum {
				t.Errorf("%s: tilt=%v: wrote %q, decoder reads %v (off by %v, more than half a quantum)", src, row.tilt, got, slats, math.Abs(slats-row.tilt))
			}
		}
	}
}
