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
//
// # Decoding ReGa string fields takes two steps, not one
//
// Every string field a ReGa script returns through this package is
// percent-encoded Latin-1, not percent-encoded UTF-8. url.QueryUnescape
// alone is therefore only the first half of the rule: "Sp%FCle" unescapes
// to the byte sequence "Sp\xFC" + "le", which is not valid UTF-8, and
// json.Marshal replaces the stray byte with U+FFFD on the way out of every
// north-bound plane. That loss is irreversible once such a value has been
// seeded into the model, so the transcode is the half that must not be
// skipped.
//
// The canonical decoder is decodeRegaField in internal/central/adapter:
// url.QueryUnescape, then a Latin-1 transcode when the result is not valid
// UTF-8 (an already-UTF-8 value passes through untouched). It is unexported,
// so a consumer in another package cannot delegate to it and has to perform
// both steps itself — doing only the unescape is the failure this note
// exists to prevent. Every doc comment in this package that names
// url.QueryUnescape says so; TestHmCliRegaDocsCarryTheLatin1Half enforces it.
package rega
