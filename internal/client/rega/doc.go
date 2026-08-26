// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package rega runs HomeMatic Script (.fn) snippets on the CCU through
// the ReGa.runScript JSON-RPC method.
//
// Scripts are embedded in the binary via go:embed. Parameters are
// substituted into the script body before dispatch via ##NAME##
// placeholders; string values are escaped so they cannot break out of
// the double-quoted string literals they live in.
//
// Script output is the textual stdout of the ReGa execution. Most
// openccu-loom scripts return JSON, but the runner does not require
// that — its only post-processing is an optional control-character
// sanitization step via [SanitizeJSONControls] that makes CCU-emitted
// JSON robust to the stray \r/\n/\t the runtime sometimes injects into
// device names.
//
// The enclosing JSON-RPC transport lives in
// internal/client/transport/jsonrpc.
package rega
