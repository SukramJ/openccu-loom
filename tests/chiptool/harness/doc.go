// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build chiptool

// Package harness brings up a openccu-loom daemon wired for Matter
// against an in-process godevccu simulator and exposes a thin
// chip-tool wrapper that the per-suite tests share.
//
// The supervisor model is single-daemon-per-suite: [TestMain]
// (in the parent package) calls [Start] once, commissions the
// fabric via [Bridge.CommissionShared], and every test reuses the
// resulting commissioned controller. Tests that need an isolated
// fabric (multi-controller, re-pair, failure injection) commission
// their own controller with [Bridge.NewController].
package harness
