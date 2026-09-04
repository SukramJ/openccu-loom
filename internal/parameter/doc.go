// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package parameter contains the parameter-tools used across the data
// plane: validation against a [hmproto.ParameterData] description,
// coercion of loosely-typed wire values into [hmtypes.ParamValue], and
// [Diff], a comparison helper that tolerates what a CCU transport does to
// a float in transit. [Diff] is offered to callers; the optimistic-update
// reconciliation a running daemon performs is decided in
// internal/model/generic and does not go through it.
//
// The package has no network dependencies. It operates purely on
// values the transport layer has already decoded.
package parameter
