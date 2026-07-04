// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// wsConnBehavior controls how one server-side connection ends.
type wsConnBehavior int

const (
	wsAbnormal wsConnBehavior = iota // send an event, then drop the TCP socket (no close frame)
	wsClean                          // send an event, then a normal WebSocket close
)

// reconnectingServer stands up a WS server that applies behaviors[i] to the
// i-th connection (clamping to the last entry for further connections) and
// exposes the total connection count. It lets a test script an abnormal drop
// followed by a clean reconnect, or a run of abnormal drops.
func reconnectingServer(t *testing.T, behaviors []wsConnBehavior) (server *httptest.Server, connCount func() int) {
	t.Helper()
	var mu sync.Mutex
	count := 0
	upgrader := websocket.Upgrader{CheckOrigin: func(_ *http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		idx := count
		count++
		mu.Unlock()

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		if _, _, err := conn.ReadMessage(); err != nil { // subscribe frame
			return
		}

		b := behaviors[len(behaviors)-1]
		if idx < len(behaviors) {
			b = behaviors[idx]
		}
		_ = conn.WriteMessage(websocket.TextMessage, mustMarshal(t, map[string]any{
			"topic": "device.X", "type": "Evt", "ts": "2026-07-01T10:00:00.000Z",
		}))

		switch b {
		case wsClean:
			_ = conn.WriteControl(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseNormalClosure, "done"), time.Now().Add(time.Second))
			_ = conn.SetReadDeadline(time.Now().Add(time.Second))
			for {
				if _, _, err := conn.ReadMessage(); err != nil {
					break
				}
			}
		case wsAbnormal:
			// Return without a close handshake: the deferred conn.Close() drops
			// the TCP socket, which the client reads as an abnormal end.
		}
	}))
	t.Cleanup(srv.Close)
	return srv, func() int { mu.Lock(); defer mu.Unlock(); return count }
}

// fastReconnectPolicy is a millisecond-scale budget for tests; resetAfter is
// large so a fast-flapping server always exhausts the retry count.
func fastReconnectPolicy() reconnectPolicy {
	return reconnectPolicy{maxRetries: 3, baseBackoff: time.Millisecond, maxBackoff: 5 * time.Millisecond, resetAfter: time.Hour}
}

// dialFirst connects the initial event stream, mirroring cmdEventsTail's setup.
func dialFirst(t *testing.T, host string) (*websocket.Conn, eventsFlags, string) {
	t.Helper()
	f := eventsFlags{host: host, topics: "*"}
	target, err := wsURL(strings.TrimRight(f.host, "/"), "/api/v1/events")
	if err != nil {
		t.Fatalf("wsURL: %v", err)
	}
	conn, err := dialEventStream(context.Background(), f, target, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	return conn, f, target
}

func TestEventsTailReconnectsAfterAbnormalDrop(t *testing.T) {
	t.Parallel()
	srv, count := reconnectingServer(t, []wsConnBehavior{wsAbnormal, wsClean})
	conn, f, target := dialFirst(t, srv.URL)

	var stdout, stderr bytes.Buffer
	if err := streamWithReconnect(context.Background(), conn, f, target, nil, fastReconnectPolicy(), &stdout, &stderr); err != nil {
		t.Fatalf("expected a clean exit after reconnect, got: %v", err)
	}
	if got := count(); got != 2 {
		t.Errorf("connection count = %d, want 2 (abnormal drop then clean reconnect)", got)
	}
	if !strings.Contains(stderr.String(), "reconnected") {
		t.Errorf("expected a 'reconnected' notice on stderr, got: %q", stderr.String())
	}
}

func TestEventsTailGivesUpAfterRetryBudget(t *testing.T) {
	t.Parallel()
	pol := fastReconnectPolicy()
	srv, count := reconnectingServer(t, []wsConnBehavior{wsAbnormal})
	conn, f, target := dialFirst(t, srv.URL)

	var stdout, stderr bytes.Buffer
	err := streamWithReconnect(context.Background(), conn, f, target, nil, pol, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected a non-nil error after exhausting the reconnect budget")
	}
	if !strings.Contains(err.Error(), "gave up") {
		t.Errorf("error = %v, want it to mention giving up", err)
	}
	if got, want := count(), 1+pol.maxRetries; got != want {
		t.Errorf("connection count = %d, want %d (initial + maxRetries)", got, want)
	}
}

func TestEventsTailCleanCloseDoesNotReconnect(t *testing.T) {
	t.Parallel()
	srv, count := reconnectingServer(t, []wsConnBehavior{wsClean})
	conn, f, target := dialFirst(t, srv.URL)

	var stdout, stderr bytes.Buffer
	if err := streamWithReconnect(context.Background(), conn, f, target, nil, fastReconnectPolicy(), &stdout, &stderr); err != nil {
		t.Fatalf("clean close should exit 0, got: %v", err)
	}
	if got := count(); got != 1 {
		t.Errorf("connection count = %d, want 1 (no reconnect after a clean close)", got)
	}
}

func TestBackoffForCapsExponentially(t *testing.T) {
	t.Parallel()
	pol := reconnectPolicy{baseBackoff: 100 * time.Millisecond, maxBackoff: 400 * time.Millisecond}
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{1, 100 * time.Millisecond},
		{2, 200 * time.Millisecond},
		{3, 400 * time.Millisecond},
		{4, 400 * time.Millisecond}, // capped
		{10, 400 * time.Millisecond},
	}
	for _, tc := range cases {
		if got := backoffFor(pol, tc.attempt); got != tc.want {
			t.Errorf("backoffFor(attempt=%d) = %v, want %v", tc.attempt, got, tc.want)
		}
	}
}

func TestSleepCtxReturnsFalseWhenCancelled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if sleepCtx(ctx, time.Hour) {
		t.Error("sleepCtx should return false when the context is already cancelled")
	}
	if !sleepCtx(context.Background(), time.Millisecond) {
		t.Error("sleepCtx should return true when the full duration elapses")
	}
}
