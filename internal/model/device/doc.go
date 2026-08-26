// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package device is the Device/Channel runtime model. A [Device]
// aggregates channels, tracks firmware state, and derives an
// [AvailabilityInfo] snapshot from UN_REACH/LOW_BAT/RSSI_DEVICE/
// OPERATING_VOLTAGE_LEVEL observations.
//
// Unlike the Python reference this package does not carry the
// provider-DI zoo. The Go daemon injects wire backends through the
// domain layer; the device model stays small and observable.
package device
