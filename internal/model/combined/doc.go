// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package combined hosts data points that aggregate two or more
// wire-level parameters into a single logical value.
//
// Unlike calculated data points (which derive a new value from others),
// combined data points stay bidirectional: a read returns the joined
// state, a write splits the payload back into the underlying
// parameters. The three flavours shipped in 0.1.0 mirror
//
//  1. [HSColor]     — HUE + SATURATION pairs for colour-controlled
//     lights.
//  2. [Timer]       — value + unit pairs with automatic S/M/H
//     conversion.
//  3. [LevelCombined] — Level + Slats-Level byte pair for blinds.
package combined
