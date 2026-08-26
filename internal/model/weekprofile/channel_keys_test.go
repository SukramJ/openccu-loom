// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package weekprofile

import (
	"slices"
	"testing"
)

// TestChannelKeySetsAgree pins that the two independent definitions of the
// schedule channel-key set stay equal.
//
// [AllChannelKeys] generates them from a loop; channelKeyBitmask writes all
// twenty-four out by hand next to the bit each maps to. Nothing connected the
// two, so a ninth actor added to the loop — or a key renamed in the map —
// would leave the daemon offering a channel key it cannot turn into a
// WEEK_PROGRAM_CHANNEL_LOCKS bit, or holding a bit for a key it never offers.
// Neither surfaces as a failure anywhere: the schedule simply stops locking
// one channel.
//
// The name was the reason to look. "All" is a promise about a set, and this
// one rested on two people writing the same twenty-four strings twice.
func TestChannelKeySetsAgree(t *testing.T) {
	t.Parallel()

	generated := AllChannelKeys()
	if len(generated) != len(channelKeyBitmask) {
		t.Fatalf("AllChannelKeys returns %d keys, channelKeyBitmask holds %d — the two "+
			"definitions of the channel-key set have diverged",
			len(generated), len(channelKeyBitmask))
	}
	for _, key := range generated {
		if _, ok := channelKeyBitmask[key]; !ok {
			t.Errorf("AllChannelKeys offers %q, which channelKeyBitmask has no bit for: the "+
				"schedule would expose a channel it cannot lock", key)
		}
	}
	for key := range channelKeyBitmask {
		if !slices.Contains(generated, key) {
			t.Errorf("channelKeyBitmask holds a bit for %q, which AllChannelKeys never offers: "+
				"the bit is unreachable", key)
		}
	}
}
