// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package wiring_pins contains AST-based pin tests that assert the wiring
// of critical production callers.  Each test fails when a method, constructor,
// or struct-literal field is no longer referenced from the expected file,
// turning silent refactoring regressions into immediate test failures.
//
// Pin tests cover the wiring only — not the behaviour.  Behavioural
// correctness is covered by unit and integration tests.
//
// When you add a new exported method, builder, or subscriber that is wired
// through an indirect path (factory, closure, reflection), add a pin test
// here.  See CONTRIBUTING.md §Pin-Tests für neue Wiring.
package wiring_pins
