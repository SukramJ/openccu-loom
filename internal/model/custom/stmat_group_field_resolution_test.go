// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package custom_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestStMatGroupFieldReadsTheGroupWideBlockFirst pins that a group field
// declared group-wide is found.
//
// Four device profiles resolved this by hand and only the light profile read
// the group-wide block; cover, valve and switch looked at the per-channel
// blocks alone. No shipped profile declares such a field group-wide today, so
// the four agreed by accident — the first profile to do so would have bound on
// one family and silently bound nothing on the other three.
func TestStMatGroupFieldReadsTheGroupWideBlockFirst(t *testing.T) {
	t.Parallel()

	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "GRP0001"})
	own := d.AddChannel("GRP0001:1", 1, "SWITCH_VIRTUAL_RECEIVER", hmenum.ParamsetKeyValues)
	d.AddChannel("GRP0001:3", 3, "SWITCH_TRANSCEIVER", hmenum.ParamsetKeyValues)

	// Group-wide: the field names a parameter of the calling channel.
	groupWide := custom.RebasedChannelGroupConfig{
		Fields: map[hmenum.Field]custom.FieldValue{hmenum.FieldGroupState: {Parameter: hmenum.ParameterState}},
	}
	param, ch, ok := custom.ResolveGroupFieldSlot(own, groupWide, hmenum.FieldGroupState)
	if !ok {
		t.Fatal("a group-wide declaration must resolve")
	}
	if param != hmenum.ParameterState || ch.Number != 1 {
		t.Errorf("group-wide resolved to %s on channel %d, want STATE on 1", param, ch.Number)
	}

	// Per-channel: the field names the group's own state channel.
	perChannel := custom.RebasedChannelGroupConfig{
		ChannelFields: map[int]map[hmenum.Field]custom.FieldValue{
			3: {hmenum.FieldGroupState: {Parameter: hmenum.ParameterState}},
		},
	}
	param, ch, ok = custom.ResolveGroupFieldSlot(own, perChannel, hmenum.FieldGroupState)
	if !ok {
		t.Fatal("a per-channel declaration must resolve")
	}
	if param != hmenum.ParameterState || ch.Number != 3 {
		t.Errorf("per-channel resolved to %s on channel %d, want STATE on 3", param, ch.Number)
	}

	// Undeclared, and a channel the device does not carry, both report nothing.
	if _, _, ok := custom.ResolveGroupFieldSlot(own, custom.RebasedChannelGroupConfig{}, hmenum.FieldGroupState); ok {
		t.Error("an undeclared field must not resolve")
	}
	absent := custom.RebasedChannelGroupConfig{
		ChannelFields: map[int]map[hmenum.Field]custom.FieldValue{
			9: {hmenum.FieldGroupState: {Parameter: hmenum.ParameterState}},
		},
	}
	if _, _, ok := custom.ResolveGroupFieldSlot(own, absent, hmenum.FieldGroupState); ok {
		t.Error("a channel the device does not carry must not resolve")
	}
}
