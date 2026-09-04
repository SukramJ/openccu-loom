// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package weekprofile

import (
	"fmt"
	"testing"
)

// TestHmSchChannelLockGridIsTheStrideThreeSchemeAndNothingElse pins what the
// WEEK_PROGRAM_CHANNEL_LOCKS table claims to be, and pins the edge of that
// claim.
//
// The firmware derives the bit from the channel's position in the device's
// schedule-relevant channel list (iseHmIPWeeklyProgram.js:357, :517-555). For
// an ordinary multi-actor virtual-receiver device that list is the actors'
// channels in order and the editor's own non-expert view walks it in steps of
// three (:361-364, :614), which is the grid [channelKeyBitmask] spells: actor
// N, sub S → bit 3(N-1)+(S-1).
//
// The second half is the part that matters. Devices exist whose relevant-list
// position is NOT that grid — HmIP-DLP, HmIP-FWI and HmIP-DRG-DALI, see the
// table's own comment — and their keys must keep failing loudly here rather
// than resolving to a plausible wrong bit. A key with a fourth sub-channel
// (HmIP-RGBW mints `1_4`) or a ninth actor (HmIP-DRG-DALI reaches channel 48)
// has no correct answer in a 24-bit stride-3 grid: giving `1_4` bit 3 would
// silently move actor 2 onto actor 1's channels.
func TestHmSchChannelLockGridIsTheStrideThreeSchemeAndNothingElse(t *testing.T) {
	t.Parallel()

	seen := make(map[uint32]string, len(channelKeyBitmask))
	for actor := 1; actor <= 8; actor++ {
		for sub := 1; sub <= 3; sub++ {
			key := hmSchChannelKey(actor, sub)
			bit, ok := ChannelKeyToBitmask(key)
			if !ok {
				t.Fatalf("key %q has no bit; the grid is 8 actors x 3 sub-channels", key)
			}
			want := uint32(1) << uint(3*(actor-1)+(sub-1)) //nolint:gosec // 0..23
			if bit != want {
				t.Errorf("key %q -> bit %d, want %d (bit = 3*(actor-1)+(sub-1), the "+
					"firmware's stride-3 scheme for an ordinary virtual-receiver device)",
					key, bit, want)
			}
			if prev, dup := seen[bit]; dup {
				t.Errorf("keys %q and %q share bit %d", prev, key, bit)
			}
			seen[bit] = key
		}
	}
	if len(channelKeyBitmask) != len(seen) {
		t.Errorf("channelKeyBitmask holds %d entries, the 8x3 grid has %d — an extra "+
			"entry addresses a channel this scheme cannot place",
			len(channelKeyBitmask), len(seen))
	}

	for _, key := range []string{"1_4", "9_1", "1_0", "0_1"} {
		if bit, ok := ChannelKeyToBitmask(key); ok {
			t.Errorf("key %q resolved to bit %d; it is outside the 8x3 grid and the "+
				"firmware places such a channel by its position in the device's "+
				"relevant-channel list, not by this formula — the write must fail "+
				"loudly instead of addressing a neighbouring channel", key, bit)
		}
	}
}

// hmSchChannelKey spells an `<actor>_<sub>` channel key.
func hmSchChannelKey(actor, sub int) string {
	return fmt.Sprintf("%d_%d", actor, sub)
}
