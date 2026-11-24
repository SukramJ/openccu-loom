// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package cover

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

func newGroupLevelFloat(t *testing.T, address string) *generic.Float {
	t.Helper()
	return generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterLevel),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
}

// With use_group_channel_for_cover_state on, a cover that has a group
// level reports its position from the group channel; with it off, it
// reports from its own channel.
func TestCoverPositionFollowsGroupChannelToggle(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		useGroup  bool
		wantLevel float64
	}{
		{name: "group_channel_used", useGroup: true, wantLevel: 0.8},
		{name: "own_channel_used", useGroup: false, wantLevel: 0.5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c, _, ownLevel := newRig(t, "GRP0001:3", &stubWriter{}, CoverCaps)
			ownLevel.OnEvent(0.5)

			group := newGroupLevelFloat(t, "GRP0001:1")
			group.OnEvent(0.8)
			c.SetGroupLevel(group, tc.useGroup)

			pos, ok := c.Position()
			if !ok {
				t.Fatal("Position not observed")
			}
			if pos.Level() != tc.wantLevel {
				t.Fatalf("Position level=%v, want %v (useGroup=%v)", pos.Level(), tc.wantLevel, tc.useGroup)
			}
		})
	}
}

// A command is dropped only when the written channel and the group channel
// both show the target: the group channel reports the combined result of
// the virtual channels and can equal the target while the own channel
// holds another level, and the own channel can
// miss a move it did not command.
func TestCoverCommandDroppedOnlyWhenBothChannelsAtTarget(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := &countWriter{}
	c, _, ownLevel := newRig(t, "GRP0001:4", w, CoverCaps)
	ownLevel.OnEvent(0.25)
	group := newGroupLevelFloat(t, "GRP0001:3")
	group.OnEvent(0.0)
	c.SetGroupLevel(group, true)

	if pos, ok := c.Position(); !ok || pos.Level() != 0.0 {
		t.Fatalf("Position=%v ok=%v, want the group level 0", pos.Level(), ok)
	}

	if err := c.SetPosition(ctx, 0.0, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetPosition: %v", err)
	}
	if w.count != 1 {
		t.Fatalf("SetPosition(0) wrote %d time(s) with the own channel at 0.25, want 1", w.count)
	}

	ownLevel.OnEvent(0.25)
	if err := c.Close(ctx, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if w.count != 2 {
		t.Fatalf("Close wrote %d time(s) in total with the own channel at 0.25, want 2", w.count)
	}

	// A move the own channel does not reflect (e.g. a button on the
	// device): the group channel is off the target, so the command is
	// sent although the own channel already holds it.
	ownLevel.OnEvent(1.0)
	group.OnEvent(0.5)
	if err := c.SetPosition(ctx, 1.0, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetPosition: %v", err)
	}
	if w.count != 3 {
		t.Fatalf("SetPosition(1) wrote %d time(s) in total with the group channel at 0.5, want 3", w.count)
	}

	// Both channels at the target: nothing to send.
	ownLevel.OnEvent(1.0)
	group.OnEvent(1.0)
	if err := c.Open(ctx, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := c.SetPosition(ctx, 1.0, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetPosition: %v", err)
	}
	if w.count != 3 {
		t.Fatalf("Open/SetPosition(1) wrote %d time(s) in total with both channels open, want 3", w.count)
	}
}

// The tilt axis follows the same rule as the position axis.
func TestBlindTiltDroppedOnlyWhenBothChannelsAtTarget(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := &putWriter{}
	b := newBlindRig(t, "GRP0002:4", w, custom.CoverCapabilities{SupportsTilt: true}, BlindKindHM)
	b.OnEvent(0.1)
	b.level2.OnEvent(0.2)
	group := newGroupLevelFloat(t, "GRP0002:3")
	group.OnEvent(0.1)
	b.SetGroupLevel(group, true)
	group2 := newGroupLevelFloat(t, "GRP0002:3")
	group2.OnEvent(0.8)
	b.SetGroupLevel2(group2)

	if tilt, ok := b.TiltPosition(); !ok || tilt.Level() != 0.8 {
		t.Fatalf("TiltPosition=%v ok=%v, want the group level 0.8", tilt.Level(), ok)
	}

	if err := b.SetTilt(ctx, 0.8, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetTilt: %v", err)
	}
	if n := len(w.combinedCalls()); n != 1 {
		t.Fatalf("SetTilt(0.8) wrote %d time(s) with the own tilt at 0.2, want 1", n)
	}

	b.level2.OnEvent(0.8)
	if err := b.SetTilt(ctx, 0.8, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetTilt: %v", err)
	}
	if n := len(w.combinedCalls()); n != 1 {
		t.Fatalf("SetTilt(0.8) wrote %d time(s) in total with the own tilt at 0.8, want 1", n)
	}
}
