// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package patches applies paramset overrides that correct CCU bugs.
//
// The MVP ships the patches ported
//   - `HM-ES-PMSw1-Pl` energy-counter unit fixes
//   - `HmIP-RGBW` saturation/hue bounds
//   - generic "operations bit" corrections where the CCU omits WRITE
//
// The registry is extensible: domain code registers patches at
// startup, and the paramset normaliser applies matching entries when
// it canonicalises a descriptor.
package patches
