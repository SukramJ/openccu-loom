// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package custom

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestResolveFieldSlotBindsTheChannelItIsStandingOn drives
// [device.Channel.Sibling]'s receiver-wins rule through the production
// caller that depends on it.
//
// The schema keys its channel-field maps by absolute channel number, so
// a profile that names the custom DP's own channel arrives here as a
// number, not as "this one". A lookup that answered that number by
// walking the parent device only would resolve nothing whenever the
// channel is not (yet) reachable from a device — the field then binds
// to nothing, the accessor reports the feature as unsupported, and no
// test and no log line says so.
func TestResolveFieldSlotBindsTheChannelItIsStandingOn(t *testing.T) {
	t.Parallel()

	group := RebasedChannelGroupConfig{
		ChannelFields: map[int]map[hmenum.Field]FieldValue{
			4: {hmenum.FieldLevel2: Bare(hmenum.ParameterLevel2)},
		},
	}

	t.Run("attached", func(t *testing.T) {
		t.Parallel()
		dev := device.New(device.Config{InterfaceID: "ccu-BidCos-RF", Address: "VCU0000001"})
		ch := dev.AddChannel("VCU0000001:4", 4, "BLIND", hmenum.ParamsetKeyValues)
		target, param, ok := ResolveFieldSlot(ch, group, hmenum.FieldLevel2)
		if !ok || target != ch || param != hmenum.ParameterLevel2 {
			t.Fatalf("ResolveFieldSlot = (%v, %q, %v), want the channel itself and LEVEL_2", target, param, ok)
		}
	})

	t.Run("not yet attached to a device", func(t *testing.T) {
		t.Parallel()
		ch := device.NewChannel("VCU0000001:4", 4, "BLIND", hmenum.ParamsetKeyValues)
		target, param, ok := ResolveFieldSlot(ch, group, hmenum.FieldLevel2)
		if !ok || target != ch || param != hmenum.ParameterLevel2 {
			t.Fatalf("ResolveFieldSlot = (%v, %q, %v), want the channel itself and LEVEL_2", target, param, ok)
		}
	})
}
