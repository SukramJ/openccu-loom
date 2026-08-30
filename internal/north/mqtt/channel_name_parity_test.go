// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// customDP is the minimum a channel needs for the custom-DP predicates to
// answer: SetCustomDataPoint requires an attachable whose key names the
// channel.
type customDP struct{ key hmtypes.DataPointKey }

func (c *customDP) DataPointKey() hmtypes.DataPointKey { return c.key }
func (c *customDP) Attach(*device.Channel)             {}

// TestDisplayChannelNameAgreesWithTheModel measures the MQTT discovery name
// builder against the model's own custom-DP naming for the same channel.
//
// Both implement the rule documented at internal/model/device/namedata.go:88 —
// a device's only primary collapses to no marker, any other grouped channel
// carries ch<no> (primary) or vch<no> (secondary). They differ in the ORDER
// they test it, and each side's comment claims its order is the load-bearing
// one, so this compares them at real channels rather than by reading.
//
// It is a parity measurement, not a fix: the two are only safe to fold into
// one once they are shown to agree, and an entity's display name is what an
// operator sees in Home Assistant.
func TestDisplayChannelNameAgreesWithTheModel(t *testing.T) {
	t.Parallel()

	build := func(t *testing.T, groupMaster bool) *device.Channel {
		t.Helper()
		d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001", Model: "HmIP-X"})
		d.SetName("Galerie")
		primary := d.AddChannel("ABC0001:3", 3, "T", hmenum.ParamsetKeyValues)
		// The `:N` suffix is what puts the model on its marker path — without
		// it BuildCustomDataPointName returns before ever computing one, and
		// a comparison against that empty answer would measure the fixture
		// rather than the two rules.
		primary.SetName("Galerie:3")
		primary.AssignGroupNumber(3)
		primary.SetCustomDataPoint(&customDP{key: hmtypes.DataPointKey{ChannelAddress: primary.Address}})
		if groupMaster {
			return primary
		}
		secondary := d.AddChannel("ABC0001:4", 4, "T", hmenum.ParamsetKeyValues)
		secondary.SetName("Galerie:4")
		secondary.AssignGroupNumber(3)
		secondary.SetCustomDataPoint(&customDP{key: hmtypes.DataPointKey{ChannelAddress: secondary.Address}})
		return secondary
	}

	for _, c := range []struct {
		what        string
		groupMaster bool
	}{
		{"primary channel of its group", true},
		{"secondary channel of its group", false},
	} {
		t.Run(c.what, func(t *testing.T) {
			t.Parallel()
			ch := build(t, c.groupMaster)
			fromAdapter := displayChannelName(Event{
				Interface: "HmIP-RF", DeviceAddress: "ABC0001",
				ChannelNo: ch.Number, Channel: ch,
			})
			fromModel := device.BuildCustomDataPointName(ch, "", "").ParameterName

			// The model renders the marker into ParameterName; the adapter
			// returns it bare. An empty answer on both sides is agreement too
			// (the single-primary collapse).
			if fromAdapter == "" && fromModel == "" {
				return
			}
			if !strings.EqualFold(fromAdapter, fromModel) {
				t.Errorf("%s: adapter says %q, the model says %q — the two naming rules have diverged",
					c.what, fromAdapter, fromModel)
			}
		})
	}
}
