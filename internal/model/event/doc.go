// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package event models the transient "fire-and-forget" side of a
// Homematic device — button presses, impulse markers, and device
// error signals. These are not value-observable data points; every
// CCU emission produces one [Event].
//
// Three kinds are defined (matching the Python reference):
//   - [KindKeypress] — PRESS*, PRESS_LONG*, PRESS_SHORT, PRESS_CONT,
//     PRESS_LOCK, PRESS_UNLOCK.
//   - [KindImpulse] — SEQUENCE_OK.
//   - [KindDeviceError] — ERROR, SENSOR_ERROR (value-gated: only
//     transitions to "active" fire an event).
package event
