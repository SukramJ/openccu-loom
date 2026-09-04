// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hmenum_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestFirmwareUpdateReadyMatchesTheCCUsInstallPrecondition pins the set
// against the condition the CCU itself applies.
//
// The CCU's own precondition is
//
//	installable = updateState == READY_FOR_UPDATE
//	           || liveServerUpdateState == NEW_FIRMWARE_AVAILABLE
//	           || liveServerUpdateState == DELIVER_FIRMWARE_IMAGE
//
// and the live-server states reach the legacy XML-RPC wire with a "LIVE_"
// prefix. Its device-firmware overview renders an Update button for
// LIVE_NEW_FIRMWARE_AVAILABLE. We omitted both live states, so an access point
// with an installable update reported its current version as the latest one
// and the update stayed hidden.
//
// DO_UPDATE_PENDING and PERFORMING_UPDATE are not installable — an install is
// already running — which is what IsFirmwareUpdateInProgress answers.
func TestFirmwareUpdateReadyMatchesTheCCUsInstallPrecondition(t *testing.T) {
	t.Parallel()

	installable := map[hmenum.DeviceFirmwareState]bool{
		hmenum.DeviceFirmwareStateReadyForUpdate:           true,
		hmenum.DeviceFirmwareStateLiveNewFirmwareAvailable: true,
		hmenum.DeviceFirmwareStateLiveDeliverFirmwareImage: true,

		hmenum.DeviceFirmwareStateDoUpdatePending:              false,
		hmenum.DeviceFirmwareStatePerformingUpdate:             false,
		hmenum.DeviceFirmwareStateNewFirmwareAvailable:         false,
		hmenum.DeviceFirmwareStateDeliverFirmwareImage:         false,
		hmenum.DeviceFirmwareStateUpToDate:                     false,
		hmenum.DeviceFirmwareStateLiveUpToDate:                 false,
		hmenum.DeviceFirmwareStateUnknown:                      false,
		hmenum.DeviceFirmwareStateBackgroundUpdateNotSupported: false,
	}
	for state, want := range installable {
		if got := state.IsFirmwareUpdateReady(); got != want {
			t.Errorf("%s: IsFirmwareUpdateReady = %v, want %v", state, got, want)
		}
	}

	// The in-flight states keep their own predicate.
	for _, s := range []hmenum.DeviceFirmwareState{
		hmenum.DeviceFirmwareStateDoUpdatePending,
		hmenum.DeviceFirmwareStatePerformingUpdate,
	} {
		if !s.IsFirmwareUpdateInProgress() {
			t.Errorf("%s must still report as in progress", s)
		}
	}
}
