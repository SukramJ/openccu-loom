// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// namedChannelInspector adds an operator channel name to the base
// fakeChannelInspector so the VR-button naming path can be exercised
// with and without a CCU-assigned channel label.
type namedChannelInspector struct {
	*fakeChannelInspector
	name string
}

func (n namedChannelInspector) ChannelName() string { return n.name }

func vrNamingEvent(param string, ch ChannelInspector) Event {
	return Event{
		Central:       "ccu",
		Interface:     "HmIP-RF",
		DeviceAddress: "HmIP-RCV-1",
		DeviceName:    "Arbeitszimmer Markus OttoIP",
		Model:         "HmIP-RCV-50",
		ChannelNo:     10,
		Parameter:     param,
		Category:      hmenum.DataPointCategoryButton,
		Usage:         hmenum.DataPointUsageDataPoint,
		Writable:      true,
		Device:        fakeVirtualRemote{vr: true},
		Channel:       ch,
	}
}

func vrButtonName(t *testing.T, db *DefaultDiscoveryBuilder, ev Event) string {
	t.Helper()
	item := db.BuildPressButton(ev)
	if !item.OK {
		t.Fatalf("BuildPressButton returned OK=false for %s", ev.Parameter)
	}
	var m map[string]any
	if err := json.Unmarshal(item.Payload, &m); err != nil {
		t.Fatalf("payload: %v", err)
	}
	name, _ := m["name"].(string)
	return name
}

// TestVirtualRemoteButtonNameIsChannelUnique is the parity tripwire for
// the `_2` / `_3` entity-id suffixes HA appended to virtual-remote
// buttons. The plain parameter label ("Press Short") is identical for
// every VR channel, so VRs that share device names collided on the same
// friendly_name. The name must always carry channel disambiguation —
// the operator channel name when present, otherwise `ch<N>`.
func TestVirtualRemoteButtonNameIsChannelUnique(t *testing.T) {
	t.Parallel()
	db := NewDefaultDiscoveryBuilder(NewTopicBuilder("openccu-loom"), "ccu")
	db.SetHubInfoFor("ccu", HubInfo{Serial: "3014F711A0001234"})

	t.Run("unnamed_channel_carries_ch_suffix", func(t *testing.T) {
		t.Parallel()
		base := &fakeChannelInspector{params: map[string]struct{}{"PRESS_SHORT": {}, "PRESS_LONG": {}}}
		shortName := vrButtonName(t, db, vrNamingEvent("PRESS_SHORT", base))
		longName := vrButtonName(t, db, vrNamingEvent("PRESS_LONG", base))
		if !strings.Contains(shortName, "ch10") {
			t.Fatalf("PRESS_SHORT name %q must carry ch10 disambiguation", shortName)
		}
		if shortName == longName {
			t.Fatalf("PRESS_SHORT and PRESS_LONG must not share a name (%q)", shortName)
		}
	})

	t.Run("named_channel_uses_operator_name", func(t *testing.T) {
		t.Parallel()
		ch := namedChannelInspector{
			fakeChannelInspector: &fakeChannelInspector{params: map[string]struct{}{"PRESS_SHORT": {}, "PRESS_LONG": {}}},
			name:                 "Signal Hüllschutz aktiviert",
		}
		name := vrButtonName(t, db, vrNamingEvent("PRESS_SHORT", ch))
		if !strings.HasPrefix(name, "Signal Hüllschutz aktiviert") {
			t.Fatalf("named-channel VR button name %q must start with the operator channel name", name)
		}
	})

	// A bare `<base>:<no>` channel name (CCU default for an un-renamed
	// channel, e.g. "KearneyIP:10") slugifies to the base alone in HA, so
	// it must fall back to `ch<N>` exactly like an empty channel name.
	t.Run("bare_address_no_channel_name_falls_back_to_ch", func(t *testing.T) {
		t.Parallel()
		ch := namedChannelInspector{
			fakeChannelInspector: &fakeChannelInspector{params: map[string]struct{}{"PRESS_SHORT": {}, "PRESS_LONG": {}}},
			name:                 "KearneyIP:10",
		}
		name := vrButtonName(t, db, vrNamingEvent("PRESS_SHORT", ch))
		if strings.Contains(name, "KearneyIP:10") {
			t.Fatalf("bare-address-no channel name must not appear verbatim in %q", name)
		}
		if !strings.Contains(name, "ch10") {
			t.Fatalf("bare-address-no VR button name %q must fall back to ch10", name)
		}
	})
}

// TestChannelNameIsBareAddressNo pins the reference
// _check_channel_name_with_channel_no detection used to drop CCU-default
// `<base>:<no>` channel names back to the ch<N> discriminator.
func TestChannelNameIsBareAddressNo(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		"KearneyIP:10":  true,
		"Foo:0":         true,
		"Schalter":      false,
		"Foo:Bar":       false,
		"a:b:2":         false,
		"Trailing:":     false,
		"":              false,
		"Wohnzimmer 12": false,
	}
	for in, want := range cases {
		if got := channelNameIsBareAddressNo(in); got != want {
			t.Errorf("channelNameIsBareAddressNo(%q)=%v want %v", in, got, want)
		}
	}
}
