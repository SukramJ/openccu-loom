// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package jsonrpc is the HTTP JSON-RPC client for the CCU (and, post-0.1.0,
// CCU-Jack).
//
// The CCU's JSON-RPC uses a flat request/response envelope with a single
// session ID, which keeps the implementation stdlib-only.
//
// The client is the south-bound transport. Session management, retry on
// 401/403, and error mapping to pkg/hmerr live here; circuit breaking,
// throttling, and coalescing belong one layer up in internal/client.
package jsonrpc
