// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package central

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// TestDeviceAddressAgreesWithHmtypesOnChannelAddresses pins the private
// deviceAddress helper against the canonical [hmtypes.DeviceAddress]. The
// helper searches backwards where hmtypes searches forwards, so the two
// agree only for as long as the addresses reaching the query facade carry at
// most one separator.
//
// The input set is bounded by the grammar hmtypes carries: the channel
// pattern `^[0-9a-zA-Z-]{5,20}:\d{1,3}$` and the device pattern
// `^[0-9a-zA-Z-]{5,20}$` (no colon), plus the empty and leading-colon edge
// cases. Multi-colon link addresses are deliberately excluded: whether they
// reach this helper is an open question that this test does not answer, and
// asserting agreement on them would assert a behaviour neither side promises.
func TestDeviceAddressAgreesWithHmtypesOnChannelAddresses(t *testing.T) {
	t.Parallel()

	for _, in := range []string{"ABC1234", "ABC1234:0", "ABC1234:1", "ABC1234:255", "", ":1"} {
		if got, want := deviceAddress(in), hmtypes.DeviceAddress(in); got != want {
			t.Errorf("deviceAddress(%q) = %q, hmtypes.DeviceAddress(%q) = %q", in, got, in, want)
		}
	}
}
