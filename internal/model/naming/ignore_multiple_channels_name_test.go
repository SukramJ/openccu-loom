// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// This file exercises the real production naming methods on [device.Channel]
// that decide the multi-channel name suffix — specifically the
// IgnoreMultipleChannelsForName opt-out branch that the MQTT discovery builder
// (internal/north/mqtt/discovery_aggregate.go displayChannelName) consumes. A
// custom DP that opts into IgnoreMultipleChannelsForName suppresses the "ch<N>"
// suffix even on a multi-primary device, so a two-channel lock renders as
// "<Lock>" / "<Lock>" instead of "<Lock> ch1" / "<Lock> ch2". Mirrors the Lock
// custom DP (`Lock.IgnoreMultipleChannelsForName` returns true) and the Python
// `_ignore_multiple_channels_for_name` flag (model/data_point.py:542,
// model/custom/lock.py:65).
package naming_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// fakeIgnoreNameDP is a lock-like custom DP: it reports an HA component (so the
// channel participates in HasSinglePrimaryCustomDP counting) and opts into
// IgnoreMultipleChannelsForName exactly as [custom/lock.Lock] does.
type fakeIgnoreNameDP struct {
	key hmtypes.DataPointKey
}

func (f *fakeIgnoreNameDP) DataPointKey() hmtypes.DataPointKey  { return f.key }
func (f *fakeIgnoreNameDP) HAComponent() string                 { return "lock" }
func (f *fakeIgnoreNameDP) IgnoreMultipleChannelsForName() bool { return true }

func attachIgnoreNameDP(ch *device.Channel) {
	ch.SetCustomDataPoint(&fakeIgnoreNameDP{
		key: hmtypes.DataPointKey{ChannelAddress: ch.Address, Parameter: "lock"},
	})
}

// newMultiLockDevice builds a two-primary lock device where each lock channel
// is its own group master. Both channels host a lock custom DP that opts into
// IgnoreMultipleChannelsForName.
func newMultiLockDevice(address string) (ch1, ch2 *device.Channel) {
	d := device.New(device.Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     address,
		Model:       "HmIP-MOD-TM",
		Name:        "Tor",
	})
	c1 := d.AddChannel(address+":1", 1, "LOCK_TRANSCEIVER", hmenum.ParamsetKeyValues)
	c2 := d.AddChannel(address+":2", 2, "LOCK_TRANSCEIVER", hmenum.ParamsetKeyValues)
	// Each lock is an independent primary (GroupNo == own Number).
	c1.AssignGroupNumber(1)
	c2.AssignGroupNumber(2)
	attachIgnoreNameDP(c1)
	attachIgnoreNameDP(c2)
	return c1, c2
}

// TestChannelIgnoreMultipleChannelsForNameSuppressesSuffix drives the REAL
// device.Channel methods (not a copy of displayChannelName) for a multi-primary
// device whose custom DP opts out of the ch<N> suffix. The four booleans
// asserted here are precisely the production branch condition: a primary channel
// on a multi-primary device (HasSinglePrimaryCustomDP == false) whose DP returns
// IgnoreMultipleChannelsForName == true drops the suffix.
func TestChannelIgnoreMultipleChannelsForNameSuppressesSuffix(t *testing.T) {
	t.Parallel()

	ch1, ch2 := newMultiLockDevice("VCU0099000")

	for _, ch := range []*device.Channel{ch1, ch2} {
		if !ch.IsCustomDPPrimaryChannel() {
			t.Errorf("%s: IsCustomDPPrimaryChannel = false, want true", ch.Address)
		}
		if ch.IsCustomDPSecondaryChannel() {
			t.Errorf("%s: IsCustomDPSecondaryChannel = true, want false", ch.Address)
		}
		// Two lock primaries share the "lock" HA component → not single.
		if ch.HasSinglePrimaryCustomDP() {
			t.Errorf("%s: HasSinglePrimaryCustomDP = true, want false (two lock primaries)", ch.Address)
		}
		// The opt-out that suppresses the ch<N> suffix on a multi-primary device.
		if !ch.IgnoreMultipleChannelsForName() {
			t.Errorf("%s: IgnoreMultipleChannelsForName = false, want true", ch.Address)
		}
		// The suffix the production builder derives from the above booleans.
		if got := displayChannelName(ch, ch.Number); got != "" {
			t.Errorf("%s: displayChannelName = %q, want \"\" (suffix suppressed by opt-out)", ch.Address, got)
		}
	}
}

// TestChannelWithoutIgnoreOptOutKeepsSuffix is the negative control: a
// multi-primary device whose custom DP does NOT implement
// IgnoreMultipleChannelsForName still gets the ch<N> suffix, proving the opt-out
// is what drives the suppression (and that device.Channel returns false when the
// DP lacks the optional method).
func TestChannelWithoutIgnoreOptOutKeepsSuffix(t *testing.T) {
	t.Parallel()

	d := device.New(device.Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "VCU0099001",
		Model:       "HmIP-DRSI4",
		Name:        "Schalter",
	})
	c1 := d.AddChannel("VCU0099001:1", 1, "SWITCH_TRANSMITTER", hmenum.ParamsetKeyValues)
	c2 := d.AddChannel("VCU0099001:2", 2, "SWITCH_TRANSMITTER", hmenum.ParamsetKeyValues)
	c1.AssignGroupNumber(1)
	c2.AssignGroupNumber(2)
	// A plain switch DP: implements HAComponent but NOT IgnoreMultipleChannelsForName.
	c1.SetCustomDataPoint(&fakeCustomDP{key: hmtypes.DataPointKey{ChannelAddress: c1.Address, Parameter: "switch"}, haComponent: "switch"})
	c2.SetCustomDataPoint(&fakeCustomDP{key: hmtypes.DataPointKey{ChannelAddress: c2.Address, Parameter: "switch"}, haComponent: "switch"})

	if c1.IgnoreMultipleChannelsForName() {
		t.Error("switch DP without the optional method must report IgnoreMultipleChannelsForName == false")
	}
	if got := displayChannelName(c1, c1.Number); got != "ch1" {
		t.Errorf("displayChannelName = %q, want \"ch1\" (no opt-out, multi-primary)", got)
	}
}
