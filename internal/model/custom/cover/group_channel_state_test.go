// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package cover

import (
	"testing"

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
