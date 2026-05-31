// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package contract hosts contract tests — assertions that pin down
// behaviour we explicitly refuse to change. Each test states a hard
// rule and fails loudly when the rule is violated.
//
// Contract tests run as part of `make test` (they have no build tag).
// The catalogue mirrors SPECIFICATION §23.1.
package contract
