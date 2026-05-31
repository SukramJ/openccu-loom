// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// log_stream_resume_contract_test.go asserts the invariant that a resuming
// SSE log stream never delivers records that the client has already seen.
// The cursor is the record Seq; Since(seq) must return only records with
// Seq > seq for every possible cursor value including 0 and the current max.
package contract

import (
	"bufio"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/pkg/hmlog"
)

// buildLiveLogWithRecords populates a LiveLog by emitting n records via a
// TeeHandler-backed logger, returning the populated log and the logger.
func buildLiveLogWithRecords(n int) (*hmlog.LiveLog, *slog.Logger) {
	live := hmlog.NewLiveLog(200)
	core := slog.NewTextHandler(noopWriter{}, &slog.HandlerOptions{Level: slog.LevelDebug})
	tee := hmlog.NewTeeHandler(core)
	tee.AttachLive(live)
	logger := slog.New(tee)
	for i := range n {
		logger.Info("record", slog.Int("i", i))
	}
	return live, logger
}

// noopWriter discards log output; we only care about the LiveLog ring.
type noopWriter struct{}

func (noopWriter) Write(p []byte) (int, error) { return len(p), nil }

// TestLogStreamResume_SinceReturnsOnlyNewerRecords verifies that Since(seq)
// returns only records with Seq > seq for several cursor values.
func TestLogStreamResume_SinceReturnsOnlyNewerRecords(t *testing.T) {
	t.Parallel()

	live, _ := buildLiveLogWithRecords(10)
	// Cursors to exercise: 0 (all), 3 (mid), lastSeq (nothing new).
	lastSeq := live.LastSeq()

	cases := []struct {
		cursor  uint64
		wantMin int // minimum expected records (exclusive lower bound check)
	}{
		{0, 10},          // since 0 → all 10 records
		{3, 7},           // since 3 → 7 records (seq 4..10)
		{lastSeq, 0},     // since lastSeq → nothing
		{lastSeq + 1, 0}, // since future → nothing
	}

	for _, tc := range cases {
		tc := tc
		t.Run("since="+strconv.FormatUint(tc.cursor, 10), func(t *testing.T) {
			t.Parallel()
			records := live.Since(tc.cursor, slog.LevelDebug)
			for _, rec := range records {
				if rec.Seq <= tc.cursor {
					t.Errorf("Since(%d) returned record with Seq=%d (not strictly greater)",
						tc.cursor, rec.Seq)
				}
			}
			if len(records) < tc.wantMin {
				t.Errorf("Since(%d) returned %d records, want >= %d",
					tc.cursor, len(records), tc.wantMin)
			}
		})
	}
}

// TestLogStreamResume_SSEHandler_BackfillNoDuplicates exercises the
// StreamLogs HTTP handler with ?since= and Last-Event-ID and verifies that
// no id: value in the SSE body is <= the cursor. The request context is
// cancelled immediately after the handler returns so the test does not
// wait for the heartbeat ticker.
func TestLogStreamResume_SSEHandler_BackfillNoDuplicates(t *testing.T) {
	t.Parallel()

	live, _ := buildLiveLogWithRecords(5)
	cursor := uint64(3)

	router := chi.NewRouter()
	router.Get("/stream", handlers.StreamLogs(live))

	cases := []struct {
		name     string
		buildReq func(ctx context.Context) *http.Request
	}{
		{
			name: "query-since",
			buildReq: func(ctx context.Context) *http.Request {
				req := httptest.NewRequest(http.MethodGet,
					"/stream?since="+strconv.FormatUint(cursor, 10), http.NoBody)
				return req.WithContext(ctx)
			},
		},
		{
			name: "last-event-id-header",
			buildReq: func(ctx context.Context) *http.Request {
				req := httptest.NewRequest(http.MethodGet, "/stream", http.NoBody)
				req.Header.Set("Last-Event-ID", strconv.FormatUint(cursor, 10))
				return req.WithContext(ctx)
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Cancel the context as soon as the goroutine starts so the
			// handler exits quickly without waiting for the heartbeat.
			ctx, cancel := context.WithCancel(context.Background())
			cancel() // cancel immediately: the handler sees ctx.Done() and exits

			req := tc.buildReq(ctx)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			body := w.Body.String()
			// Extract every "id: N" line and verify no Seq <= cursor.
			scanner := bufio.NewScanner(strings.NewReader(body))
			for scanner.Scan() {
				line := strings.TrimSpace(scanner.Text())
				if !strings.HasPrefix(line, "id: ") {
					continue
				}
				rawID := strings.TrimPrefix(line, "id: ")
				seq, err := strconv.ParseUint(rawID, 10, 64)
				if err != nil {
					t.Errorf("malformed SSE id line %q: %v", line, err)
					continue
				}
				if seq <= cursor {
					t.Errorf("SSE id %d <= cursor %d: replay detected", seq, cursor)
				}
			}
		})
	}
}

// TestLogStreamResume_SSEHandler_EventFrameShape verifies the canonical
// SSE event format: each log event must have id:, event: log, and data:
// lines in the body when backfill records are present.
func TestLogStreamResume_SSEHandler_EventFrameShape(t *testing.T) {
	t.Parallel()

	live, _ := buildLiveLogWithRecords(3)

	router := chi.NewRouter()
	router.Get("/stream", handlers.StreamLogs(live))

	// Cancel immediately so the handler exits after backfill without waiting
	// for the heartbeat ticker.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	req := httptest.NewRequest(http.MethodGet, "/stream?since=0", http.NoBody).WithContext(ctx)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	body := w.Body.String()
	if !strings.Contains(body, "event: log\n") {
		t.Errorf("SSE body missing 'event: log' line\nbody:\n%s", body)
	}
	if !strings.Contains(body, "data: ") {
		t.Errorf("SSE body missing 'data: ' line\nbody:\n%s", body)
	}
	if !strings.Contains(body, "id: ") {
		t.Errorf("SSE body missing 'id: ' line\nbody:\n%s", body)
	}
}
