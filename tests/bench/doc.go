// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package bench collects performance benchmarks that block release
// regressions. Run weekly in CI; regressions >20% block release.
//
// Every benchmark is tagged `//go:build bench` so `go test` skips
// them. Run explicitly via `make bench` or
// `go test -tags=bench -bench=. -benchmem ./tests/bench/...`.
package bench
