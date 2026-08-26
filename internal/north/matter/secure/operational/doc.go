// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package operational wires the CASE handshake (Sigma) into the
// secure-channel session layer. It is the bridge-side glue that turns
// the Sigma key-derivation output into a usable
// [channel.Session] keyed by Matter session-id.
//
// Responsibilities:
//
//   - Allocate session-ids for incoming CASE handshakes.
//   - Construct [channel.Session] from Sigma-derived I2RKey/R2IKey
//     keypairs.
//   - Maintain a session-id → *channel.Session lookup the message
//     dispatcher consults on receive.
//   - Persist resumption-ids to [store.Store] so a returning peer
//     can resume via Sigma1 with a known token (Matter §4.13.2.4).
//
// Sessions themselves live in RAM only — Matter convention is that
// CASE sessions are volatile. After a daemon restart, peers
// re-handshake (potentially via resumption to skip the full Sigma
// dance).
package operational
