// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"strconv"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// customDP is the minimum a channel needs for the custom-DP predicates to
// answer. SetCustomDataPoint requires an attachable whose key names the
// channel, and HasSinglePrimaryCustomDP counts only siblings that report
// an HA component — a fake without one is never counted, which would make
// every device look like it had no primary at all.
type customDP struct {
	key       hmtypes.DataPointKey
	component string
}

func (c *customDP) DataPointKey() hmtypes.DataPointKey { return c.key }
func (c *customDP) Attach(*device.Channel)             {}
func (c *customDP) HAComponent() string                { return c.component }

// addCustomChannel attaches a channel carrying a custom data point.
// groupNo is what decides primary from secondary: a channel whose group
// number is its own number is the group's primary, any other member is a
// secondary mirror.
func addCustomChannel(d *device.Device, no, groupNo int, name string) *device.Channel {
	addr := d.Address + ":" + strconv.Itoa(no)
	ch := d.AddChannel(addr, no, "T", hmenum.ParamsetKeyValues)
	ch.SetName(name)
	ch.AssignGroupNumber(groupNo)
	ch.SetCustomDataPoint(&customDP{
		key:       hmtypes.DataPointKey{ChannelAddress: addr},
		component: "switch",
	})
	return ch
}

func displayNameOf(ch *device.Channel) string {
	return displayChannelName(Event{
		Interface: "HmIP-RF", DeviceAddress: "ABC0001",
		ChannelNo: ch.Number, Channel: ch,
	})
}

// twoPrimaries builds a device with two independent primary custom-DPs —
// the HmIP-DRSI4 shape — so neither channel is the device's only primary
// and the marker rule is in play.
func twoPrimaries(t *testing.T, name3, name4 string) (ch3, ch4 *device.Channel) {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001", Model: "HmIP-X"})
	d.SetName("Galerie")
	return addCustomChannel(d, 3, 3, name3), addCustomChannel(d, 4, 4, name4)
}

// TestDisplayChannelNameUsesTheOperatorsChannelName is the behaviour that
// changed: a channel the operator named on the CCU is published under that
// name, not under the `ch<N>` marker.
//
// The marker exists to keep two channels of one device apart when neither
// carries a label — the CCU reports the bare `<device>:<no>` form for a
// channel nobody renamed, and those slugify onto one entity_id in Home
// Assistant. A named channel already distinguishes itself, and REST, the
// SPA and the model all showed that name while MQTT alone showed `ch3`.
func TestDisplayChannelNameUsesTheOperatorsChannelName(t *testing.T) {
	t.Parallel()

	ch3, ch4 := twoPrimaries(t, "Leselampe", "Bücherregal")

	if got := displayNameOf(ch3); got != "Leselampe" {
		t.Errorf("named channel 3: got %q, want %q", got, "Leselampe")
	}
	if got := displayNameOf(ch4); got != "Bücherregal" {
		t.Errorf("named channel 4: got %q, want %q", got, "Bücherregal")
	}
}

// TestDisplayChannelNameFallsBackToTheMarker pins the other half: with no
// operator name the CCU reports `<device>:<no>`, and the marker is what
// keeps the two channels apart.
func TestDisplayChannelNameFallsBackToTheMarker(t *testing.T) {
	t.Parallel()

	ch3, ch4 := twoPrimaries(t, "Galerie:3", "Galerie:4")

	if got := displayNameOf(ch3); got != "ch3" {
		t.Errorf("unnamed channel 3: got %q, want %q", got, "ch3")
	}
	if got := displayNameOf(ch4); got != "ch4" {
		t.Errorf("unnamed channel 4: got %q, want %q", got, "ch4")
	}
}

// TestDisplayChannelNameMarksASecondaryMirror pins the secondary shape: a
// group member that is not its group's primary carries `vch<N>` when it
// has no operator name, so the mirror channel stays distinguishable from
// the primary it mirrors.
func TestDisplayChannelNameMarksASecondaryMirror(t *testing.T) {
	t.Parallel()

	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001", Model: "HmIP-X"})
	d.SetName("Galerie")
	primary := addCustomChannel(d, 3, 3, "Galerie:3")
	secondary := addCustomChannel(d, 4, 3, "Galerie:4")

	// The device now hosts exactly one primary, so the primary itself
	// collapses to the device name — the HmIP-PSM shape.
	if got := displayNameOf(primary); got != "" {
		t.Errorf("the device's only primary: got %q, want an empty name", got)
	}
	if got := displayNameOf(secondary); got != "vch4" {
		t.Errorf("secondary mirror: got %q, want %q", got, "vch4")
	}
}

// TestDisplayChannelNameMatchesTheModel keeps the adapter from growing its
// own copy of the rule again.
//
// The copy it used to carry is what this pins against: it re-derived the
// marker from the classification alone and never consulted the channel's
// name, so every channel of a multi-primary device was published as
// `ch<N>`. The model composes the name including the operator's label, and
// the adapter's answer must be exactly that.
func TestDisplayChannelNameMatchesTheModel(t *testing.T) {
	t.Parallel()

	named3, named4 := twoPrimaries(t, "Leselampe", "Bücherregal")
	bare3, bare4 := twoPrimaries(t, "Galerie:3", "Galerie:4")

	for _, ch := range []*device.Channel{named3, named4, bare3, bare4} {
		want := device.BuildCustomDataPointName(ch, "", "").Name()
		if got := displayNameOf(ch); got != want {
			t.Errorf("channel %d (%q): adapter says %q, the model says %q",
				ch.Number, ch.Name(), got, want)
		}
	}
}
