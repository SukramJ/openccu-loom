// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package hmproto holds the on-the-wire shapes of CCU XML-RPC /
// JSON-RPC payloads — device descriptions, parameter descriptions,
// paramsets — plus helpers to normalise them into canonical form.
//
// Types here mirror the CCU schema exactly. Normalisation code lives
// alongside the types so that the wire shape and the deterministic
// representation used for hash-based change detection are never out of
// sync.
package hmproto
