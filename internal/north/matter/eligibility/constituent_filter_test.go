// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package eligibility

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

type fakeUsageSource struct{ u hmenum.DataPointUsage }

func (f fakeUsageSource) Usage() hmenum.DataPointUsage { return f.u }

// TestHideFromMatter pins the Matter visibility gate against the DataPointUsage
// taxonomy across both the expose_secondary_channels flag and whether the DP's
// channel already owns a custom DP.
func TestHideFromMatter(t *testing.T) {
	t.Parallel()
	// want[usage] indexed by [channelHasCustom][exposeSecondary].
	cases := []struct {
		usage hmenum.DataPointUsage
		// hide[hasCustom][exposeSecondary]
		hide [2][2]bool
	}{
		// Service / status / overflow params: hidden everywhere, always.
		{hmenum.DataPointUsageIgnored, [2][2]bool{{true, true}, {true, true}}},
		// Consumed by an aggregating parent: hidden when the channel owns the
		// parent (would duplicate it) OR the flag is off; revealed only on a
		// bare secondary channel with the flag on.
		{hmenum.DataPointUsageNoCreate, [2][2]bool{{true, false}, {true, true}}},
		// Secondary member / group-state transmitter: hidden unless the flag is on.
		{hmenum.DataPointUsageCDPSecondary, [2][2]bool{{true, false}, {true, false}}},
		{hmenum.DataPointUsageCDPState, [2][2]bool{{true, false}, {true, false}}},
		// Entity-creating usages: never hidden.
		{hmenum.DataPointUsageCDPVisible, [2][2]bool{{false, false}, {false, false}}},
		{hmenum.DataPointUsageCDPPrimary, [2][2]bool{{false, false}, {false, false}}},
		{hmenum.DataPointUsageDataPoint, [2][2]bool{{false, false}, {false, false}}},
		{hmenum.DataPointUsageEvent, [2][2]bool{{false, false}, {false, false}}},
	}
	for _, c := range cases {
		for hasCustom := range 2 {
			for expose := range 2 {
				got := hideFromMatter(fakeUsageSource{c.usage}, hasCustom == 1, expose == 1)
				want := c.hide[hasCustom][expose]
				if got != want {
					t.Errorf("hideFromMatter(%s, hasCustom=%v, exposeSecondary=%v) = %v, want %v",
						c.usage, hasCustom == 1, expose == 1, got, want)
				}
			}
		}
	}
	// A source that does not report a Usage() is never hidden here.
	if hideFromMatter("no-usage-method", false, false) {
		t.Error("a source without Usage() must not be hidden")
	}
	if hideFromMatter(nil, true, true) {
		t.Error("a nil source must not be hidden")
	}
}
