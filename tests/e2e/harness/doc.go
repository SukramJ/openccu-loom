// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package harness assembles a complete openccu-loom daemon in-process
// for end-to-end black-box tests under tests/e2e/.
//
// The harness mirrors the wiring from cmd/openccu-loom/daemon.go but
// substitutes external dependencies with hermetic in-process
// equivalents:
//
//   - South-bound CCU: godevccu (re-exported from tests/integration)
//   - MQTT broker: an embedded pure-Go broker
//   - OIDC OP: a mock provider that signs RS256 tokens in memory
//   - Persistence: SQLite in t.TempDir()
//   - Clock: the test clock from internal/clock
//
// Every listener is bound to an OS-assigned ephemeral port; tests
// read the effective port through the accessor methods on Harness.
//
// The harness is single-shot: each test must call Start to obtain a
// fresh daemon. Reuse across tests is intentionally not supported,
// because it leaks state through SQLite, the audit log, and the
// MQTT broker's retained-message store.
//
// See notes/testplans/e2e-testplan.md §4.1 for the design and §6 for the file
// layout this package supports.
package harness
