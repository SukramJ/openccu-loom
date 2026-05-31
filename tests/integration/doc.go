// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package integration hosts end-to-end tests that exercise the daemon
// against a godevccu-based mock CCU started in-process. godevccu is a
// Pure-Go port — no Python toolchain or sub-process is
// required to run the suite.
//
// Every test file in this package must set the `integration` build
// constraint so that `make test` skips them; run them explicitly with
// `make integration`.
package integration
