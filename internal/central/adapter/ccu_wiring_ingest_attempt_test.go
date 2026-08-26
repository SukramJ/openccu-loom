// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmlog"
)

// TestIngestAttemptContextDemotesRetriedFailures asserts the severity an
// operator actually sees for each boot-time ingest attempt.
//
// The assertion runs through hmlog.StartOp rather than inspecting the
// context, because the context markers are unexported and, more to the
// point, the log record is the artefact under test: a healthy boot that
// recovered on its second attempt was shipping `level: error`, which is
// what operators reported.
func TestIngestAttemptContextDemotesRetriedFailures(t *testing.T) {
	t.Parallel()

	const retries = 5
	tests := []struct {
		name      string
		attempt   int
		err       error
		slow      bool
		wantLevel string
	}{
		{
			name:      "first attempt fails and will be retried",
			attempt:   0,
			err:       errors.New("http 503: internal backend exception"),
			wantLevel: "WARN",
		},
		{
			name:      "last retryable attempt still demotes",
			attempt:   retries - 1,
			err:       errors.New("http 503: internal backend exception"),
			wantLevel: "WARN",
		},
		{
			name:      "final attempt reports the failure as one",
			attempt:   retries,
			err:       errors.New("http 503: internal backend exception"),
			wantLevel: "ERROR",
		},
		{
			name:      "slow call against a booting CCU is not a warning",
			attempt:   0,
			slow:      true,
			wantLevel: "INFO",
		},
		{
			name:      "slow call on the final attempt is still tolerated",
			attempt:   retries,
			slow:      true,
			wantLevel: "INFO",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

			opts := hmlog.OpOptions{Logger: logger}
			if tc.slow {
				opts.SlowThreshold = time.Nanosecond
			}
			ctx := ingestAttemptContext(context.Background(), tc.attempt, retries)
			_, closer := hmlog.StartOp(ctx, "xml-rpc.listDevices", opts)
			if tc.slow {
				time.Sleep(time.Millisecond)
			}
			closer(tc.err)

			if got := endLevel(t, &buf); got != tc.wantLevel {
				t.Errorf("end-record level = %q, want %q", got, tc.wantLevel)
			}
		})
	}
}

// endLevel returns the level of the last op.end record written to buf.
func endLevel(t *testing.T, buf *bytes.Buffer) string {
	t.Helper()
	var level string
	for line := range bytes.SplitSeq(bytes.TrimRight(buf.Bytes(), "\n"), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal(line, &rec); err != nil {
			t.Fatalf("log line is not valid JSON: %v (%q)", err, line)
		}
		if rec["msg"] == "op.end" {
			level, _ = rec["level"].(string)
		}
	}
	if level == "" {
		t.Fatalf("no op.end record emitted; got %q", buf.String())
	}
	return level
}
