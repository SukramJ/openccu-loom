// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// addressParserCases is the shared table every address parser in this
// package is measured against.
var addressParserCases = []string{
	"DEV001:3",
	"DEV001",
	"DEV:abc",
	"DEV001:",
	"DEV001:None",
	"0001ABCD:0",
}

// TestLocalAddressParsersProjectTheCanonicalGrammar pins each of this
// package's address parsers as a documented projection of pkg/hmtypes.
//
// Four hand-rolled copies of "split DEV:N" lived here, and nothing compared
// them: a divergence in what counts as a channel suffix shows up as a
// channel lookup that silently resolves to the wrong object, never as a
// failing test. The projections asserted below are what each caller's
// return shape needs; the grammar underneath them must be the canonical
// one.
func TestLocalAddressParsersProjectTheCanonicalGrammar(t *testing.T) {
	t.Parallel()

	for _, addr := range addressParserCases {
		t.Run(addr, func(t *testing.T) {
			t.Parallel()

			canonNo, canonOK := hmtypes.ChannelNo(addr)
			canonDev, canonSplitNo, canonSplitOK := hmtypes.SplitChannelAddress(addr)

			// parseChannel: address unchanged, channel number or 0.
			wantNo := 0
			if canonOK {
				wantNo = canonNo
			}
			if gotAddr, gotNo := parseChannel(addr); gotAddr != addr || gotNo != wantNo {
				t.Errorf("parseChannel = (%q, %d), want (%q, %d)", gotAddr, gotNo, addr, wantNo)
			}

			// deviceAddrAndChannel: device part, channel number or 0.
			wantSplitNo := 0
			if canonSplitOK {
				wantSplitNo = canonSplitNo
			}
			if gotDev, gotNo := deviceAddrAndChannel(addr); gotDev != canonDev || gotNo != wantSplitNo {
				t.Errorf("deviceAddrAndChannel = (%q, %d), want (%q, %d)",
					gotDev, gotNo, canonDev, wantSplitNo)
			}

			// splitChannelAddress: same, except an address the canonical
			// parser rejects is handed back whole so it resolves nothing.
			wantDev := addr
			if canonSplitOK {
				wantDev = canonDev
			}
			if gotDev, gotNo := splitChannelAddress(addr); gotDev != wantDev || gotNo != wantSplitNo {
				t.Errorf("splitChannelAddress = (%q, %d), want (%q, %d)",
					gotDev, gotNo, wantDev, wantSplitNo)
			}

			// splitChannel: the canonical answer verbatim.
			if gotNo, gotOK := splitChannel(addr); gotNo != canonNo || gotOK != canonOK {
				t.Errorf("splitChannel = (%d, %v), want (%d, %v)", gotNo, gotOK, canonNo, canonOK)
			}
		})
	}
}

// TestScheduleChannelAddressComposesTheDeviceRoot pins the composer side:
// the device-root sentinel must compose to the bare device address, not to
// a literal ":-1" suffix that addresses nothing.
func TestScheduleChannelAddressComposesTheDeviceRoot(t *testing.T) {
	t.Parallel()

	if got := scheduleChannelAddress("0001ABCD", -1); got != "0001ABCD" {
		t.Errorf("device root = %q, want %q", got, "0001ABCD")
	}
	if got := scheduleChannelAddress("0001ABCD", 2); got != "0001ABCD:2" {
		t.Errorf("channel 2 = %q, want %q", got, "0001ABCD:2")
	}
}
