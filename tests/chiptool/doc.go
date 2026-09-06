// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

//go:build chiptool

// Package chiptool drives the CSA reference commissioner
// (`chip-tool`) against a running openccu-loom Matter bridge to
// validate end-to-end Matter behaviour.
//
// Build tag `chiptool` keeps this suite out of `make test` and
// `make e2e`. Run with `make chiptool-test`, which builds
// `./bin/openccu-loom` first and then `go test -tags=chiptool
// ./tests/chiptool/...`.
//
// Every test calls [harness.RequireChipTool] which `t.Skip`s when
// `chip-tool` is not on PATH; the suite stays harmless on machines
// without the snap installed.
//
// Live-CCU policy: this suite never touches the developer's CCU.
// All southbound traffic is served by an in-process godevccu
// simulator — writes (T6-style OnOff cycles) hit the simulator's
// virtual devices and are explicitly the "hermetic test paths"
// CLAUDE.md §3 carves out as the parallel path that does not need
// per-device user approval.
//
// Negative-write parity (planned extension). The suite today validates
// the happy path (a write/invoke the bridge ACCEPTS round-trips through a
// real reference controller). The complementary validation method —
// asserting that a write matter.js REJECTS is also rejected by the bridge
// at the reference-controller level — is covered in-process by the Go
// behaviour-parity suite (the constraint-rejection table under
// go-fabric's cluster package) but is NOT yet exercised through
// chip-tool. When extending this suite, add negative cases that Invoke a
// constraint-violating command and assert chip-tool reports the matching
// IM status rather than Status: 0x0 (success):
//   - windowcovering go-to-lift-percentage with liftPercent100ths > 10000
//     → CONSTRAINT_ERROR (0x87)
//   - thermostat setpoint-raise-lower mode=Heat against a cooling-only
//     endpoint → INVALID_COMMAND (0x85)
//   - thermostat write OccupiedHeatingSetpoint past MaxHeatSetpointLimit
//     → CONSTRAINT_ERROR (0x87)
//
// These were deliberately left as a documented gated plan rather than
// shipped as compile-only tests: chip-tool's command/argument spelling is
// version-sensitive and cannot be validated on a machine without the
// binary, so the cases are authored when chip-tool is available to run
// them. The in-process table is the authoritative guard until then.
package chiptool
