// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package schedule provides the human-readable schedule data models
// used by non-climate devices ([Simple]) and climate devices
// ([Climate]). The 0.1.0 scope is the in-memory shape plus value
// validation — the wire-format round-trip (CCU paramset grouping)
// lives in the domain layer.
package schedule
