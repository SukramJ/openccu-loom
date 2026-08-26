// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package parameter contains the parameter-tools used across the data
// plane: validation against a [hmproto.ParameterData] description,
// coercion of loosely-typed wire values into [hmtypes.ParamValue], and
// a diff helper for optimistic-update reconciliation.
//
// The package has no network dependencies. It operates purely on
// values the transport layer has already decoded.
package parameter
