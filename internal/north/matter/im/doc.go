// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package im implements the Matter Interaction Model per Matter Core
// Specification §10.6.
//
// The Interaction Model is the application-layer protocol that runs
// on top of the Secure Channel session ([..]/secure/channel). It
// defines four interactions:
//
//   - Read     — fetch attribute values
//   - Write    — set attribute values
//   - Invoke   — dispatch cluster commands
//   - Subscribe — register for ongoing attribute reports
//
// All messages are TLV-encoded ([..]/tlv) with context-tagged fields
// inside top-level Structures. Path Information Blocks (AttributePathIB,
// CommandPathIB) use the LIST type per spec.
//
// # Architecture
//
// This package owns:
//
//   - Path types (ConcreteAttributePath, ConcreteCommandPath) with
//     wildcard support and TLV codecs.
//   - Status codes (per spec §8.10) and StatusIB.
//   - The four request / response message types.
//   - The [Dispatcher] interface — the surface the cluster-server
//     side fills in so the IM layer stays blind to specific cluster
//     IDs.
//
// This package does NOT own:
//
//   - Cluster-attribute encoding — lives in
//     internal/north/matter/cluster/*.
//   - Subscription state persistence — currently in-memory only.
//   - Timed Request / Timed Action — not yet implemented.
//
// # Wildcard semantics
//
// A path field is "concrete" when its Has* flag is set; otherwise it
// is a wildcard matching every value. The wildcard semantics let a
// commissioner read every attribute on every endpoint of every cluster
// with a single request — the [Dispatcher] expands wildcards on the
// device side.
package im
