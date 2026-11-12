// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	pload "github.com/SukramJ/openccu-loom/internal/payload"
)

// stubSource stands in for a channel's custom data point on an event. Only
// its presence matters to [announcedEvents]; the embedded interface is nil
// and never called.
type stubSource struct{ pload.Source }

// TestAnnouncedEventsNamesTheEntitiesOfOneDataPoint walks the four inputs
// that decide what a data point's event announces and holds the answer:
// the channel's entity only with a custom data point and an observed
// value, the data point's own only when it is eligible, and never with
// the custom data point still on the event.
func TestAnnouncedEventsNamesTheEntitiesOfOneDataPoint(t *testing.T) {
	t.Parallel()
	plain := mqtt.Event{Parameter: "HUMIDITY"}
	withCustom := mqtt.Event{Parameter: "HUMIDITY", Source: stubSource{}}

	cases := []struct {
		name               string
		ev                 mqtt.Event
		eligible, observed bool
		wantChannel        bool
		wantOwn            bool
	}{
		{"plain, eligible, observed", plain, true, true, false, true},
		{"plain, eligible, unobserved", plain, true, false, false, true},
		{"plain, not eligible", plain, false, true, false, false},
		{"custom, eligible, observed", withCustom, true, true, true, true},
		{"custom, not eligible, observed", withCustom, false, true, true, false},
		{"custom, eligible, unobserved", withCustom, true, false, false, true},
		{"custom, not eligible, unobserved", withCustom, false, false, false, false},
	}
	for _, tc := range cases {
		channelEntity, own := announcedEvents(tc.ev, tc.eligible, tc.observed)
		if (channelEntity != nil) != tc.wantChannel || (own != nil) != tc.wantOwn {
			t.Errorf("%s: channel entity %v, own %v; want %v, %v",
				tc.name, channelEntity != nil, own != nil, tc.wantChannel, tc.wantOwn)
			continue
		}
		if channelEntity != nil && channelEntity.Source == nil {
			t.Errorf("%s: the channel's entity is asked for without the custom data point", tc.name)
		}
		if own != nil && own.Source != nil {
			t.Errorf("%s: the data point's own entity is asked for with the custom data point, which announces the channel's entity again", tc.name)
		}
	}
	if withCustom.Source == nil {
		t.Fatal("announcedEvents cleared the caller's event")
	}
}
