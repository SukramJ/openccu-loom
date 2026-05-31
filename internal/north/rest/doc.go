// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package rest is the REST + WebSocket north-bound adapter.
//
// Routing is built on [chi.Router]; request/response bodies use
// JSON; errors follow RFC 9457 problem+json. Every handler resolves
// authentication via the [internal/auth] middleware and touches the
// domain only through the small facade interfaces the handler
// packages declare.
package rest
