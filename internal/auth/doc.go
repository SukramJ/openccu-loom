// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package auth implements the authentication surface every north-
// bound adapter shares: identity resolution, credential validation,
// and the two HTTP middlewares the REST router composes (Resolve +
// Require).
//
// MVP schemes:
//   - HTTP Basic, backed by the [UserStore]
//   - Bearer (API Token), backed by the [TokenStore]
//
// Session auth (§18.2 of SPECIFICATION.md) ships alongside the Config UI.
package auth
