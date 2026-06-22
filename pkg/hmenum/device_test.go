// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmenum

import "testing"

func TestDeriveDeviceUpdateStatus(t *testing.T) {
	cases := []struct {
		name            string
		state           DeviceFirmwareState
		updateAvailable bool
		want            DeviceUpdateStatus
	}{
		{
			name:  "PerformingUpdate yields installing",
			state: DeviceFirmwareStatePerformingUpdate,
			want:  DeviceUpdateStatusInstalling,
		},
		{
			name:  "DoUpdatePending yields installing",
			state: DeviceFirmwareStateDoUpdatePending,
			want:  DeviceUpdateStatusInstalling,
		},
		{
			name:            "UpToDate with updateAvailable=true yields update_available",
			state:           DeviceFirmwareStateUpToDate,
			updateAvailable: true,
			want:            DeviceUpdateStatusUpdateAvailable,
		},
		{
			name:  "ReadyForUpdate without updateAvailable flag yields update_available",
			state: DeviceFirmwareStateReadyForUpdate,
			want:  DeviceUpdateStatusUpdateAvailable,
		},
		{
			name:  "UpToDate without updateAvailable flag yields up_to_date",
			state: DeviceFirmwareStateUpToDate,
			want:  DeviceUpdateStatusUpToDate,
		},
		{
			// NewFirmwareAvailable is NOT in IsFirmwareUpdateReady's set
			// (only ReadyForUpdate / DoUpdatePending / PerformingUpdate are),
			// so without the updateAvailable signal it is up_to_date.
			name:  "NewFirmwareAvailable without updateAvailable flag yields up_to_date",
			state: DeviceFirmwareStateNewFirmwareAvailable,
			want:  DeviceUpdateStatusUpToDate,
		},
	}
	for _, tc := range cases {
		got := DeriveDeviceUpdateStatus(tc.state, tc.updateAvailable)
		if got != tc.want {
			t.Errorf("%s: DeriveDeviceUpdateStatus(%s, %v) = %q, want %q",
				tc.name, tc.state, tc.updateAvailable, got, tc.want)
		}
	}
}

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
