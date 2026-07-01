// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// fakeEventServer stands up an httptest.Server that performs a WS upgrade,
// reads the subscribe frame, sends the provided frames, and then closes the
// connection. It returns the server and a channel that receives the decoded
// subscribe message once the handshake is complete.
func fakeEventServer(t *testing.T, frames [][]byte) (*httptest.Server, <-chan eventsSubscribeMsg) { //nolint:gocritic // named returns not necessary here; gocritic preference doesn't apply to test helpers
	t.Helper()
	ch := make(chan eventsSubscribeMsg, 1)
	upgrader := websocket.Upgrader{
		CheckOrigin: func(_ *http.Request) bool { return true },
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()

		// Read the subscribe frame.
		_, data, err := conn.ReadMessage()
		if err != nil {
			t.Errorf("read subscribe: %v", err)
			return
		}
		var sub eventsSubscribeMsg
		if err := json.Unmarshal(data, &sub); err != nil {
			t.Errorf("decode subscribe: %v", err)
			return
		}
		ch <- sub

		// Emit the provided frames then close.
		for _, f := range frames {
			if err := conn.WriteMessage(websocket.TextMessage, f); err != nil {
				return
			}
		}
		// Graceful close: send the close frame, then drain the client's echoed
		// close (bounded by a read deadline) before tearing down the TCP socket.
		// Returning immediately lets the socket teardown race ahead of the
		// client reading the close frame, which some platforms surface as an
		// abrupt reset rather than a clean WS close.
		_ = conn.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, "done"),
			time.Now().Add(time.Second))
		_ = conn.SetReadDeadline(time.Now().Add(time.Second))
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				break
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv, ch
}

// httpToWS converts the httptest.Server URL (http://…) to ws://… for use by
// the events tail flags.
func httpToWS(u string) string {
	return "ws" + strings.TrimPrefix(u, "http")
}

// TestEventsTailSubscribeTopics verifies that the subscribe frame sent to the
// server contains exactly the topics passed via --topics, and that the command
// exits cleanly when the server closes the connection.
func TestEventsTailSubscribeTopics(t *testing.T) {
	t.Parallel()
	frames := [][]byte{
		mustMarshal(t, map[string]any{
			"topic": "device.0001ABC",
			"type":  "DataPointValueChanged",
			"ts":    "2026-07-01T10:00:00.000Z",
			"payload": map[string]any{
				"value": true,
			},
		}),
	}
	srv, subCh := fakeEventServer(t, frames)

	// Run with http:// base URL — the command must swap to ws://.
	var stdout, stderr bytes.Buffer
	args := []string{"tail", "--host", srv.URL, "--topics", "device.*,hub.*"}
	if err := cmdEvents(args, &stdout, &stderr); err != nil {
		t.Fatalf("cmdEvents returned error: %v", err)
	}

	select {
	case sub := <-subCh:
		if sub.Op != "subscribe" {
			t.Errorf("op = %q, want subscribe", sub.Op)
		}
		want := []string{"device.*", "hub.*"}
		if len(sub.Topics) != len(want) {
			t.Errorf("topics = %v, want %v", sub.Topics, want)
		} else {
			for i, w := range want {
				if sub.Topics[i] != w {
					t.Errorf("topics[%d] = %q, want %q", i, sub.Topics[i], w)
				}
			}
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for subscribe frame")
	}
}

// TestEventsTailHumanOutput verifies that in the default (human) mode the
// command prints the event type and topic on stdout and responds to an
// application-level {"op":"ping"} frame with a pong (the server does not
// close on missing pong, so we just confirm no error and that the event line
// appears in the output).
func TestEventsTailHumanOutput(t *testing.T) {
	t.Parallel()
	frames := [][]byte{
		// App-level ping — the client should pong and continue.
		mustMarshal(t, map[string]any{"op": "ping"}),
		// A real broadcast event.
		mustMarshal(t, map[string]any{
			"topic":   "device.ABCDEF",
			"type":    "DataPointValueChanged",
			"ts":      "2026-07-01T12:34:56.000Z",
			"payload": map[string]any{"value": 42},
		}),
	}
	srv, _ := fakeEventServer(t, frames)

	var stdout, stderr bytes.Buffer
	args := []string{"tail", "--host", srv.URL, "--topics", "*"}
	if err := cmdEvents(args, &stdout, &stderr); err != nil {
		t.Fatalf("cmdEvents returned error: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "DataPointValueChanged") {
		t.Errorf("human output missing event type; got:\n%s", out)
	}
	if !strings.Contains(out, "device.ABCDEF") {
		t.Errorf("human output missing topic; got:\n%s", out)
	}
}

// TestEventsTailJSONOutput verifies that with --json the command emits each
// event as a compact JSON line (JSONL) and that the topic and type fields are
// present.
func TestEventsTailJSONOutput(t *testing.T) {
	t.Parallel()
	frames := [][]byte{
		mustMarshal(t, map[string]any{
			"topic":   "hub.status",
			"type":    "HubStateChanged",
			"ts":      "2026-07-01T08:00:00.000Z",
			"payload": map[string]any{"state": "running"},
		}),
	}
	srv, _ := fakeEventServer(t, frames)

	var stdout, stderr bytes.Buffer
	args := []string{"tail", "--host", srv.URL, "--topics", "hub.*", "--json"}
	if err := cmdEvents(args, &stdout, &stderr); err != nil {
		t.Fatalf("cmdEvents returned error: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	// Expect exactly one non-empty line (the one event).
	if len(lines) != 1 {
		t.Fatalf("expected 1 JSON line, got %d:\n%s", len(lines), stdout.String())
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatalf("line is not valid JSON: %v\nline: %s", err, lines[0])
	}
	if got["topic"] != "hub.status" {
		t.Errorf("topic = %v, want hub.status", got["topic"])
	}
	if got["type"] != "HubStateChanged" {
		t.Errorf("type = %v, want HubStateChanged", got["type"])
	}
}

// TestEventsTailClassify verifies that --classify sets the classify field in
// the subscribe frame.
func TestEventsTailClassify(t *testing.T) {
	t.Parallel()
	srv, subCh := fakeEventServer(t, nil)

	var stdout, stderr bytes.Buffer
	args := []string{"tail", "--host", srv.URL, "--classify"}
	if err := cmdEvents(args, &stdout, &stderr); err != nil {
		t.Fatalf("cmdEvents returned error: %v", err)
	}

	select {
	case sub := <-subCh:
		if !sub.Classify {
			t.Errorf("classify = false, want true")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for subscribe frame")
	}
}

// TestEventsTailDefaultTopicStar verifies that omitting --topics sends ["*"].
func TestEventsTailDefaultTopicStar(t *testing.T) {
	t.Parallel()
	srv, subCh := fakeEventServer(t, nil)

	var stdout, stderr bytes.Buffer
	args := []string{"tail", "--host", srv.URL}
	if err := cmdEvents(args, &stdout, &stderr); err != nil {
		t.Fatalf("cmdEvents returned error: %v", err)
	}

	select {
	case sub := <-subCh:
		if len(sub.Topics) != 1 || sub.Topics[0] != "*" {
			t.Errorf("default topics = %v, want [*]", sub.Topics)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for subscribe frame")
	}
}

// TestWsURL verifies the scheme-swap helper for various inputs.
func TestWsURL(t *testing.T) {
	t.Parallel()
	cases := []struct {
		base, path, want string
		wantErr          bool
	}{
		{"http://localhost:8119", "/api/v1/events", "ws://localhost:8119/api/v1/events", false},
		{"https://example.com", "/api/v1/events", "wss://example.com/api/v1/events", false},
		{"ftp://bad", "/path", "", true},
	}
	for _, tc := range cases {
		got, err := wsURL(tc.base, tc.path)
		if tc.wantErr {
			if err == nil {
				t.Errorf("wsURL(%q, %q): expected error, got %q", tc.base, tc.path, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("wsURL(%q, %q): unexpected error: %v", tc.base, tc.path, err)
			continue
		}
		if got != tc.want {
			t.Errorf("wsURL(%q, %q) = %q, want %q", tc.base, tc.path, got, tc.want)
		}
	}
}

// TestSplitTopics verifies the comma-split helper.
func TestSplitTopics(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want []string
	}{
		{"*", []string{"*"}},
		{"device.*,hub.*", []string{"device.*", "hub.*"}},
		{"  device.* , hub.* ", []string{"device.*", "hub.*"}},
		{"", []string{"*"}},
		{",,,", []string{"*"}},
	}
	for _, tc := range cases {
		got := splitTopics(tc.in)
		if len(got) != len(tc.want) {
			t.Errorf("splitTopics(%q) = %v, want %v", tc.in, got, tc.want)
			continue
		}
		for i, w := range tc.want {
			if got[i] != w {
				t.Errorf("splitTopics(%q)[%d] = %q, want %q", tc.in, i, got[i], w)
			}
		}
	}
}

// mustMarshal is a test helper that marshals v to JSON or fatals.
func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("mustMarshal: %v", err)
	}
	return b
}

// Compile-time check that httpToWS is used (used in narrative but not in
// current tests since we pass srv.URL directly and rely on the http→ws swap
// inside cmdEvents). Keep the helper to avoid a lint "declared and not used"
// error by referencing it in a blank assignment.
var _ = httpToWS
