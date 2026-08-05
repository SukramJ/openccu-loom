// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package history

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SukramJ/openccu-loom/internal/httpx"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// DefaultInfluxMeasurement is the line-protocol measurement name used
// when the config does not override it.
const DefaultInfluxMeasurement = "openccu_loom"

// InfluxConfig configures an [InfluxExporter]. Endpoint, Org, Bucket and
// Token are required for a working exporter; the rest default.
type InfluxConfig struct {
	Endpoint    string // base URL, e.g. http://influx:8086
	Org         string
	Bucket      string
	Token       string // write token (a secret; sourced from env per ADR 0027)
	Measurement string // line-protocol measurement; default openccu_loom

	FlushInterval time.Duration // default DefaultFlushInterval
	MaxBuffer     int           // default DefaultMaxBuffer
	HTTPClient    *http.Client  // default: 10s timeout
	Logger        *slog.Logger
}

// InfluxExporter forwards recorded samples to an InfluxDB v2 endpoint via
// line protocol over HTTP — standard library only, no client dependency
// (same lean-binary reasoning as the OTLP exporter, ADR 0037). It buffers
// internally and flushes on an interval; Export never blocks and drops
// the oldest sample on overflow so a slow collector cannot stall the bus.
type InfluxExporter struct {
	writeURL    string
	token       string
	measurement string
	client      *http.Client
	logger      *slog.Logger
	maxBuffer   int

	mu  sync.Mutex
	buf []sqlite.MeasurementSample

	cancel context.CancelFunc
	done   chan struct{}

	exported atomic.Int64
	dropped  atomic.Int64
	failures atomic.Int64
}

// NewInfluxExporter builds an exporter and starts its background flush
// loop. Call Shutdown to stop it and flush the tail.
func NewInfluxExporter(cfg InfluxConfig) *InfluxExporter {
	measurement := cfg.Measurement
	if measurement == "" {
		measurement = DefaultInfluxMeasurement
	}
	flush := cfg.FlushInterval
	if flush <= 0 {
		flush = DefaultFlushInterval
	}
	maxBuf := cfg.MaxBuffer
	if maxBuf <= 0 {
		maxBuf = DefaultMaxBuffer
	}
	client := cfg.HTTPClient
	if client == nil {
		client = httpx.NewClient(10 * time.Second)
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	q := url.Values{}
	q.Set("org", cfg.Org)
	q.Set("bucket", cfg.Bucket)
	q.Set("precision", "ns")
	writeURL := strings.TrimRight(cfg.Endpoint, "/") + "/api/v2/write?" + q.Encode()

	ctx, cancel := context.WithCancel(context.Background())
	e := &InfluxExporter{
		writeURL:    writeURL,
		token:       cfg.Token,
		measurement: measurement,
		client:      client,
		logger:      logger,
		maxBuffer:   maxBuf,
		cancel:      cancel,
		done:        make(chan struct{}),
	}
	go e.loop(ctx, flush)
	return e
}

// Export buffers a sample, dropping the oldest on overflow. Non-blocking.
func (e *InfluxExporter) Export(s sqlite.MeasurementSample) {
	e.mu.Lock()
	if len(e.buf) >= e.maxBuffer {
		copy(e.buf, e.buf[1:])
		e.buf = e.buf[:len(e.buf)-1]
		e.dropped.Add(1)
	}
	e.buf = append(e.buf, s)
	e.mu.Unlock()
}

// Shutdown stops the loop and flushes the tail, bounded by ctx.
func (e *InfluxExporter) Shutdown(ctx context.Context) error {
	e.cancel()
	select {
	case <-e.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// InfluxMetrics is a snapshot of the exporter's counters.
type InfluxMetrics struct {
	Exported int64
	Dropped  int64
	Failures int64
}

// Metrics returns the cumulative counters since start.
func (e *InfluxExporter) Metrics() InfluxMetrics {
	if e == nil {
		return InfluxMetrics{}
	}
	return InfluxMetrics{
		Exported: e.exported.Load(),
		Dropped:  e.dropped.Load(),
		Failures: e.failures.Load(),
	}
}

func (e *InfluxExporter) loop(ctx context.Context, flush time.Duration) {
	defer close(e.done)
	ticker := time.NewTicker(flush)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			// Final flush on a fresh, bounded context — the loop ctx is
			// already cancelled, but the tail must still be written.
			flushCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			e.flush(flushCtx) //nolint:contextcheck // shutdown flush must not inherit the cancelled loop ctx
			cancel()
			return
		case <-ticker.C:
			e.flush(ctx)
		}
	}
}

func (e *InfluxExporter) drain() []sqlite.MeasurementSample {
	e.mu.Lock()
	if len(e.buf) == 0 {
		e.mu.Unlock()
		return nil
	}
	out := e.buf
	e.buf = make([]sqlite.MeasurementSample, 0, len(out))
	e.mu.Unlock()
	return out
}

func (e *InfluxExporter) flush(ctx context.Context) {
	batch := e.drain()
	if len(batch) == 0 {
		return
	}
	body := e.lineProtocol(batch)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.writeURL, strings.NewReader(body))
	if err != nil {
		e.failures.Add(1)
		e.logger.Warn("history.export.request_err", slog.String("err", err.Error()))
		return
	}
	req.Header.Set("Authorization", "Token "+e.token)
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	resp, err := e.client.Do(req)
	if err != nil {
		e.failures.Add(1)
		e.logger.Warn("history.export.post_err",
			slog.Int("samples", len(batch)), slog.String("err", err.Error()))
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		e.failures.Add(1)
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		e.logger.Warn("history.export.bad_status",
			slog.Int("status", resp.StatusCode),
			slog.Int("samples", len(batch)),
			slog.String("body", string(snippet)))
		return
	}
	// Drain the body so the connection can be reused.
	_, _ = io.Copy(io.Discard, resp.Body)
	e.exported.Add(int64(len(batch)))
}

// lineProtocol renders a batch as InfluxDB line protocol, one line per
// sample: <measurement>,<tags> value=<v> <ts_ns>.
func (e *InfluxExporter) lineProtocol(batch []sqlite.MeasurementSample) string {
	var b strings.Builder
	for i := range batch {
		s := &batch[i]
		b.WriteString(escapeMeasurement(e.measurement))
		b.WriteString(",central=")
		b.WriteString(escapeTag(s.CentralName))
		b.WriteString(",interface_id=")
		b.WriteString(escapeTag(s.InterfaceID))
		b.WriteString(",channel=")
		b.WriteString(escapeTag(s.ChannelAddress))
		b.WriteString(",parameter=")
		b.WriteString(escapeTag(s.Parameter))
		b.WriteString(" value=")
		b.WriteString(strconv.FormatFloat(s.Value, 'g', -1, 64))
		b.WriteByte(' ')
		b.WriteString(strconv.FormatInt(s.TS.UnixNano(), 10))
		b.WriteByte('\n')
	}
	return b.String()
}

// tagEscaper escapes the characters that are significant in line-protocol
// tag keys/values: comma, space and equals.
var tagEscaper = strings.NewReplacer(",", `\,`, " ", `\ `, "=", `\=`)

// measurementEscaper escapes comma and space in the measurement name.
var measurementEscaper = strings.NewReplacer(",", `\,`, " ", `\ `)

func escapeTag(s string) string         { return tagEscaper.Replace(s) }
func escapeMeasurement(s string) string { return measurementEscaper.Replace(s) }

// compile-time assertion that InfluxExporter satisfies the seam.
var _ MeasurementExporter = (*InfluxExporter)(nil)
