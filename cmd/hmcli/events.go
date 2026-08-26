// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
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
	// broadcast event fields. Kind and Seq mirror outboundEvent
	// (internal/north/rest/ws/client.go) verbatim, without omitempty, so a
	// genuine zero seq or an event carrying no kind is not indistinguishable
	// from the field being absent — --json re-encodes this struct as the
	// JSONL line, so a dropped or omitted field here is a dropped field on
	// every consumer of `hmcli events tail --json`.
	Kind    string          `json:"kind"`
	Topic   string          `json:"topic,omitempty"`
	Type    string          `json:"type,omitempty"`
	TS      string          `json:"ts,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
	// control / ack fields
	Op     string   `json:"op,omitempty"`
	Topics []string `json:"topics,omitempty"`
	Seq    uint64   `json:"seq"`
}

// ─── flags ───────────────────────────────────────────────────────────────────

type eventsFlags struct {
	host     string
	token    string
	user     string
	password string
	cacert   string
	insecure bool
	// timeout bounds only the initial WebSocket handshake; the stream itself
	// runs until closed or interrupted so that --timeout 0 streams indefinitely.
	timeout  time.Duration
	topics   string
	jsonOut  bool
	classify bool
}

func (f *eventsFlags) bindTo(fs *flag.FlagSet) {
	fs.StringVar(&f.host, "host", defaultHost, "daemon REST base URL")
	fs.StringVar(&f.token, "token", "", "API bearer token (or set "+envToken+")")
	fs.StringVar(&f.user, "user", "", "basic-auth username")
	fs.StringVar(&f.password, "password", "", "basic-auth password (or set "+envPassword+")")
	fs.StringVar(&f.cacert, "cacert", "", "path to a PEM CA bundle to trust for TLS")
	fs.BoolVar(&f.insecure, "insecure", false, "skip TLS certificate verification (dangerous; off by default)")
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

// reconnectPolicy bounds the auto-reconnect behaviour after an abnormal stream
// drop: a capped number of consecutive retry attempts with exponential backoff.
// The counter resets once a connection has stayed up for resetAfter, so a
// long-lived healthy stream that hiccups is not penalised for old, unrelated
// drops while a rapidly flapping endpoint still exhausts the budget and exits.
type reconnectPolicy struct {
	maxRetries  int
	baseBackoff time.Duration
	maxBackoff  time.Duration
	resetAfter  time.Duration
}

// defaultReconnectPolicy is the production reconnect budget for `events tail`.
func defaultReconnectPolicy() reconnectPolicy {
	return reconnectPolicy{
		maxRetries:  5,
		baseBackoff: 500 * time.Millisecond,
		maxBackoff:  8 * time.Second,
		resetAfter:  30 * time.Second,
	}
}

// cmdEventsTail connects to the daemon's WebSocket event stream and prints
// arriving events until the connection closes cleanly or the process is
// interrupted. An abnormal drop (daemon death, network loss, abrupt peer close)
// triggers a bounded auto-reconnect; exhausting the retry budget exits non-zero.
func cmdEventsTail(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("events tail", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var f eventsFlags
	f.bindTo(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}

	// Resolve off-argv credentials once so they are reused across reconnects
	// without re-prompting. The plaintext warning uses the http(s) host — ws://
	// is as exposed as http://.
	f.token, f.user, f.password = resolveCredentials(f.token, f.user, f.password, os.Stdin, stderr)
	warnIfPlaintextCredentials(f.host, f.token, f.user, stderr)

	// See the note in cache.go: userinfo is stripped so a credential in
	// --host cannot resurface through the target embedded in errors and
	// reconnect diagnostics.
	target, err := wsURL(strings.TrimRight(redactHostUserinfo(f.host), "/"), "/api/v1/events")
	if err != nil {
		return err
	}

	tlsCfg, err := buildTLSConfig(f.cacert, f.insecure, stderr)
	if err != nil {
		return err
	}

	// The signal context spans the whole (re)connect lifecycle so a Ctrl-C
	// during the handshake, a backoff, or a reconnect still exits cleanly.
	streamCtx, stopStream := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopStream()

	// The initial connection fails fast: a daemon that is not reachable at
	// startup is an operator error, not a mid-stream drop to retry.
	conn, err := dialEventStream(streamCtx, f, target, tlsCfg)
	if err != nil {
		return err
	}

	return streamWithReconnect(streamCtx, conn, f, target, tlsCfg, defaultReconnectPolicy(), stdout, stderr)
}

// dialEventStream dials the WebSocket endpoint, applying the timeout to the
// handshake only and the supplied TLS config (may be nil for defaults). The
// dial honours ctx, so a signal cancels an in-flight handshake. The returned
// connection is ready to use.
func dialEventStream(ctx context.Context, f eventsFlags, target string, tlsCfg *tls.Config) (*websocket.Conn, error) {
	hdrs := http.Header{}
	if auth := f.authHeader(); auth != "" {
		hdrs.Set("Authorization", auth)
	}

	dialCtx := ctx
	var dialCancel context.CancelFunc
	if f.timeout > 0 {
		dialCtx, dialCancel = context.WithTimeout(ctx, f.timeout)
		defer dialCancel()
	}

	dialer := websocket.Dialer{HandshakeTimeout: f.timeout, TLSClientConfig: tlsCfg}
	conn, resp, err := dialer.DialContext(dialCtx, target, hdrs)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		return nil, fmt.Errorf("events tail: connect %s: %w", target, err)
	}
	return conn, nil
}

// streamWithReconnect runs the event stream over conn and, on an abnormal drop,
// re-dials with exponential backoff up to the policy's retry budget. A clean
// server close or a user interrupt returns nil (exit 0); exhausting the budget
// returns the last error (exit non-zero).
func streamWithReconnect(
	ctx context.Context,
	conn *websocket.Conn,
	f eventsFlags,
	target string,
	tlsCfg *tls.Config,
	pol reconnectPolicy,
	stdout, stderr io.Writer,
) error {
	failures := 0
	for {
		if conn == nil {
			c, derr := dialEventStream(ctx, f, target, tlsCfg)
			if derr != nil {
				failures++
				if failures > pol.maxRetries {
					return fmt.Errorf("events tail: reconnect failed after %d attempts: %w", pol.maxRetries, derr)
				}
				_, _ = fmt.Fprintf(stderr, "events tail: reconnect attempt %d/%d failed: %v\n", failures, pol.maxRetries, derr)
				if !sleepCtx(ctx, backoffFor(pol, failures)) {
					return nil
				}
				continue
			}
			conn = c
			_, _ = fmt.Fprintln(stderr, "events tail: reconnected")
		}

		start := time.Now()
		err := runEventStream(ctx, conn, f, stdout, stderr)
		_ = conn.Close()
		conn = nil

		if err == nil {
			return nil // clean server close or user interrupt
		}
		if ctx.Err() != nil {
			return nil //nolint:nilerr // a signal-cancelled context is a clean shutdown, not a stream failure
		}
		if time.Since(start) >= pol.resetAfter {
			failures = 0 // the connection was stable for a while: fresh budget
		}
		failures++
		if failures > pol.maxRetries {
			return fmt.Errorf("events tail: stream lost, gave up after %d reconnect attempts: %w", pol.maxRetries, err)
		}
		_, _ = fmt.Fprintf(stderr, "events tail: stream dropped (%v); reconnecting %d/%d\n", err, failures, pol.maxRetries)
		if !sleepCtx(ctx, backoffFor(pol, failures)) {
			return nil
		}
	}
}

// backoffFor returns the exponential backoff for the given 1-based attempt,
// capped at the policy's maximum.
func backoffFor(pol reconnectPolicy, attempt int) time.Duration {
	d := pol.baseBackoff << (attempt - 1)
	if d <= 0 || d > pol.maxBackoff {
		return pol.maxBackoff
	}
	return d
}

// sleepCtx sleeps for d, returning true if the full duration elapsed or false
// if ctx was cancelled first (a user interrupt during backoff).
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// runEventStream subscribes, then reads and prints events over a single
// connection until it closes or ctx is cancelled. It returns nil for a clean
// end (normal/going-away close or a ctx cancel) and a non-nil error for an
// abnormal drop.
func runEventStream(ctx context.Context, conn *websocket.Conn, f eventsFlags, stdout, stderr io.Writer) error {
	// writeMu guards all conn.Write* calls. gorilla/websocket forbids
	// concurrent writes; both the subscribe frame and pong replies must
	// go through this mutex.
	var writeMu sync.Mutex

	if err := sendSubscribe(conn, &writeMu, f.topics, f.classify); err != nil {
		return err
	}

	// Close this connection cleanly when ctx fires (Ctrl-C / SIGTERM). The done
	// channel stops the watcher when the read loop returns for any other reason,
	// so it never outlives the connection it guards across reconnects.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			writeMu.Lock()
			_ = conn.WriteMessage(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			writeMu.Unlock()
		case <-done:
		}
	}()

	return readEvents(ctx, conn, &writeMu, stdout, stderr, f.jsonOut)
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
// the stream context is cancelled. It returns nil for a clean end (user
// interrupt or a normal / going-away server close) and a non-nil error for an
// abnormal drop.
func readEvents(ctx context.Context, conn *websocket.Conn, writeMu *sync.Mutex, stdout, stderr io.Writer, jsonOut bool) error {
	for {
		msgType, data, err := conn.ReadMessage()
		if err != nil {
			// Interrupted by the user (SIGINT/SIGTERM): clean exit.
			if ctx.Err() != nil {
				return nil //nolint:nilerr // context cancelled by signal is a clean exit, not an error
			}
			// Clean server-initiated close: exit silently.
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				return nil
			}
			// Abnormal end: the daemon went away, the network dropped, or the
			// peer closed the socket abruptly (some platforms surface that as
			// ECONNRESET / wsarecv rather than a WebSocket close frame). Report
			// it as an error so the caller can auto-reconnect and, if that
			// fails, exit non-zero — a silent exit 0 would hide a lost stream.
			return fmt.Errorf("events tail: stream ended abnormally: %w", err)
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

	// Human line: "HH:MM:SS  <type>  <topic>  <payload-summary>". Every
	// server-controlled field is sanitised so a crafted name/type/topic or a
	// raw control byte inside the payload cannot rewrite the operator's
	// terminal (the JSON branch above already escapes control bytes).
	ts := sanitizeForTerminal(formatEventTS(msg.TS))
	payload := sanitizeForTerminal(compactPayload(msg.Payload))
	_, err := fmt.Fprintf(w, "%s  %-30s  %-40s  %s\n",
		ts, sanitizeForTerminal(msg.Type), sanitizeForTerminal(msg.Topic), payload)
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
