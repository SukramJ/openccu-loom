// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package metrics

import "math"

func bitsFromFloat(f float64) uint64 { return math.Float64bits(f) }
func floatFromBits(b uint64) float64 { return math.Float64frombits(b) }
