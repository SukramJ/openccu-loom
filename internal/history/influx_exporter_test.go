// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package history

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// newTestExporter builds an InfluxExporter pointed at the given endpoint
// with a flush interval long enough that the ticker never fires during tests
// (Shutdown's final flush is used instead for determinism).
func newTestExporter(t *testing.T, endpoint string, extra ...func(*InfluxConfig)) *InfluxExporter {
	t.Helper()
	cfg := InfluxConfig{
		Endpoint:      endpoint,
		Org:           "testorg",
		Bucket:        "testbucket",
		Token:         "testtoken",
		Measurement:   DefaultInfluxMeasurement,
		FlushInterval: 24 * time.Hour, // keep ticker from firing
		MaxBuffer:     DefaultMaxBuffer,
	}
	for _, fn := range extra {
		fn(&cfg)
	}
	e := NewInfluxExporter(cfg)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = e.Shutdown(ctx)
	})
	return e
}

// fixedSample returns a deterministic MeasurementSample for use in assertions.
func fixedSample() sqlite.MeasurementSample {
	return sqlite.MeasurementSample{
		CentralName:    "Home",
		InterfaceID:    "Home-HmIP-RF",
		ChannelAddress: "ABC:4",
		Parameter:      "ACTUAL_TEMPERATURE",
		TS:             time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC),
		Value:          21.5,
	}
}

// ============================================================
// escapeTag / escapeMeasurement
// ============================================================

func TestEscapeTag(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want string
	}{
		{"plain", "plain"},
		{"with,comma", `with\,comma`},
		{"with space", `with\ space`},
		{"with=equals", `with\=equals`},
		{"a,b c=d", `a\,b\ c\=d`},
		{"", ""},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			got := escapeTag(tc.in)
			if got != tc.want {
				t.Errorf("escapeTag(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestEscapeMeasurement(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want string
	}{
		{"plain", "plain"},
		{"with,comma", `with\,comma`},
		{"with space", `with\ space`},
		// equals is NOT escaped in measurement names (only comma+space)
		{"with=equals", "with=equals"},
		{"a,b c", `a\,b\ c`},
		{"", ""},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			got := escapeMeasurement(tc.in)
			if got != tc.want {
				t.Errorf("escapeMeasurement(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// ============================================================
// lineProtocol formatting
// ============================================================

func TestLineProtocol_PlainSample(t *testing.T) {
	t.Parallel()
	e := newTestExporter(t, "http://127.0.0.1:19999") // unreachable; not flushed
	s := fixedSample()

	got := e.lineProtocol([]sqlite.MeasurementSample{s})

	wantPrefix := "openccu_loom,central=Home,interface_id=Home-HmIP-RF,channel=ABC:4,parameter=ACTUAL_TEMPERATURE value=21.5 "
	if !strings.HasPrefix(got, wantPrefix) {
		t.Errorf("lineProtocol prefix mismatch:\ngot  %q\nwant prefix %q", got, wantPrefix)
	}
	if !strings.HasSuffix(got, "\n") {
		t.Errorf("lineProtocol should end with newline, got %q", got)
	}

	// Verify the timestamp field equals sample.TS.UnixNano().
	parts := strings.SplitN(got, " value=21.5 ", 2)
	if len(parts) != 2 {
		t.Fatalf("lineProtocol missing ' value=21.5 ' separator: %q", got)
	}
	gotTS := strings.TrimSuffix(parts[1], "\n")
	wantTS := strconv.FormatInt(s.TS.UnixNano(), 10)
	if gotTS != wantTS {
		t.Errorf("lineProtocol timestamp = %q, want %q", gotTS, wantTS)
	}
}

func TestLineProtocol_ExactLine(t *testing.T) {
	t.Parallel()
	e := newTestExporter(t, "http://127.0.0.1:19999")
	s := fixedSample()

	got := e.lineProtocol([]sqlite.MeasurementSample{s})

	wantLine := "openccu_loom,central=Home,interface_id=Home-HmIP-RF,channel=ABC:4,parameter=ACTUAL_TEMPERATURE value=21.5 " +
		strconv.FormatInt(s.TS.UnixNano(), 10) + "\n"
	if got != wantLine {
		t.Errorf("lineProtocol exact mismatch:\ngot  %q\nwant %q", got, wantLine)
	}
}

func TestLineProtocol_TagEscaping(t *testing.T) {
	t.Parallel()
	e := newTestExporter(t, "http://127.0.0.1:19999")
	s := sqlite.MeasurementSample{
		CentralName:    "My CCU",  // space → escaped
		InterfaceID:    "HmIP,RF", // comma → escaped
		ChannelAddress: "DEV:1",
		Parameter:      "TEMP=F", // equals → escaped
		TS:             time.Now(),
		Value:          0.0,
	}
	got := e.lineProtocol([]sqlite.MeasurementSample{s})
	if !strings.Contains(got, `central=My\ CCU`) {
		t.Errorf("lineProtocol did not escape space in CentralName: %q", got)
	}
	if !strings.Contains(got, `interface_id=HmIP\,RF`) {
		t.Errorf("lineProtocol did not escape comma in InterfaceID: %q", got)
	}
	if !strings.Contains(got, `parameter=TEMP\=F`) {
		t.Errorf("lineProtocol did not escape equals in Parameter: %q", got)
	}
}

// ============================================================
// End-to-end POST via httptest.Server (success path)
// ============================================================

func TestInfluxExporter_EndToEnd_Success(t *testing.T) {
	t.Parallel()

	type capturedRequest struct {
		method string
		path   string
		query  string
		auth   string
		body   string
	}
	var captured capturedRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		captured = capturedRequest{
			method: r.Method,
			path:   r.URL.Path,
			query:  r.URL.RawQuery,
			auth:   r.Header.Get("Authorization"),
			body:   string(body),
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	e := newTestExporter(t, srv.URL)
	s := fixedSample()
	e.Export(s)

	// Shutdown triggers the final flush.
	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := e.Shutdown(shutCtx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	// Verify the captured HTTP request.
	if captured.method != http.MethodPost {
		t.Errorf("HTTP method = %q, want POST", captured.method)
	}
	if captured.path != "/api/v2/write" {
		t.Errorf("HTTP path = %q, want /api/v2/write", captured.path)
	}
	if !strings.Contains(captured.query, "org=testorg") {
		t.Errorf("query missing org: %q", captured.query)
	}
	if !strings.Contains(captured.query, "bucket=testbucket") {
		t.Errorf("query missing bucket: %q", captured.query)
	}
	if !strings.Contains(captured.query, "precision=ns") {
		t.Errorf("query missing precision=ns: %q", captured.query)
	}
	if captured.auth != "Token testtoken" {
		t.Errorf("Authorization = %q, want %q", captured.auth, "Token testtoken")
	}
	wantBodyLine := "openccu_loom,central=Home,interface_id=Home-HmIP-RF,channel=ABC:4,parameter=ACTUAL_TEMPERATURE value=21.5 "
	if !strings.Contains(captured.body, wantBodyLine) {
		t.Errorf("body does not contain expected line:\nbody=%q\nwant substring=%q", captured.body, wantBodyLine)
	}

	m := e.Metrics()
	if m.Exported != 1 {
		t.Errorf("Metrics.Exported = %d, want 1", m.Exported)
	}
	if m.Failures != 0 {
		t.Errorf("Metrics.Failures = %d, want 0", m.Failures)
	}
}

func TestInfluxExporter_EndToEnd_MultipleSamples(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	e := newTestExporter(t, srv.URL)
	for i := range 3 {
		s := fixedSample()
		s.Value = float64(i) + 1.0
		e.Export(s)
	}

	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := e.Shutdown(shutCtx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	m := e.Metrics()
	if m.Exported != 3 {
		t.Errorf("Metrics.Exported = %d, want 3", m.Exported)
	}
	if m.Failures != 0 {
		t.Errorf("Metrics.Failures = %d, want 0", m.Failures)
	}
}

// ============================================================
// Non-2xx response bumps Failures
// ============================================================

func TestInfluxExporter_ServerError_BumpsFailures(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	e := newTestExporter(t, srv.URL)
	e.Export(fixedSample())

	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = e.Shutdown(shutCtx)

	m := e.Metrics()
	if m.Failures < 1 {
		t.Errorf("Metrics.Failures = %d, want >= 1 after 500 response", m.Failures)
	}
	if m.Exported != 0 {
		t.Errorf("Metrics.Exported = %d, want 0 after 500 response", m.Exported)
	}
}

// ============================================================
// Export drop-oldest
// ============================================================

func TestInfluxExporter_DropOldest(t *testing.T) {
	t.Parallel()

	// MaxBuffer:2, long flush interval so the ticker never fires;
	// we call drain() directly to inspect the buffer without an HTTP round-trip.
	e := newTestExporter(t, "http://127.0.0.1:19999", func(cfg *InfluxConfig) {
		cfg.MaxBuffer = 2
		cfg.FlushInterval = 24 * time.Hour
	})

	s1 := fixedSample()
	s1.Value = 1.0
	s2 := fixedSample()
	s2.Value = 2.0
	s3 := fixedSample()
	s3.Value = 3.0

	e.Export(s1)
	e.Export(s2)
	e.Export(s3) // should drop s1 (oldest) and keep s2, s3

	m := e.Metrics()
	if m.Dropped != 1 {
		t.Errorf("Metrics.Dropped = %d, want 1", m.Dropped)
	}

	drained := e.drain()
	if len(drained) != 2 {
		t.Fatalf("drain returned %d items, want 2", len(drained))
	}
	if drained[0].Value != 2.0 {
		t.Errorf("drain[0].Value = %v, want 2.0 (second-oldest survives)", drained[0].Value)
	}
	if drained[1].Value != 3.0 {
		t.Errorf("drain[1].Value = %v, want 3.0 (newest)", drained[1].Value)
	}
}

// ============================================================
// Shutdown is bounded by context deadline
// ============================================================

func TestInfluxExporter_Shutdown_Bounded(t *testing.T) {
	t.Parallel()

	e := newTestExporter(t, "http://127.0.0.1:19999")

	alreadyCancelled, cancel := context.WithCancel(context.Background())
	cancel() // cancel before calling Shutdown

	done := make(chan error, 1)
	go func() {
		done <- e.Shutdown(alreadyCancelled)
	}()

	select {
	case <-done:
		// returned promptly — pass
	case <-time.After(2 * time.Second):
		t.Error("Shutdown did not return within 2s with a cancelled context")
	}
}

// ============================================================
// Failed flush keeps the batch
// ============================================================

// TestInfluxExporter_RetryableFailure_RequeuesBatch pins that a transient
// Influx outage does not cost the samples buffered since the last flush: the
// batch goes back into the buffer and the next tick retries it. Without the
// requeue every sample drained into a failing POST is gone for good, which is
// silent data loss on the export plane.
func TestInfluxExporter_RetryableFailure_RequeuesBatch(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		status int
	}{
		{"server error", http.StatusInternalServerError},
		{"rate limited", http.StatusTooManyRequests},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.ReadAll(r.Body)
				w.WriteHeader(tc.status)
			}))
			t.Cleanup(srv.Close)

			e := newTestExporter(t, srv.URL)
			e.Export(fixedSample())
			e.flush(context.Background())

			if m := e.Metrics(); m.Failures != 1 || m.Exported != 0 || m.Dropped != 0 {
				t.Errorf("metrics = %+v, want Failures=1 Exported=0 Dropped=0", m)
			}
			left := e.drain()
			if len(left) != 1 {
				t.Fatalf("buffer holds %d samples after a failed flush, want 1 (batch requeued)", len(left))
			}
			if left[0].Value != 21.5 {
				t.Errorf("requeued sample = %+v, want the original", left[0])
			}
		})
	}
}

// TestInfluxExporter_PermanentFailure_DropsBatch pins the other side of the
// requeue: a payload or token Influx rejects will be rejected again on every
// retry, so keeping it would wedge the bounded buffer full of poison and push
// live samples out of it. Such a batch is dropped and counted instead.
func TestInfluxExporter_PermanentFailure_DropsBatch(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)

	e := newTestExporter(t, srv.URL)
	e.Export(fixedSample())
	e.flush(context.Background())

	if m := e.Metrics(); m.Failures != 1 || m.Dropped != 1 {
		t.Errorf("metrics = %+v, want Failures=1 Dropped=1", m)
	}
	if left := e.drain(); len(left) != 0 {
		t.Errorf("buffer holds %d samples after a rejected batch, want 0", len(left))
	}
}

// TestInfluxExporter_RequeueRespectsMaxBuffer checks that the requeue stays
// bounded: samples that arrived during the failed flush are kept, and the
// oldest are dropped and counted when the combined batch exceeds MaxBuffer.
func TestInfluxExporter_RequeueRespectsMaxBuffer(t *testing.T) {
	t.Parallel()

	e := newTestExporter(t, "http://127.0.0.1:19999", func(cfg *InfluxConfig) {
		cfg.MaxBuffer = 2
	})
	newest := fixedSample()
	newest.Value = 9.0
	e.Export(newest)

	old1 := fixedSample()
	old1.Value = 1.0
	old2 := fixedSample()
	old2.Value = 2.0
	e.requeue([]sqlite.MeasurementSample{old1, old2})

	if m := e.Metrics(); m.Dropped != 1 {
		t.Errorf("Metrics.Dropped = %d, want 1 (oldest evicted at MaxBuffer)", m.Dropped)
	}
	got := e.drain()
	if len(got) != 2 || got[0].Value != 2.0 || got[1].Value != 9.0 {
		t.Fatalf("buffer = %+v, want [2.0, 9.0] (requeued batch first, newest last)", got)
	}
}
