// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

//go:build integration

package integration

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/pkg/hmlog"
)

// TestLogFeed is a hermetic integration test for the log-feed endpoints.
// It requires no CCU or godevccu; it builds the full hmlog stack in-process
// and drives the REST handlers directly via httptest.
func TestLogFeed(t *testing.T) {
	// Build the full logger stack so Stack.Logger + Stack.Live are wired.
	stack := hmlog.BuildFullStack(hmlog.StackOptions{
		Writer: io.Discard,
		Format: hmlog.FormatJSON,
	}, slog.LevelDebug)

	// Build a subsystem logger to exercise the logger column.
	binrpcLogger := stack.Logger.With(slog.String("logger", "client.binrpc"))

	// Mount the handlers on a minimal chi router.
	router := chi.NewRouter()
	router.Get("/diagnostics/logs", handlers.ListLogs(stack.Live))
	router.Get("/diagnostics/logs/stream", handlers.StreamLogs(stack.Live))

	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	// Log several records before assertions.
	stack.Logger.Info("first message")
	stack.Logger.Warn("second message")
	binrpcLogger.Info("binrpc message")

	// Give the LiveLog's ring a moment to settle (it is synchronous under
	// the lock, so a brief yield is sufficient).
	time.Sleep(10 * time.Millisecond)

	t.Run("ListLogs_returnsRecordsWithLoggerField", func(t *testing.T) {
		resp, err := http.Get(server.URL + "/diagnostics/logs?min_level=info")
		if err != nil {
			t.Fatalf("GET /diagnostics/logs: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}

		var body handlers.LogsResponse
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if len(body.Records) == 0 {
			t.Fatal("expected at least one record in ListLogs response")
		}
		if body.LastSeq == 0 {
			t.Error("last_seq must be > 0 after logging records")
		}

		// Check that the binrpc record carries the logger field.
		found := false
		for _, rec := range body.Records {
			if rec.Logger == "client.binrpc" {
				found = true
				break
			}
		}
		if !found {
			t.Error("expected a record with logger=client.binrpc in ListLogs response")
		}

		// Verify expected messages appear.
		hasMsg := func(want string) bool {
			for _, rec := range body.Records {
				if strings.Contains(rec.Msg, want) {
					return true
				}
			}
			return false
		}
		for _, wantMsg := range []string{"first message", "second message", "binrpc message"} {
			if !hasMsg(wantMsg) {
				t.Errorf("ListLogs missing record with msg containing %q", wantMsg)
			}
		}
	})

	t.Run("StreamLogs_deliversNewRecordAfterResume", func(t *testing.T) {
		// Capture last_seq from the list endpoint to use as resume cursor.
		resp, err := http.Get(server.URL + "/diagnostics/logs")
		if err != nil {
			t.Fatalf("GET /diagnostics/logs: %v", err)
		}
		var listBody handlers.LogsResponse
		if jerr := json.NewDecoder(resp.Body).Decode(&listBody); jerr != nil {
			resp.Body.Close()
			t.Fatalf("decode list body: %v", jerr)
		}
		resp.Body.Close()
		lastSeq := listBody.LastSeq

		// Open an SSE stream from lastSeq.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		streamURL := fmt.Sprintf("%s/diagnostics/logs/stream?since=%d", server.URL, lastSeq)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, streamURL, http.NoBody)
		if err != nil {
			t.Fatalf("build stream request: %v", err)
		}
		streamResp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("open SSE stream: %v", err)
		}
		defer streamResp.Body.Close()

		if streamResp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 on SSE stream, got %d", streamResp.StatusCode)
		}
		ct := streamResp.Header.Get("Content-Type")
		if !strings.HasPrefix(ct, "text/event-stream") {
			t.Errorf("Content-Type=%q want text/event-stream", ct)
		}

		// Log one more record — this should arrive on the stream.
		go func() {
			time.Sleep(50 * time.Millisecond)
			stack.Logger.Info("stream probe record")
		}()

		// Read SSE events until we find the probe record or timeout.
		got := make(chan string, 1)
		go func() {
			scanner := bufio.NewScanner(streamResp.Body)
			var dataLine string
			for scanner.Scan() {
				line := scanner.Text()
				if strings.HasPrefix(line, "data: ") {
					dataLine = strings.TrimPrefix(line, "data: ")
				}
				if line == "" && dataLine != "" {
					// Blank line ends an event frame.
					got <- dataLine
					dataLine = ""
				}
			}
			close(got)
		}()

		deadline := time.After(4 * time.Second)
		for {
			select {
			case data, open := <-got:
				if !open {
					t.Error("SSE stream closed without delivering probe record")
					return
				}
				if strings.Contains(data, "stream probe record") {
					return // success
				}
			case <-deadline:
				t.Error("timeout waiting for probe record on SSE stream")
				return
			}
		}
	})
}
