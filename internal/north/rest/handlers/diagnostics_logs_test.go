// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/pkg/hmlog"
)

// --------------------------------------------------------------------------
// fakeLogFeedService
// --------------------------------------------------------------------------

type fakeLogFeedService struct {
	snapshotResult []hmlog.LogRecord
	snapshotLimit  int
	snapshotLevel  slog.Level
	sinceResult    []hmlog.LogRecord
	sinceSeq       uint64
	sinceLevel     slog.Level
	subscribeCh    chan hmlog.LogRecord
	lastSeqResult  uint64
}

func (f *fakeLogFeedService) Snapshot(limit int, minLevel slog.Level) []hmlog.LogRecord {
	f.snapshotLimit = limit
	f.snapshotLevel = minLevel
	return f.snapshotResult
}

func (f *fakeLogFeedService) Since(seq uint64, minLevel slog.Level) []hmlog.LogRecord {
	f.sinceSeq = seq
	f.sinceLevel = minLevel
	return f.sinceResult
}

func (f *fakeLogFeedService) Subscribe(_ slog.Level) (events <-chan hmlog.LogRecord, cancel func()) {
	return f.subscribeCh, func() {}
}

func (f *fakeLogFeedService) LastSeq() uint64 {
	return f.lastSeqResult
}

// cannedRecords returns a small slice of LogRecord for use in tests.
func cannedRecords(n int) []hmlog.LogRecord {
	out := make([]hmlog.LogRecord, n)
	for i := range out {
		out[i] = hmlog.LogRecord{
			Seq:   uint64(i + 1),
			Time:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			Level: "info",
			Msg:   "test",
		}
	}
	return out
}

// --------------------------------------------------------------------------
// fakeLogDefaultLevelService
// --------------------------------------------------------------------------

type fakeLogDefaultLevelService struct {
	defaultLevel slog.Level
	setCalled    []slog.Level
}

func (f *fakeLogDefaultLevelService) Default() slog.Level { return f.defaultLevel }

func (f *fakeLogDefaultLevelService) SetDefault(level slog.Level) {
	f.setCalled = append(f.setCalled, level)
	f.defaultLevel = level
}

// --------------------------------------------------------------------------
// ListLogs — nil service
// --------------------------------------------------------------------------

func TestListLogs_NilService_Returns503(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics/logs", http.NoBody)
	w := httptest.NewRecorder()
	handlers.ListLogs(nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
}

// --------------------------------------------------------------------------
// ListLogs — happy path JSON
// --------------------------------------------------------------------------

func TestListLogs_HappyPath_Returns200WithRecords(t *testing.T) {
	t.Parallel()
	svc := &fakeLogFeedService{
		snapshotResult: cannedRecords(3),
		lastSeqResult:  3,
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics/logs", http.NoBody)
	w := httptest.NewRecorder()
	handlers.ListLogs(svc).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	ct := w.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var resp handlers.LogsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.LastSeq != 3 {
		t.Errorf("last_seq = %d, want 3", resp.LastSeq)
	}
	if len(resp.Records) != 3 {
		t.Errorf("records len = %d, want 3", len(resp.Records))
	}
}

// --------------------------------------------------------------------------
// ListLogs — ndjson format
// --------------------------------------------------------------------------

func TestListLogs_NdjsonFormat_OneLinePerRecord(t *testing.T) {
	t.Parallel()
	svc := &fakeLogFeedService{snapshotResult: cannedRecords(2)}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics/logs?format=ndjson", http.NoBody)
	w := httptest.NewRecorder()
	handlers.ListLogs(svc).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	ct := w.Header().Get("Content-Type")
	if !strings.Contains(ct, "application/x-ndjson") {
		t.Errorf("Content-Type = %q, want application/x-ndjson", ct)
	}
	lines := nonEmptyLines(w.Body.Bytes())
	if len(lines) != 2 {
		t.Errorf("ndjson lines = %d, want 2", len(lines))
	}
	for i, line := range lines {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Errorf("line %d is not valid JSON: %v — %q", i, err, line)
		}
	}
}

// --------------------------------------------------------------------------
// ListLogs — download=1 sets Content-Disposition
// --------------------------------------------------------------------------

func TestListLogs_Download_SetsContentDispositionJSON(t *testing.T) {
	t.Parallel()
	svc := &fakeLogFeedService{snapshotResult: cannedRecords(1)}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics/logs?download=1", http.NoBody)
	w := httptest.NewRecorder()
	handlers.ListLogs(svc).ServeHTTP(w, req)

	cd := w.Header().Get("Content-Disposition")
	if !strings.Contains(cd, "attachment") {
		t.Errorf("Content-Disposition = %q, want attachment", cd)
	}
}

func TestListLogs_Download_NdjsonSetsContentDisposition(t *testing.T) {
	t.Parallel()
	svc := &fakeLogFeedService{snapshotResult: cannedRecords(1)}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics/logs?format=ndjson&download=1", http.NoBody)
	w := httptest.NewRecorder()
	handlers.ListLogs(svc).ServeHTTP(w, req)

	cd := w.Header().Get("Content-Disposition")
	if !strings.Contains(cd, "attachment") {
		t.Errorf("Content-Disposition = %q, want attachment", cd)
	}
}

// --------------------------------------------------------------------------
// ListLogs — limit and min_level forwarded to service
// --------------------------------------------------------------------------

func TestListLogs_LimitParam_ForwardedToSnapshot(t *testing.T) {
	t.Parallel()
	svc := &fakeLogFeedService{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics/logs?limit=42", http.NoBody)
	w := httptest.NewRecorder()
	handlers.ListLogs(svc).ServeHTTP(w, req)

	if svc.snapshotLimit != 42 {
		t.Errorf("snapshotLimit = %d, want 42", svc.snapshotLimit)
	}
}

func TestListLogs_MinLevelParam_ForwardedToSnapshot(t *testing.T) {
	t.Parallel()
	svc := &fakeLogFeedService{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics/logs?min_level=warn", http.NoBody)
	w := httptest.NewRecorder()
	handlers.ListLogs(svc).ServeHTTP(w, req)

	if svc.snapshotLevel != slog.LevelWarn {
		t.Errorf("snapshotLevel = %v, want warn", svc.snapshotLevel)
	}
}

func TestListLogs_BogusMinLevel_FallsBackToDebug(t *testing.T) {
	t.Parallel()
	svc := &fakeLogFeedService{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics/logs?min_level=bogus", http.NoBody)
	w := httptest.NewRecorder()
	handlers.ListLogs(svc).ServeHTTP(w, req)

	if svc.snapshotLevel != slog.LevelDebug {
		t.Errorf("snapshotLevel = %v, want debug fallback", svc.snapshotLevel)
	}
}

// --------------------------------------------------------------------------
// StreamLogs — nil service
// --------------------------------------------------------------------------

func TestStreamLogs_NilService_Returns503(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics/logs/stream", http.NoBody)
	w := httptest.NewRecorder()
	handlers.StreamLogs(nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
}

// --------------------------------------------------------------------------
// StreamLogs — happy path: SSE headers + backfill + channel close
// --------------------------------------------------------------------------

func TestStreamLogs_HappyPath_SSEHeadersAndBackfill(t *testing.T) {
	t.Parallel()

	// Channel closed immediately so the handler exits after backfill.
	closed := make(chan hmlog.LogRecord)
	close(closed)

	svc := &fakeLogFeedService{
		sinceResult: cannedRecords(2),
		subscribeCh: closed,
	}

	ctx := t.Context()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics/logs/stream", http.NoBody).WithContext(ctx)
	w := httptest.NewRecorder()
	handlers.StreamLogs(svc).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	ct := w.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
	// The two backfill records must appear as SSE events.
	body := w.Body.String()
	for i := 1; i <= 2; i++ {
		wantID := strings.Contains(body, "id: "+itoa(i)+"\n")
		if !wantID {
			t.Errorf("SSE output missing id %d\nbody:\n%s", i, body)
		}
	}
	if !strings.Contains(body, "event: log\n") {
		t.Errorf("SSE output missing 'event: log' line\nbody:\n%s", body)
	}
}

// --------------------------------------------------------------------------
// StreamLogs — resume via ?since= and Last-Event-ID header
// --------------------------------------------------------------------------

func TestStreamLogs_SinceParam_ForwardedToSince(t *testing.T) {
	t.Parallel()

	closed := make(chan hmlog.LogRecord)
	close(closed)
	svc := &fakeLogFeedService{subscribeCh: closed}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics/logs/stream?since=5", http.NoBody)
	w := httptest.NewRecorder()
	handlers.StreamLogs(svc).ServeHTTP(w, req)

	if svc.sinceSeq != 5 {
		t.Errorf("sinceSeq = %d, want 5", svc.sinceSeq)
	}
}

func TestStreamLogs_LastEventIDHeader_ForwardedToSince(t *testing.T) {
	t.Parallel()

	closed := make(chan hmlog.LogRecord)
	close(closed)
	svc := &fakeLogFeedService{subscribeCh: closed}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics/logs/stream", http.NoBody)
	req.Header.Set("Last-Event-ID", "7")
	w := httptest.NewRecorder()
	handlers.StreamLogs(svc).ServeHTTP(w, req)

	if svc.sinceSeq != 7 {
		t.Errorf("sinceSeq = %d, want 7 (from Last-Event-ID header)", svc.sinceSeq)
	}
}

// --------------------------------------------------------------------------
// StreamLogs — live record delivered on channel
// --------------------------------------------------------------------------

func TestStreamLogs_LiveRecord_DeliveredInSSEOutput(t *testing.T) {
	t.Parallel()

	liveRec := hmlog.LogRecord{Seq: 99, Level: "warn", Msg: "live event", Time: time.Now()}
	ch := make(chan hmlog.LogRecord, 1)
	ch <- liveRec
	close(ch)

	svc := &fakeLogFeedService{subscribeCh: ch}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics/logs/stream", http.NoBody)
	w := httptest.NewRecorder()
	handlers.StreamLogs(svc).ServeHTTP(w, req)

	body := w.Body.String()
	if !strings.Contains(body, "id: 99\n") {
		t.Errorf("SSE output missing 'id: 99': %s", body)
	}
	if !strings.Contains(body, "live event") {
		t.Errorf("SSE output missing live event message: %s", body)
	}
}

// --------------------------------------------------------------------------
// GetDefaultLogLevel
// --------------------------------------------------------------------------

func TestGetDefaultLogLevel_NilService_Returns503(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics/log-level", http.NoBody)
	w := httptest.NewRecorder()
	handlers.GetDefaultLogLevel(nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestGetDefaultLogLevel_HappyPath_Returns200(t *testing.T) {
	t.Parallel()
	svc := &fakeLogDefaultLevelService{defaultLevel: slog.LevelInfo}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics/log-level", http.NoBody)
	w := httptest.NewRecorder()
	handlers.GetDefaultLogLevel(svc).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp handlers.LogDefaultLevelResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Default != "info" {
		t.Errorf("default = %q, want info", resp.Default)
	}
}

// --------------------------------------------------------------------------
// PutDefaultLogLevel
// --------------------------------------------------------------------------

func TestPutDefaultLogLevel_NilService_Returns503(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/diagnostics/log-level",
		strings.NewReader(`{"level":"debug"}`))
	w := httptest.NewRecorder()
	handlers.PutDefaultLogLevel(nil, nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPutDefaultLogLevel_HappyPath_Returns200AndSetsLevel(t *testing.T) {
	t.Parallel()
	svc := &fakeLogDefaultLevelService{defaultLevel: slog.LevelInfo}
	req := httptest.NewRequest(http.MethodPut, "/api/v1/diagnostics/log-level",
		strings.NewReader(`{"level":"debug"}`))
	w := httptest.NewRecorder()
	handlers.PutDefaultLogLevel(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp handlers.LogDefaultLevelResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Default != "debug" {
		t.Errorf("response default = %q, want debug", resp.Default)
	}
	if len(svc.setCalled) != 1 || svc.setCalled[0] != slog.LevelDebug {
		t.Errorf("SetDefault called with %v, want [debug]", svc.setCalled)
	}
}

func TestPutDefaultLogLevel_BadLevel_Returns400(t *testing.T) {
	t.Parallel()
	svc := &fakeLogDefaultLevelService{}
	req := httptest.NewRequest(http.MethodPut, "/api/v1/diagnostics/log-level",
		strings.NewReader(`{"level":"superverbose"}`))
	w := httptest.NewRecorder()
	handlers.PutDefaultLogLevel(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPutDefaultLogLevel_BadBody_Returns400(t *testing.T) {
	t.Parallel()
	svc := &fakeLogDefaultLevelService{}
	req := httptest.NewRequest(http.MethodPut, "/api/v1/diagnostics/log-level",
		strings.NewReader(`{not json}`))
	w := httptest.NewRecorder()
	handlers.PutDefaultLogLevel(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPutDefaultLogLevel_AuditRecorderCalledOnSuccess(t *testing.T) {
	t.Parallel()
	svc := &fakeLogDefaultLevelService{defaultLevel: slog.LevelInfo}
	rec := audit.NewBuffer(10)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/diagnostics/log-level",
		strings.NewReader(`{"level":"debug"}`))
	w := httptest.NewRecorder()
	handlers.PutDefaultLogLevel(svc, rec).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	entries := rec.List(10)
	if len(entries) != 1 {
		t.Fatalf("audit entries = %d, want 1", len(entries))
	}
	if entries[0].Action != audit.ActionLoggingDefaultLevelSet {
		t.Errorf("action = %q, want logging.default_level_set", entries[0].Action)
	}
}

// --------------------------------------------------------------------------
// helpers
// --------------------------------------------------------------------------

func nonEmptyLines(b []byte) []string {
	sc := bufio.NewScanner(bytes.NewReader(b))
	var out []string
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[pos:])
}
