// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package e2e hosts black-box, end-to-end tests that exercise every
// externally offered surface of the daemon — REST, WebSocket, MQTT,
// the SPA + HTMX UI, /metrics, hmcli — through its production wire,
// against a fully assembled openccu-loom daemon brought up in-process
// by the harness under [github.com/SukramJ/openccu-loom/tests/e2e/harness].
//
// The suite is hermetic: godevccu replaces the CCU, an embedded
// pure-Go MQTT broker replaces Mosquitto, and a small mock OP
// replaces the OIDC provider. No Docker, no real network, no real
// CCU.
//
// Every test file in this package must set the `e2e` build
// constraint so that `make test` skips them; run them explicitly
// with `make e2e`. See docs/e2e-testplan.md for the full plan.
package e2e
