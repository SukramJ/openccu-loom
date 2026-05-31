// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package rpcserver hosts the two callback listeners a central runs:
// an HTTP XML-RPC endpoint (port 8120 by default, path
// /RPC2/<central_name>) and a raw-TCP BIN-RPC endpoint (port 8129 by
// default). Both listeners accept registrations from multiple
// centrals and route incoming requests to the owning [Handler] by
// interface_id or central_name.
package rpcserver
