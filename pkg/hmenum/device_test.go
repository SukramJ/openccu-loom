// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmenum

import "testing"

func TestDeviceFirmwareStatePredicates(t *testing.T) {
	inProgress := []DeviceFirmwareState{
		DeviceFirmwareStateDoUpdatePending,
		DeviceFirmwareStatePerformingUpdate,
	}
	for _, s := range inProgress {
		if !s.IsFirmwareUpdateInProgress() {
			t.Errorf("%s should be InProgress", s)
		}
		if !s.IsFirmwareUpdateReady() {
			t.Errorf("%s should be Ready", s)
		}
	}
	ready := []DeviceFirmwareState{
		DeviceFirmwareStateReadyForUpdate,
	}
	for _, s := range ready {
		if s.IsFirmwareUpdateInProgress() {
			t.Errorf("%s should NOT be InProgress", s)
		}
		if !s.IsFirmwareUpdateReady() {
			t.Errorf("%s should be Ready", s)
		}
	}
	notReady := []DeviceFirmwareState{
		DeviceFirmwareStateUpToDate,
		DeviceFirmwareStateUnknown,
		DeviceFirmwareStateNewFirmwareAvailable,
	}
	for _, s := range notReady {
		if s.IsFirmwareUpdateInProgress() {
			t.Errorf("%s should NOT be InProgress", s)
		}
		if s.IsFirmwareUpdateReady() {
			t.Errorf("%s should NOT be Ready", s)
		}
	}
}
