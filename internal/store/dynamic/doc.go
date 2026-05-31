// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package dynamic holds the in-memory caches that sit between the
// CCU wire and the domain layer. These caches are not persisted —
// they are rebuilt on daemon restart from the authoritative stores.
//
// Three cache flavours live here:
//
//   - [DataCache]: last-observed VALUES paramset per (device, channel)
//   - [CommandCache]: last-sent command tracker (used to suppress
//     spurious echo events from the CCU)
//   - [PingPongJournal]: rolling window of ping/pong events for the
//     health tracker
package dynamic
