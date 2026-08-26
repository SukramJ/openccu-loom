// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package binrpc is the southbound BIN-RPC transport used to talk to
// CUxD. BIN-RPC is semantically identical to XML-RPC — same value
// model, same method dispatch — but framed as a compact binary
// envelope over raw TCP.
//
// The [Value] types from package xmlrpc are reused unchanged; this
// package only contributes the wire codec, the TCP client, and the
// TCP listener that hosts our BIN-RPC callback server (the one CUxD
// pushes events into via xmlrpc_bin://… URLs).
package binrpc
