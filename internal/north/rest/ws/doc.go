// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package ws implements the `/api/v1/events` WebSocket endpoint plus
// its in-memory hub.
//
// The wire protocol is deliberately lean:
//
//	client → server : {"op":"subscribe",   "topics":["device.*", "hub.*"]}
//	client → server : {"op":"unsubscribe", "topics":["hub.*"]}
//	client → server : {"op":"pong"}
//	server → client : {"topic":"device.0001...", "type":"DataPointValueChanged", "ts":"...", "payload":{...}}
//	server → client : {"op":"ping"}
//
// Topic matching follows
// subscription of "device.*" matches every event whose topic starts
// with "device.". Exact strings and full wildcards ("*") are
// supported.
package ws
