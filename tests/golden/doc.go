// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package golden hosts session-replay tests: recorded CCU
// interactions are fed into the daemon and the emitted events are
// compared against a golden JSON fixture.
//
// Run `go test -update ./tests/golden/...` to refresh the fixtures
// after an intentional output change.
package golden
