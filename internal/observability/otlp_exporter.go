// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SukramJ/openccu-loom/internal/httpx"
	"github.com/SukramJ/openccu-loom/pkg/hmlog"
)

// OTLPHTTPConfig configures the lean OTLP/HTTP span exporter.
// All zero values fall back to safe production defaults.
type OTLPHTTPConfig struct {
	// Endpoint is the base collector URL, e.g. http://collector:4318.
	// The exporter POSTs to <Endpoint>/v1/traces.
	Endpoint string

	// ServiceName is the OTLP resource service.name attribute.
	// Defaults to "openccu-loom" when empty.
	ServiceName string

	// BatchSize is the maximum number of spans per HTTP POST.
	// Defaults to 256 when zero.
	BatchSize int

	// FlushInterval is the maximum time between flushes.
	// Defaults to 5 s when zero.
	FlushInterval time.Duration

	// BufferSize is the capacity of the internal span channel.
	// Defaults to 2048 when zero. Spans dropped when full.
	BufferSize int

	// Client is the HTTP client used for export POSTs.
	// Defaults to &http.Client{Timeout: 10 s} when nil.
	Client *http.Client

	// Logger receives Warn-level messages when export fails.
	// Discards when nil.
	Logger *slog.Logger
}

// OTLPHTTPExporter is the process-wide OTLP/HTTP exporter. It satisfies
// SpanExporter: ExportSpan is non-blocking (enqueue only) and Shutdown
// drains the remaining buffer before returning.
type OTLPHTTPExporter struct {
	cfg     OTLPHTTPConfig
	ch      chan *Span
	done    chan struct{}
	once    sync.Once
	dropped atomic.Int64
	wg      sync.WaitGroup
}

// NewOTLPHTTPExporter constructs and starts an OTLP/HTTP span exporter.
// The background goroutine runs until Shutdown is called.
func NewOTLPHTTPExporter(cfg OTLPHTTPConfig) *OTLPHTTPExporter {
	if cfg.ServiceName == "" {
		cfg.ServiceName = "openccu-loom"
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 256
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = 5 * time.Second
	}
	if cfg.BufferSize <= 0 {
		cfg.BufferSize = 2048
	}
	if cfg.Client == nil {
		cfg.Client = httpx.NewClient(10 * time.Second)
	}

	e := &OTLPHTTPExporter{
		cfg:  cfg,
		ch:   make(chan *Span, cfg.BufferSize),
		done: make(chan struct{}),
	}
	e.wg.Add(1)
	go e.run()
	return e
}

// ExportSpan enqueues a finished span for batched export. This method
// never blocks: when the internal channel is full the span is dropped
// and the dropped counter is incremented.
func (e *OTLPHTTPExporter) ExportSpan(s *Span) {
	select {
	case e.ch <- s:
	default:
		e.dropped.Add(1)
	}
}

// Dropped returns the number of spans dropped because the internal buffer
// was full. Useful for health gauges.
func (e *OTLPHTTPExporter) Dropped() int64 {
	return e.dropped.Load()
}

// Shutdown signals the background goroutine to stop, waits for it to
// drain the remaining buffer (bounded by ctx), and returns ctx.Err() if
// the wait times out. Idempotent — a second call returns nil immediately.
func (e *OTLPHTTPExporter) Shutdown(ctx context.Context) error {
	e.once.Do(func() { close(e.done) })

	stopped := make(chan struct{})
	go func() {
		e.wg.Wait()
		close(stopped)
	}()
	select {
	case <-stopped:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// run is the background goroutine. It collects spans into a batch and
// flushes when the batch reaches BatchSize or FlushInterval elapses.
// On shutdown it flushes the remainder of the channel before exiting.
func (e *OTLPHTTPExporter) run() {
	defer e.wg.Done()

	ticker := time.NewTicker(e.cfg.FlushInterval)
	defer ticker.Stop()

	batch := make([]*Span, 0, e.cfg.BatchSize)

	flush := func() {
		if len(batch) == 0 {
			return
		}
		e.flush(batch)
		batch = batch[:0]
	}

	for {
		select {
		case s := <-e.ch:
			batch = append(batch, s)
			if len(batch) >= e.cfg.BatchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-e.done:
			// Drain whatever is left in the channel.
		drain:
			for {
				select {
				case s := <-e.ch:
					batch = append(batch, s)
				default:
					break drain
				}
			}
			flush()
			return
		}
	}
}

// flush marshals batch to OTLP/HTTP JSON and POSTs it to the collector.
// Non-2xx responses and transport errors are logged at Warn level; the
// batch is always discarded regardless (tracing export is best-effort).
func (e *OTLPHTTPExporter) flush(batch []*Span) {
	body, err := marshalOTLPJSON(e.cfg.ServiceName, batch)
	if err != nil {
		e.logWarn("otlp.marshal", slog.String("err", err.Error()))
		return
	}

	endpoint := strings.TrimRight(e.cfg.Endpoint, "/") + "/v1/traces"
	//nolint:noctx // flush is called from the background goroutine which has no incoming request context; using context.Background is correct here
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		e.logWarn("otlp.request", slog.String("err", err.Error()))
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.cfg.Client.Do(req)
	if err != nil {
		e.logWarn("otlp.post", slog.String("err", err.Error()))
		return
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		e.logWarn("otlp.status",
			slog.Int("status", resp.StatusCode),
			slog.Int("spans", len(batch)))
	}
}

func (e *OTLPHTTPExporter) logWarn(msg string, args ...any) {
	if e.cfg.Logger != nil {
		e.cfg.Logger.Warn(msg, args...)
	}
}

// ---------------------------------------------------------------------------
// OTLP/JSON wire shape
// ---------------------------------------------------------------------------
//
// The OTLP/JSON encoding differs from the protobuf binary encoding in one
// key way: trace IDs and span IDs are represented as lowercase hex strings
// (NOT base64). Times are decimal nanosecond strings (int64).
//
// Wire shape (ExportTraceServiceRequest):
//
//	{
//	  "resourceSpans": [ {
//	    "resource": { "attributes": [ {"key":"service.name","value":{"stringValue":"<svc>"}} ] },
//	    "scopeSpans": [ {
//	      "scope": { "name": "openccu-loom" },
//	      "spans": [ {
//	        "traceId": "<32-lowercase-hex>",
//	        "spanId":  "<16-lowercase-hex>",
//	        "parentSpanId": "<16-lowercase-hex>",   // omitted when root
//	        "name":    "<name>",
//	        "kind":    1,
//	        "startTimeUnixNano": "<decimal-int64-string>",
//	        "endTimeUnixNano":   "<decimal-int64-string>",
//	        "attributes": [ {"key":"k","value":{"stringValue":"v"}} ],
//	        "events":    [ {"timeUnixNano":"<str>","name":"...","attributes":[...]} ]
//	      } ]
//	    } ]
//	  } ]
//	}

// otlpAttribute is one OTLP key-value pair.
type otlpAttribute struct {
	Key   string         `json:"key"`
	Value map[string]any `json:"value"`
}

// otlpEvent is one span event.
type otlpEvent struct {
	TimeUnixNano string          `json:"timeUnixNano"`
	Name         string          `json:"name"`
	Attributes   []otlpAttribute `json:"attributes,omitempty"`
}

// otlpSpan is one OTLP span.
type otlpSpan struct {
	TraceID           string          `json:"traceId"`
	SpanID            string          `json:"spanId"`
	ParentSpanID      string          `json:"parentSpanId,omitempty"`
	Name              string          `json:"name"`
	Kind              int             `json:"kind"`
	StartTimeUnixNano string          `json:"startTimeUnixNano"`
	EndTimeUnixNano   string          `json:"endTimeUnixNano"`
	Attributes        []otlpAttribute `json:"attributes,omitempty"`
	Events            []otlpEvent     `json:"events,omitempty"`
}

type otlpScopeSpans struct {
	Scope struct {
		Name string `json:"name"`
	} `json:"scope"`
	Spans []otlpSpan `json:"spans"`
}

type otlpResource struct {
	Attributes []otlpAttribute `json:"attributes"`
}

type otlpResourceSpans struct {
	Resource   otlpResource     `json:"resource"`
	ScopeSpans []otlpScopeSpans `json:"scopeSpans"`
}

type otlpRequest struct {
	ResourceSpans []otlpResourceSpans `json:"resourceSpans"`
}

// marshalOTLPJSON encodes batch as an OTLP/HTTP JSON request body.
func marshalOTLPJSON(serviceName string, batch []*Span) ([]byte, error) {
	spans := make([]otlpSpan, 0, len(batch))
	for _, s := range batch {
		spans = append(spans, spanToOTLP(s))
	}

	var ss otlpScopeSpans
	ss.Scope.Name = "openccu-loom"
	ss.Spans = spans

	req := otlpRequest{
		ResourceSpans: []otlpResourceSpans{
			{
				Resource: otlpResource{
					Attributes: []otlpAttribute{
						{Key: "service.name", Value: map[string]any{"stringValue": serviceName}},
					},
				},
				ScopeSpans: []otlpScopeSpans{ss},
			},
		},
	}
	return json.Marshal(req)
}

// spanToOTLP converts one internal Span to the OTLP wire shape.
//
// ID-size notes:
//   - TraceID is a UUID string (36 chars with dashes). Strip dashes →
//     32 lowercase hex chars. If the result is not exactly 32 hex chars
//     (e.g. truncated by some test helper), left-pad or truncate to 32.
//   - SpanID is the first 8 chars of a UUID hex (8 hex chars). OTLP
//     spanId must be 16 hex chars → left-pad with '0' to 16.
//   - ParentSpanID follows the same rule; omit the field entirely when
//     ParentSpanID is "" (root span — json omitempty handles this).
func spanToOTLP(s *Span) otlpSpan {
	traceHex := traceIDToHex(s.TraceID)
	spanHex := spanIDToHex(s.SpanID)

	var parentHex string
	if s.ParentSpanID != "" {
		parentHex = spanIDToHex(s.ParentSpanID)
	}

	attrs := anyMapToOTLP(s.Attributes())
	events := eventsToOTLP(s.Events())

	return otlpSpan{
		TraceID:           traceHex,
		SpanID:            spanHex,
		ParentSpanID:      parentHex,
		Name:              s.Name,
		Kind:              1,
		StartTimeUnixNano: strconv.FormatInt(s.StartedAt.UnixNano(), 10),
		EndTimeUnixNano:   strconv.FormatInt(s.EndedAt.UnixNano(), 10),
		Attributes:        attrs,
		Events:            events,
	}
}

// traceIDToHex converts a UUID string (with or without dashes) to a
// 32-character lowercase hex string suitable for OTLP traceId.
// Non-hex residue is stripped; the result is zero-padded or truncated
// to exactly 32 chars.
func traceIDToHex(id string) string {
	// Strip dashes (UUID format: xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx).
	h := strings.ReplaceAll(id, "-", "")
	h = strings.ToLower(h)
	return padOrTrunc(h, 32)
}

// spanIDToHex converts an 8-char hex span ID to the 16-char lowercase
// hex string OTLP requires (left-pad with '0').
func spanIDToHex(id string) string {
	h := strings.ToLower(id)
	return padOrTrunc(h, 16)
}

// padOrTrunc left-pads s with '0' to length n, or truncates from the
// right when s is already longer than n.
func padOrTrunc(s string, n int) string {
	switch {
	case len(s) == n:
		return s
	case len(s) > n:
		return s[:n]
	default:
		return fmt.Sprintf("%0*s", n, s)
	}
}

// anyMapToOTLP converts a map[string]any to a slice of OTLP attributes.
// Type mapping: string→stringValue, bool→boolValue, int/int64→intValue
// (decimal string per OTLP spec), float64→doubleValue; all others fall
// back to fmt.Sprint → stringValue.
//
// Attribute keys are filtered through the same redaction predicate the
// log pipeline uses ([hmlog.IsSensitiveKey]): a span or event attribute
// keyed like a secret (e.g. "password", "token") is emitted with its
// value replaced by [hmlog.RedactMask], so tracing never becomes a side
// channel that bypasses the RedactingHandler.
func anyMapToOTLP(m map[string]any) []otlpAttribute {
	if len(m) == 0 {
		return nil
	}
	out := make([]otlpAttribute, 0, len(m))
	for k, v := range m {
		val := anyToOTLPValue(v)
		if hmlog.IsSensitiveKey(k) {
			val = map[string]any{"stringValue": hmlog.RedactMask}
		}
		out = append(out, otlpAttribute{Key: k, Value: val})
	}
	return out
}

func anyToOTLPValue(v any) map[string]any {
	switch t := v.(type) {
	case string:
		return map[string]any{"stringValue": t}
	case bool:
		return map[string]any{"boolValue": t}
	case int:
		return map[string]any{"intValue": strconv.FormatInt(int64(t), 10)}
	case int64:
		return map[string]any{"intValue": strconv.FormatInt(t, 10)}
	case float64:
		return map[string]any{"doubleValue": t}
	default:
		return map[string]any{"stringValue": fmt.Sprint(v)}
	}
}

// eventsToOTLP converts internal span events to the OTLP wire shape.
func eventsToOTLP(events []SpanEvent) []otlpEvent {
	if len(events) == 0 {
		return nil
	}
	out := make([]otlpEvent, len(events))
	for i, e := range events {
		out[i] = otlpEvent{
			TimeUnixNano: strconv.FormatInt(e.At.UnixNano(), 10),
			Name:         e.Name,
			Attributes:   anyMapToOTLP(e.Attributes),
		}
	}
	return out
}
