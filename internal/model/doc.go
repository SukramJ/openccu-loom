// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package model hosts the daemon's domain model: devices, channels,
// data points, custom device profiles, and the mixins they share.
//
// The model is deliberately thin — it provides the types and helpers
// that the northbound adapters consume, but no I/O: that stays in
// [internal/client] / [internal/central].
package model
