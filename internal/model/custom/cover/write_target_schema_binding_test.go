// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package cover_test

import (
	"context"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/custom/cover"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// paramRecorder records every parameter a custom DP writes.
type paramRecorder struct {
	mu     sync.Mutex
	params []hmenum.Parameter
	values []any
}

func (r *paramRecorder) SetValue(
	_ context.Context, _ string, parameter hmenum.Parameter, value any, _ hmenum.CommandPriority,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.params = append(r.params, parameter)
	r.values = append(r.values, value)
	return nil
}

func (r *paramRecorder) PutParamset(
	_ context.Context, _ string, _ hmenum.ParamsetKey, _ map[string]any, _ hmenum.CommandPriority,
) error {
	return nil
}

func (r *paramRecorder) wrote(parameter hmenum.Parameter) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range r.params {
		if p == parameter {
			return true
		}
	}
	return false
}

// newSchemaBoundCoverChannel builds a single-channel cover carrying LEVEL,
// LEVEL_2 and the two alternate write parameters the tests bind through the
// schema.
func newSchemaBoundCoverChannel(t *testing.T) *device.Channel {
	t.Helper()
	dev := device.New(device.Config{
		InterfaceID:  "HmIP-RF",
		Interface:    hmenum.InterfaceHmIPRF,
		Address:      "SCHEMA0001",
		ProductGroup: hmenum.ProductGroupHmIP,
	})
	dev.AddChannel("SCHEMA0001:0", 0, "MAINTENANCE", hmenum.ParamsetKeyValues)
	ch := dev.AddChannel("SCHEMA0001:1", 1, "BLIND", hmenum.ParamsetKeyValues)
	putCoverFloatDP(ch, hmenum.ParameterLevel)
	putCoverFloatDP(ch, hmenum.ParameterLevel2)
	return ch
}

// TestCoverStopFollowsTheProfileSchema pins the STOP write to the parameter
// the profile's channel-group schema names, not to a fixed parameter on the
// cover's own channel. Every shipping profile happens to name STOP, so a
// hard-coded write is indistinguishable from a schema-driven one until a
// schema names something else — which is what this test does.
func TestCoverStopFollowsTheProfileSchema(t *testing.T) {
	t.Parallel()
	w := &paramRecorder{}
	ch := newSchemaBoundCoverChannel(t)
	c := cover.New(cover.Config{
		Channel:      ch,
		Writer:       w,
		Capabilities: custom.CoverCapabilities{SupportsStop: true},
		Group: custom.RebasedChannelGroupConfig{
			Fields: map[hmenum.Field]custom.FieldValue{
				hmenum.FieldStop: custom.Bare(hmenum.ParameterOpen),
			},
		},
	})
	if err := c.Stop(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if !w.wrote(hmenum.ParameterOpen) {
		t.Fatalf("Cover.Stop wrote %v; the schema named %s", w.params, hmenum.ParameterOpen)
	}
}

// TestBlindCombinedWriteFollowsTheProfileSchema is the same pin for the
// joint-axis write. The field differs per family — LEVEL_COMBINED on the
// classic RF actuators, COMBINED_PARAMETER on HmIP — so the binding is keyed
// on the blind kind and then resolved through the schema.
func TestBlindCombinedWriteFollowsTheProfileSchema(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		kind  cover.BlindKind
		field hmenum.Field
	}{
		{"HM", cover.BlindKindHM, hmenum.FieldLevelCombined},
		{"IP", cover.BlindKindIP, hmenum.FieldCombinedParameter},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := &paramRecorder{}
			ch := newSchemaBoundCoverChannel(t)
			b := cover.NewBlind(cover.BlindConfig{
				Channel: ch,
				Writer:  w,
				Kind:    tc.kind,
				Group: custom.RebasedChannelGroupConfig{
					Fields: map[hmenum.Field]custom.FieldValue{
						tc.field: custom.Bare(hmenum.ParameterLevelSlats),
					},
				},
			})
			if err := b.SetCombined(context.Background(), 0.5, 0.25, hmenum.CommandPriorityHigh); err != nil {
				t.Fatalf("SetCombined: %v", err)
			}
			if !w.wrote(hmenum.ParameterLevelSlats) {
				t.Fatalf("Blind combined write used %v; the schema named %s", w.params, hmenum.ParameterLevelSlats)
			}
		})
	}
}

// TestBlindCombinedClampsBothAxes pins what an out-of-range command puts on
// the wire: a bounded percent pair, never "L=150".
//
// Driven through SetPosition, which applies no bound of its own — the
// saturation is the one [custom.Position] defines, applied by the percent
// conversion in sendCombined's HmIP branch. Measured while writing this:
// the bound is applied more than once on this path (sendCombined bounds its
// arguments, and Cover.Position bounds again on read), so removing any single
// one of them leaves the wire output correct. This test pins the output, not
// one of those sites.
func TestBlindCombinedClampsBothAxes(t *testing.T) {
	t.Parallel()
	w := &paramRecorder{}
	ch := newSchemaBoundCoverChannel(t)
	b := cover.NewBlind(cover.BlindConfig{Channel: ch, Writer: w, Kind: cover.BlindKindIP})
	if err := b.SetPosition(context.Background(), 1.5, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetPosition: %v", err)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.values) != 1 {
		t.Fatalf("SetCombined issued %d writes, want 1", len(w.values))
	}
	if got := w.values[0]; got != "L2=0,L=100" {
		t.Fatalf("COMBINED_PARAMETER = %v, want \"L2=0,L=100\"", got)
	}
}
