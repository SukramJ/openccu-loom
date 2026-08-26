// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package weekprofile wraps a device's weekly schedule. Concrete
// profile types compose a domain-specific schedule model from
// [internal/model/schedule] with a [ProfileDataPoint] that reads and
// writes the CCU paramset.
//
// The 0.1.0 scope is the observable surface: Current/Load/Save wiring
// and change subscriptions. The paramset packing (raw CCU group
// numbers, ENABLE_SCHEDULE on/off) is ported incrementally together
// with the climate domain work.
package weekprofile
