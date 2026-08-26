// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package interfaces holds the cross-cutting DI contracts that are
// consumed by more than one bounded context.
//
// Standard Go convention places an interface in its consumer package.
// The types in this package are the deliberate exceptions: every one
// of them is implemented by at least one transport or backend and
// consumed by at least two of {central, client, north-bound adapters}.
// Anything usable by a single consumer stays in that consumer.
//
// See SPECIFICATION.md §3.2 and CLAUDE.md §Critical Rules for the rule.
package interfaces
