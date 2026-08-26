// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package patches applies paramset overrides that correct CCU bugs.
//
// The built-in patches are model-specific — there is no generic
// operations-bit correction:
//   - `HM-ES-PMSw1-Pl` energy-counter unit fixes
//   - `HmIP-RGBW` saturation/hue bounds and an EVENT-bit correction
//   - `HM-CC-VG-1`
//   - `HmIP-FWI`
//
// The registry is extensible: domain code registers patches at
// startup, and the paramset normaliser applies matching entries when
// it canonicalises a descriptor.
package patches
