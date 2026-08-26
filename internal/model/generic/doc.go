// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package generic implements Level 4 of the data-point hierarchy from
// SPECIFICATION §6.1: one concrete Go type per functional parameter
// kind (switch, number, select, sensor, …).
//
// Every concrete type embeds a generic core [DataPoint[T]] that holds
// the current value, observed flag, optimistic timestamp, and the
// [Writer] used to emit outbound commands. Custom-device classes in
// internal/model/custom compose these generics into richer devices
// (climate, cover, lock, …).
//
// The package is deliberately transport-agnostic: it knows nothing
// about XML-RPC, BIN-RPC, or the coordinator layer. The [Writer]
// interface captures the single hook needed for sending; the
// [DataPoint[T].OnEvent] entry point captures the single hook needed
// for inbound wire updates.
package generic
