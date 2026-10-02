// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package harness runs a complete openccu-loom daemon as a child
// process for end-to-end black-box tests under tests/e2e/.
//
// Start writes a config.yaml, then executes the built daemon binary
// (located by locateDaemonBinary, so `make build` must have run) with
// `run --config`. Everything the daemon talks to is substituted with a
// hermetic in-process equivalent:
//
//   - South-bound CCU: godevccu (imported directly from
//     github.com/SukramJ/godevccu)
//   - South-bound openccu-lite box: litefake (BackendOpenCCULite; see
//     lite.go), selected through Options.Backend
//   - MQTT broker: an embedded pure-Go broker
//   - OIDC OP: a mock provider that signs RS256 tokens in memory
//   - Persistence: SQLite in t.TempDir()
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
