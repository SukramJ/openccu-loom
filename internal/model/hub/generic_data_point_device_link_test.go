// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hub

import "testing"

// ─── HubDataPoint.DeviceAddress ─────────────────────────────────────────

// TestHubDataPointDeviceAddressSplitsChannelAddress verifies that
// DeviceAddress returns the part of a set channel address before the ':'.
func TestHubDataPointDeviceAddressSplitsChannelAddress(t *testing.T) {
	t.Parallel()

	hdp := NewHubDataPoint("ccu-01", "name", "", true)
	hdp.SetChannel("0001ABCD:3")

	if got := hdp.Channel(); got != "0001ABCD:3" {
		t.Fatalf("Channel() = %q, want %q", got, "0001ABCD:3")
	}
	if got := hdp.DeviceAddress(); got != "0001ABCD" {
		t.Fatalf("DeviceAddress() = %q, want %q", got, "0001ABCD")
	}
}

// TestHubDataPointDeviceAddressEmptyWhenNoChannel verifies that both
// Channel() and DeviceAddress() are empty before any assignment, and stay
// empty after explicitly clearing with SetChannel("").
func TestHubDataPointDeviceAddressEmptyWhenNoChannel(t *testing.T) {
	t.Parallel()

	hdp := NewHubDataPoint("ccu-01", "name", "", true)
	if got := hdp.Channel(); got != "" {
		t.Fatalf("fresh Channel() = %q, want empty", got)
	}
	if got := hdp.DeviceAddress(); got != "" {
		t.Fatalf("fresh DeviceAddress() = %q, want empty", got)
	}

	hdp.SetChannel("0001ABCD:1")
	hdp.SetChannel("") // explicit clear
	if got := hdp.Channel(); got != "" {
		t.Fatalf("Channel() after clear = %q, want empty", got)
	}
	if got := hdp.DeviceAddress(); got != "" {
		t.Fatalf("DeviceAddress() after clear = %q, want empty", got)
	}
}

// TestHubDataPointDeviceAddressWithoutColon verifies that a channel address
// without a ':' (a device-level address) is returned unchanged.
func TestHubDataPointDeviceAddressWithoutColon(t *testing.T) {
	t.Parallel()

	hdp := NewHubDataPoint("ccu-01", "name", "", true)
	hdp.SetChannel("ABC")

	if got := hdp.DeviceAddress(); got != "ABC" {
		t.Fatalf("DeviceAddress() = %q, want %q", got, "ABC")
	}
}
