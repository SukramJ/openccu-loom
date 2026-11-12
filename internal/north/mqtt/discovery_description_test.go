// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// hideParameter is a visibility set that hides one parameter.
type hideParameter struct{ param hmenum.Parameter }

func (h hideParameter) Visible(_, _ string, _ hmenum.ParamsetKey, p hmenum.Parameter) bool {
	return p != h.param
}

func (h hideParameter) VisibleForChannel(_, _ string, _ int, _ hmenum.ParamsetKey, p hmenum.Parameter) bool {
	return p != h.param
}

func switchEvent() Event {
	return Event{
		Central: "ccu-01", Interface: "HmIP-RF", DeviceAddress: "000A", ChannelNo: 1, ChannelAddress: "000A:1",
		Parameter: "STATE", Category: hmenum.DataPointCategorySwitch, Writable: true,
	}
}

// discoveryTopics are the config topics a recorder saw, sorted.
func discoveryTopics(pub *mockPublisher) []string {
	var out []string
	for _, r := range pub.recorded() {
		if strings.HasSuffix(r.topic, "/config") {
			out = append(out, r.topic)
		}
	}
	sort.Strings(out)
	return out
}

// TestDataPointDescriptionNamesTheBridgeGate holds each of the bridge's own
// gates to its reason, and holds announcing to the description: what the
// description withholds is not published, what it announces is.
func TestDataPointDescriptionNamesTheBridgeGate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		opt  func(*BridgeConfig)
		want DiscoveryReason
	}{
		{"announced", func(*BridgeConfig) {}, ""},
		{"hidden parameter", func(c *BridgeConfig) { c.Visibility = hideParameter{param: "STATE"} }, DiscoveryHiddenParameter},
		{"hidden channel", func(c *BridgeConfig) {
			c.ChannelHidden = func(_, ch string) bool { return ch == "000A:1" }
		}, DiscoveryChannelHidden},
		{"discovery off", func(c *BridgeConfig) { c.HADiscoveryEnabled = false }, DiscoveryDisabled},
		{"refused by the builder", func(*BridgeConfig) {}, DiscoveryUsageIgnored},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b, pub := newTestBridge(t, func(c *BridgeConfig) {
				c.DiscoveryBuilder = NewDefaultDiscoveryBuilder(NewTopicBuilder(c.Base), c.CentralName)
				tc.opt(c)
			})
			ev := switchEvent()
			if tc.want == DiscoveryUsageIgnored {
				ev.Usage = hmenum.DataPointUsageIgnored
			}
			d := b.describeDataPointDiscovery(ev)
			if d.Verdict.Reason != tc.want || d.Verdict.OK != (tc.want == "") {
				t.Fatalf("verdict OK=%v reason=%q; want reason %q", d.Verdict.OK, d.Verdict.Reason, tc.want)
			}
			if err := b.PublishState(context.Background(), ev); err != nil {
				t.Fatalf("PublishState: %v", err)
			}
			var want []string
			if d.Verdict.OK {
				want = append(want, b.topics.DiscoveryConfig(d.Verdict.Component, d.Verdict.NodeID, d.Verdict.ObjectID))
			}
			for _, c := range d.Companions {
				want = append(want, b.topics.DiscoveryConfig(c.Component, c.NodeID, c.ObjectID))
			}
			sort.Strings(want)
			if got := discoveryTopics(pub); !slices.Equal(got, want) {
				t.Fatalf("published %v; the description announces %v", got, want)
			}
		})
	}
}

// The channel's custom data point is asked directly only with an event that
// carries one, and a channel event entity only with discovery on.
func TestDirectDescriptionsNameTheirGates(t *testing.T) {
	t.Parallel()
	b, _ := newTestBridge(t, func(c *BridgeConfig) {
		c.DiscoveryBuilder = NewDefaultDiscoveryBuilder(NewTopicBuilder(c.Base), c.CentralName)
	})
	if got := b.describeCustomDPDiscovery(switchEvent()).Verdict.Reason; got != DiscoveryNoCustomDataPoint {
		t.Errorf("custom data point without a source: reason %q; want %q", got, DiscoveryNoCustomDataPoint)
	}
	off, _ := newTestBridge(t, func(c *BridgeConfig) { c.HADiscoveryEnabled = false })
	if got := off.describeChannelEventDiscovery(switchEvent()).Verdict.Reason; got != DiscoveryDisabled {
		t.Errorf("channel event with discovery off: reason %q; want %q", got, DiscoveryDisabled)
	}
	if got := off.describeCustomDPDiscovery(switchEvent()).Verdict.Reason; got != DiscoveryDisabled {
		t.Errorf("custom data point with discovery off: reason %q; want %q", got, DiscoveryDisabled)
	}
}
