// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package generic

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// TestW2GenEventDataDeviceAddressUsesTheCanonicalRule pins the channel →
// device address grammar in this package to its one owner,
// [hmtypes.DeviceAddress].
//
// The rule is not "strip everything after some colon": the owner cuts at the
// FIRST separator and documents the contract ("ABC:1" → "ABC"). A private
// copy that searches backwards answers differently on an address carrying more
// than one separator, and nothing downstream can tell which rule produced the
// value it received.
//
// The repository's dedicated guard for this rule,
// tests/contract/address_rule_single_source_test.go, cannot see a copy that
// lives inside a method or inside a helper not named for the address, which is
// exactly the shape this one had. Hence a guard here, over the observable.
func TestW2GenEventDataDeviceAddressUsesTheCanonicalRule(t *testing.T) {
	t.Parallel()

	// The multi-separator case is the only one that separates the two rules;
	// the single-separator cases are here so a regression that breaks the
	// ordinary address is caught by the same test.
	for _, channelAddress := range []string{
		"VCU1530633:1",
		"00021BE9957782:4",
		"NoSeparator",
		"LINK:PEER:3",
	} {
		dp := NewDataPoint[float64](Spec{
			Key: hmtypes.DataPointKey{
				InterfaceID:    "HmIP-RF",
				ChannelAddress: channelAddress,
				ParamsetKey:    hmenum.ParamsetKeyValues,
				Parameter:      string(hmenum.ParameterLevel),
			},
			CentralName: "ccu-a",
			Descriptor:  hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead},
		})

		want := hmtypes.DeviceAddress(channelAddress)
		if got := dp.GetEventDataFor(nil).DeviceAddress; got != want {
			t.Errorf("GetEventDataFor(%q).DeviceAddress = %q, want %q — the device part of a "+
				"channel address is hmtypes.DeviceAddress's answer, not a rule restated here",
				channelAddress, got, want)
		}
	}
}
