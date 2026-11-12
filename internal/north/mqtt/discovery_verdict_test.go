// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"bytes"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestDiscoveryVerdictNamesTheRuleBehindEveryRefusal drives one event through each
// gate of the per-parameter path and holds the verdict to the gate's
// reason. Build is asked the same question each time: the two are one
// pass, and a refusal of one that the other announces is the defect this
// keeps out.
func TestDiscoveryVerdictNamesTheRuleBehindEveryRefusal(t *testing.T) {
	t.Parallel()
	db := NewDefaultDiscoveryBuilder(NewTopicBuilder("gh"), "ccu")
	base := func(mod func(*Event)) Event {
		ev := Event{
			Central: "ccu", Interface: "HmIP-RF", DeviceAddress: "0001ABCD", ChannelNo: 3,
			Parameter: "STATE", Category: hmenum.DataPointCategorySwitch, Writable: true,
		}
		mod(&ev)
		return ev
	}
	cases := []struct {
		name string
		ev   Event
		want DiscoveryReason
	}{
		{"announced", base(func(*Event) {}), ""},
		{"usage no_create", base(func(e *Event) { e.Usage = hmenum.DataPointUsageNoCreate }), DiscoveryUsageNoCreate},
		{"usage ignored", base(func(e *Event) { e.Usage = hmenum.DataPointUsageIgnored }), DiscoveryUsageIgnored},
		{"custom data point primary", base(func(e *Event) { e.Usage = hmenum.DataPointUsageCDPPrimary }), DiscoveryPartOfChannelEntity},
		{"custom data point secondary", base(func(e *Event) { e.Usage = hmenum.DataPointUsageCDPSecondary }), DiscoveryPartOfChannelEntity},
		{"event-only press typed as button", base(func(e *Event) {
			e.Parameter, e.Category, e.Usage = "PRESS_LOCK", hmenum.DataPointCategoryButton, hmenum.DataPointUsageEvent
		}), DiscoveryEventOnlyPress},
		{"no component", base(func(e *Event) {
			e.Parameter, e.Category = "COMBINED_PARAMETER", hmenum.DataPointCategoryAction
		}), DiscoveryNoComponent},
	}
	for _, tc := range cases {
		v := db.decide(tc.ev)
		if v.Reason != tc.want || v.OK != (tc.want == "") {
			t.Errorf("%s: OK=%v reason=%q; want reason %q", tc.name, v.OK, v.Reason, tc.want)
		}
		component, nodeID, objectID, payload, ok := db.Build(tc.ev)
		if ok != v.OK || component != v.Component || nodeID != v.NodeID || objectID != v.ObjectID || !bytes.Equal(payload, v.Payload) {
			t.Errorf("%s: Build and the verdict disagree (Build ok=%v %s/%s/%s, verdict ok=%v %s/%s/%s)",
				tc.name, ok, component, nodeID, objectID, v.OK, v.Component, v.NodeID, v.ObjectID)
		}
		if v.OK && (v.Component == "" || len(v.Payload) == 0) {
			t.Errorf("%s: announced without a component or a payload", tc.name)
		}
	}
}
