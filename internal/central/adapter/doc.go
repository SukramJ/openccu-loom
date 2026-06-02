// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package adapter wires the central domain into the north-bound
// handler interfaces. Every adapter is a thin pointer-wrap around a
// [*central.Unit] (or the multi-CCU [*central.Registry]) so
// the REST/UI packages can consume the domain without importing
// anything transient.
package adapter
