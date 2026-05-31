// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package calculated hosts data points the daemon derives from one or
// more wire-level parameters: climate-comfort sensors (dew point,
// apparent temperature, …), the battery-level percentage, and
// registry-driven derived binary sensors (window-open, smoke-alarm,
// intrusion-alarm).
//
// Two categories exist:
//
//  1. Pure formulas — stateless functions in this package that
//     translate inputs into outputs. Formulas return an (ok, value)
//     pair so callers can distinguish "unable to compute" from a
//     real zero.
//  2. Data-point wrappers — per-formula structs that cache the latest
//     source values, run the formula on every ingestion, and fire
//     subscribers when the derived value changes.
package calculated
