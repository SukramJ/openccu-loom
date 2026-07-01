// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
)

// ─── wire types ──────────────────────────────────────────────────────────────

// eventsSubscribeMsg is the JSON frame the client sends to subscribe.
type eventsSubscribeMsg struct {
	Op       string   `json:"op"`
	Topics   []string `json:"topics"`
	Classify bool     `json:"classify,omitempty"`
}

// eventsPongMsg is the JSON frame replying to a server-side {"op":"ping"}.
type eventsPongMsg struct {
	Op string `json:"op"`
}

// eventsInbound is a generic shape for frames arriving from the server.
// Fields not present in a given frame are zero-valued / nil.
type eventsInbound struct {
	// broadcast event fields
	Topic   string          `json:"topic,omitempty"`
	Type    string          `json:"type,omitempty"`
	TS      string          `json:"ts,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
	// control / ack fields
	Op     string   `json:"op,omitempty"`
	Topics []string `json:"topics,omitempty"`
	Seq    int64    `json:"seq,omitempty"`
}

// ─── flags ───────────────────────────────────────────────────────────────────

type eventsFlags struct {
	host     string
	token    string
	user     string
	password string
	// timeout bounds only the initial WebSocket handshake; the stream itself
	// runs until closed or interrupted so that --timeout 0 streams indefinitely.
	timeout  time.Duration
	topics   string
	jsonOut  bool
	classify bool
}

func (f *eventsFlags) bindTo(fs *flag.FlagSet) {
	fs.StringVar(&f.host, "host", "http://localhost:8119", "daemon REST base URL")
	fs.StringVar(&f.token, "token", "", "API bearer token")
	fs.StringVar(&f.user, "user", "", "basic-auth username")
	fs.StringVar(&f.password, "password", "", "basic-auth password")
	fs.DurationVar(&f.timeout, "timeout", 0,
		"handshake timeout only; 0 = no handshake deadline (stream runs until closed or interrupted)")
	fs.StringVar(&f.topics, "topics", "*", "comma-separated topics to subscribe; supports exact strings and prefix wildcards (e.g. device.*)")
	fs.BoolVar(&f.jsonOut, "json", false, "print each event as a compact JSON line (JSONL) instead of human-readable")
	fs.BoolVar(&f.classify, "classify", false, "request inline category/data_point_type on datapoint value-changed events")
}

// authHeader returns the Authorization header value to use for the WS
// handshake, honouring the same Bearer-over-Basic precedence as daemonClient.
// Returns an empty string when no credentials are configured.
func (f *eventsFlags) authHeader() string {
	switch {
	case f.token != "":
		return "Bearer " + f.token
	case f.user != "":
		// RFC 7617 basic-auth encoding: base64(user:password).
		creds := base64.StdEncoding.EncodeToString([]byte(f.user + ":" + f.password))
		return "Basic " + creds
	default:
		return ""
	}
}

// wsURL converts an http(s) base URL to the ws(s) equivalent and appends path.
// The scheme swap is the only difference; host, port, and path carry through.
func wsURL(baseURL, path string) (string, error) {
	switch {
	case strings.HasPrefix(baseURL, "https://"):
		return "wss://" + strings.TrimPrefix(baseURL, "https://") + path, nil
	case strings.HasPrefix(baseURL, "http://"):
		return "ws://" + strings.TrimPrefix(baseURL, "http://") + path, nil
	default:
		return "", fmt.Errorf("events tail: base URL must start with http:// or https://, got %q", baseURL)
	}
}

// ─── command ─────────────────────────────────────────────────────────────────

// cmdEvents dispatches `events <op> [args…]`.
func cmdEvents(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("events: missing operation (try: tail)")
	}
	switch args[0] {
	case "tail":
		return cmdEventsTail(args[1:], stdout, stderr)
	default:
		return fmt.Errorf("events: unknown operation %q", args[0])
	}
}

// cmdEventsTail connects to the daemon's WebSocket event stream and prints
// arriving events until the connection closes or the process is interrupted.
func cmdEventsTail(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("events tail", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var f eventsFlags
	f.bindTo(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}

	target, err := wsURL(strings.TrimRight(f.host, "/"), "/api/v1/events")
	if err != nil {
		return err
	}

	conn, err := dialEventStream(f, target)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	return runEventStream(conn, f, stdout, stderr)
}

// dialEventStream dials the WebSocket endpoint, applying the timeout to the
// handshake only. The returned connection is ready to use.
func dialEventStream(f eventsFlags, target string) (*websocket.Conn, error) {
	hdrs := http.Header{}
	if auth := f.authHeader(); auth != "" {
		hdrs.Set("Authorization", auth)
	}

	dialCtx := context.Background()
	var dialCancel context.CancelFunc
	if f.timeout > 0 {
		dialCtx, dialCancel = context.WithTimeout(dialCtx, f.timeout)
		defer dialCancel()
	}

	dialer := websocket.Dialer{HandshakeTimeout: f.timeout}
	conn, resp, err := dialer.DialContext(dialCtx, target, hdrs)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		return nil, fmt.Errorf("events tail: connect %s: %w", target, err)
	}
	return conn, nil
}

// runEventStream sends the subscribe frame, then reads and prints events until
// the connection closes or the process is interrupted (SIGINT / SIGTERM).
func runEventStream(conn *websocket.Conn, f eventsFlags, stdout, stderr io.Writer) error {
	// writeMu guards all conn.Write* calls. gorilla/websocket forbids
	// concurrent writes; both the subscribe frame and pong replies must
	// go through this mutex.
	var writeMu sync.Mutex

	if err := sendSubscribe(conn, &writeMu, f.topics, f.classify); err != nil {
		return err
	}

	// Signal context: stream until interrupted.
	streamCtx, stopStream := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopStream()

	// Close the connection cleanly when the stream context fires (Ctrl-C / SIGTERM).
	go func() {
		<-streamCtx.Done()
		writeMu.Lock()
		_ = conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		writeMu.Unlock()
	}()

	return readEvents(streamCtx, conn, &writeMu, stdout, stderr, f.jsonOut)
}

// sendSubscribe marshals and writes the subscribe frame.
func sendSubscribe(conn *websocket.Conn, mu *sync.Mutex, topics string, classify bool) error {
	subMsg := eventsSubscribeMsg{
		Op:       "subscribe",
		Topics:   splitTopics(topics),
		Classify: classify,
	}
	raw, err := json.Marshal(subMsg)
	if err != nil {
		return fmt.Errorf("events tail: marshal subscribe: %w", err)
	}
	mu.Lock()
	err = conn.WriteMessage(websocket.TextMessage, raw)
	mu.Unlock()
	if err != nil {
		return fmt.Errorf("events tail: send subscribe: %w", err)
	}
	return nil
}

// readEvents is the main read loop. It blocks until the connection closes or
// the stream context is cancelled.
func readEvents(ctx context.Context, conn *websocket.Conn, writeMu *sync.Mutex, stdout, stderr io.Writer, jsonOut bool) error {
	streamCtx := ctx
	for {
		msgType, data, err := conn.ReadMessage()
		if err != nil {
			// A clean close triggered by our own signal handler is not an error.
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				return nil
			}
			if streamCtx.Err() != nil {
				return nil //nolint:nilerr // context cancelled by SIGINT/SIGTERM is a clean exit, not an error
			}
			return fmt.Errorf("events tail: read: %w", err)
		}
		if msgType != websocket.TextMessage {
			continue
		}

		var msg eventsInbound
		if err := json.Unmarshal(data, &msg); err != nil {
			_, _ = fmt.Fprintf(stderr, "events tail: malformed frame: %v\n", err)
			continue
		}

		// Respond to server-side application-level ping ("op":"ping").
		// gorilla handles the WebSocket protocol-level ping/pong automatically
		// via its default PongHandler, so only the JSON-layer heartbeat needs
		// explicit handling here.
		if msg.Op == "ping" {
			pong, _ := json.Marshal(eventsPongMsg{Op: "pong"})
			writeMu.Lock()
			_ = conn.WriteMessage(websocket.TextMessage, pong)
			writeMu.Unlock()
			continue
		}

		// Skip ack frames and other control messages.
		if msg.Op != "" {
			continue
		}

		if err := printEvent(stdout, msg, jsonOut); err != nil {
			return fmt.Errorf("events tail: write output: %w", err)
		}
	}
}

// printEvent writes one event frame to w. When jsonOut is true the original
// frame is re-encoded as a single compact JSON line; otherwise a human-readable
// summary line is written.
func printEvent(w io.Writer, msg eventsInbound, jsonOut bool) error {
	if jsonOut {
		enc, err := json.Marshal(msg)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(w, "%s\n", enc)
		return err
	}

	// Human line: "HH:MM:SS  <type>  <topic>  <payload-summary>"
	ts := formatEventTS(msg.TS)
	payload := compactPayload(msg.Payload)
	_, err := fmt.Fprintf(w, "%s  %-30s  %-40s  %s\n", ts, msg.Type, msg.Topic, payload)
	return err
}

// formatEventTS converts an ISO-8601 timestamp to HH:MM:SS for human display.
// Falls back to the raw string on parse failure.
func formatEventTS(raw string) string {
	if raw == "" {
		return time.Now().Format("15:04:05")
	}
	t, err := time.Parse("2006-01-02T15:04:05.000Z", raw)
	if err != nil {
		t, err = time.Parse(time.RFC3339, raw)
		if err != nil {
			return raw
		}
	}
	return t.Local().Format("15:04:05")
}

// compactPayload returns a one-line summary of the payload JSON. For small
// payloads (≤ 120 chars) the compact JSON is returned verbatim; larger ones
// are truncated with an ellipsis so the human line stays readable.
func compactPayload(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return string(raw)
	}
	s := buf.String()
	if len(s) <= 120 {
		return s
	}
	return s[:117] + "..."
}

// splitTopics parses a comma-separated topic string into a slice.
// Empty fields (consecutive commas, trailing commas) are dropped.
func splitTopics(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return []string{"*"}
	}
	return out
}
