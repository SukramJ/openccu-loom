// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package ws

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"
)

// TestDispatchMessageAnswersMalformedFrameWithError asserts that a text
// frame which does not decode as inboundMessage JSON (e.g. `topics` sent
// as a string instead of an array) gets an `{op:"error"}` reply instead of
// being dropped silently. A client waiting on the documented `subscribed`
// ack for that request would otherwise hang forever with no signal that
// its frame was never processed.
//
// Reads the raw frame via readServerText rather than the [wsConn.recv]
// helper: recv (and readEvent) deliberately skip `op:"error"` — like
// ping, it is an out-of-band notification uncorrelated to any pending
// `call` — so this test needs the unfiltered frame reader to observe it.
func TestDispatchMessageAnswersMalformedFrameWithError(t *testing.T) {
	hub := NewHub()
	server := httptest.NewServer(Handler(hub, nil, nil))
	defer server.Close()

	conn := dialWS(t, server)
	conn.send(map[string]any{"op": "subscribe", "topics": "notalist"})

	raw := readServerText(t, conn.br)
	var reply outboundOp
	if err := json.Unmarshal(raw, &reply); err != nil {
		t.Fatalf("decode reply: %v, raw=%s", err, string(raw))
	}
	if reply.Op != "error" {
		t.Fatalf("op=%q, want %q", reply.Op, "error")
	}
	if reply.Error == nil || reply.Error.Code != CommandErrorBadRequest {
		t.Fatalf("error=%+v, want code %q", reply.Error, CommandErrorBadRequest)
	}
}

// TestDispatchMessageAnswersUnknownOpWithError asserts that a
// syntactically valid frame carrying an `op` the daemon does not know
// (a typo such as "subscrbe") gets the same `{op:"error"}` reply as a
// malformed frame, rather than being matched by no case in the switch
// and discarded without any response.
func TestDispatchMessageAnswersUnknownOpWithError(t *testing.T) {
	hub := NewHub()
	server := httptest.NewServer(Handler(hub, nil, nil))
	defer server.Close()

	conn := dialWS(t, server)
	conn.send(map[string]any{"op": "subscrbe", "topics": []string{"*"}})

	raw := readServerText(t, conn.br)
	var reply outboundOp
	if err := json.Unmarshal(raw, &reply); err != nil {
		t.Fatalf("decode reply: %v, raw=%s", err, string(raw))
	}
	if reply.Op != "error" {
		t.Fatalf("op=%q, want %q", reply.Op, "error")
	}
	if reply.Error == nil || reply.Error.Code != CommandErrorBadRequest {
		t.Fatalf("error=%+v, want code %q", reply.Error, CommandErrorBadRequest)
	}
}

// TestSubscribeWithEmptyTopicsStillGetsAcked asserts that a `subscribe`
// frame whose `topics` list is missing or empty still receives the
// documented `{op:"subscribed"}` ack, rather than being discarded because
// there is nothing to subscribe to. A client waiting on that ack before
// considering itself connected would otherwise hang forever on a request
// the daemon accepted but never answered.
func TestSubscribeWithEmptyTopicsStillGetsAcked(t *testing.T) {
	hub := NewHub()
	server := httptest.NewServer(Handler(hub, nil, nil))
	defer server.Close()

	conn := dialWS(t, server)
	conn.send(map[string]any{"op": "subscribe"})

	_ = conn.conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	raw := readServerText(t, conn.br)
	var reply outboundOp
	if err := json.Unmarshal(raw, &reply); err != nil {
		t.Fatalf("decode reply: %v, raw=%s", err, string(raw))
	}
	if reply.Op != "subscribed" {
		t.Fatalf("op=%q, want %q", reply.Op, "subscribed")
	}
}
