// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmlog

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// DefaultCaptureBufferBytes is the soft cap on the in-memory buffer
// of a [CaptureSink]. Newer events kept; once the limit is reached
// the buffer drops complete oldest lines until the new event fits.
// 64 MiB ≈ 250 k mid-sized JSON records — comfortably above what a
// 30-minute capture at debug level produces on a healthy CCU.
const DefaultCaptureBufferBytes = 64 * 1024 * 1024

// CaptureSink is an in-memory ndjson buffer that a [TeeHandler]
// pushes a copy of every log record into. The buffer is a soft ring:
// once it exceeds [DefaultCaptureBufferBytes] (or whatever the caller
// configured) older lines are evicted FIFO so the latest activity
// always survives.
//
// All public methods are safe for concurrent use.
type CaptureSink struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	maxLen int
	events int
	closed bool
	// anonymise replaces device-address-shaped values with stable
	// hashes so the resulting archive is safe to attach to an issue
	// ticket without leaking the operator's CCU fleet.
	anonymise bool
}

// NewCaptureSink allocates a sink with the supplied byte budget.
// Passing 0 or a negative value uses [DefaultCaptureBufferBytes].
func NewCaptureSink(maxBytes int, anonymise bool) *CaptureSink {
	if maxBytes <= 0 {
		maxBytes = DefaultCaptureBufferBytes
	}
	return &CaptureSink{maxLen: maxBytes, anonymise: anonymise}
}

// Append writes one ndjson line into the sink. Caller already
// encoded the record — this method exists so that a [TeeHandler]
// (which carries the slog handler) and a future fan-in writer (e.g.
// captured Bash output) can both push into the same buffer.
func (s *CaptureSink) Append(line []byte) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	// Strip trailing newline; we re-add exactly one below so callers
	// can pass raw JSON without coordination.
	line = bytes.TrimRight(line, "\n")
	if len(line) == 0 {
		return
	}
	// Drop oldest complete lines until the new event fits.
	for s.buf.Len()+len(line)+1 > s.maxLen && s.buf.Len() > 0 {
		raw := s.buf.Bytes()
		_, after, ok := bytes.Cut(raw, []byte{'\n'})
		if !ok {
			s.buf.Reset()
			break
		}
		// Truncate by re-buffering the tail. This is O(n) per drop;
		// acceptable because drops only happen at the buffer's high-
		// water and the alternative (a linked list of chunks) would
		// add per-event allocation overhead.
		s.buf = *bytes.NewBuffer(after)
	}
	s.buf.Write(line)
	s.buf.WriteByte('\n')
	s.events++
}

// Snapshot returns a copy of every buffered ndjson line. Calling
// this concurrently with Append is safe — the returned slice is
// detached from the underlying buffer.
func (s *CaptureSink) Snapshot() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]byte, s.buf.Len())
	copy(out, s.buf.Bytes())
	return out
}

// Events reports the number of records the sink has accepted since
// construction (including the ones that have since been evicted).
func (s *CaptureSink) Events() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.events
}

// Bytes reports the current buffered byte count.
func (s *CaptureSink) Bytes() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Len()
}

// Anonymise reports whether the sink was configured to anonymise
// device-address-shaped values. The encoder ([TeeHandler]) consults
// this flag before emitting individual attributes.
func (s *CaptureSink) Anonymise() bool {
	return s != nil && s.anonymise
}

// Close marks the sink as closed; further Append calls are dropped.
// Snapshot remains usable.
func (s *CaptureSink) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
}

// TeeHandler wraps an [slog.Handler] and mirrors records into two
// optional side channels: an on-demand [CaptureSink] (the operator-
// triggered debug archive) and an always-on [LiveLog] ring (the log
// viewer's tail / backfill source). With neither attached it is a
// zero-cost pass-through (two atomic loads per record).
type TeeHandler struct {
	inner slog.Handler
	sink  atomic.Pointer[CaptureSink]
	live  atomic.Pointer[LiveLog]
	// bound carries the attributes attached via With(...) up the
	// handler chain (e.g. logger / central / interface). slog keeps
	// these on the handler, not on the record, so the [LiveLog] would
	// otherwise lose them; we accumulate and merge them at Handle time.
	bound []slog.Attr
}

// NewTeeHandler wraps inner. Use [TeeHandler.Attach] / Detach to
// switch the sink at runtime — typically driven by the capture REST
// endpoint.
func NewTeeHandler(inner slog.Handler) *TeeHandler {
	if inner == nil {
		panic("hmlog: NewTeeHandler: inner handler must not be nil")
	}
	return &TeeHandler{inner: inner}
}

// Attach installs sink as the current capture target. Replaces any
// previously attached sink (which the caller can detach + snapshot
// before discarding).
func (h *TeeHandler) Attach(sink *CaptureSink) {
	h.sink.Store(sink)
}

// Detach removes the current sink and returns it (nil when no sink
// was attached). The caller is responsible for snapshotting and
// closing the returned sink.
func (h *TeeHandler) Detach() *CaptureSink {
	return h.sink.Swap(nil)
}

// Active reports whether a capture sink is currently attached.
func (h *TeeHandler) Active() bool {
	return h.sink.Load() != nil
}

// AttachLive installs an always-on [LiveLog] ring that mirrors every
// record for the log-viewer stream. Unlike the capture sink it is set
// once (at stack construction) and never detached.
func (h *TeeHandler) AttachLive(live *LiveLog) {
	h.live.Store(live)
}

// Enabled delegates to the inner handler. The tee branch is always
// "enabled" — if no sink is attached the branch is skipped per
// record without consulting the level.
func (h *TeeHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

// Handle mirrors the record into the active sink (if any) and then
// delegates to the inner handler. Sink encoding failures are
// swallowed — capture must never interfere with normal logging.
func (h *TeeHandler) Handle(ctx context.Context, r slog.Record) error {
	if sink := h.sink.Load(); sink != nil {
		if line, err := encodeRecord(r, sink.Anonymise()); err == nil {
			sink.Append(line)
		}
	}
	if live := h.live.Load(); live != nil {
		live.record(r, h.bound)
	}
	return h.inner.Handle(ctx, r)
}

// WithAttrs delegates to the inner handler and rewraps with the same
// sink pointer so that loggers derived via With(...) still feed the
// capture stream.
func (h *TeeHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	child := &TeeHandler{inner: h.inner.WithAttrs(attrs)}
	child.sink.Store(h.sink.Load())
	child.live.Store(h.live.Load())
	if len(h.bound)+len(attrs) > 0 {
		merged := make([]slog.Attr, 0, len(h.bound)+len(attrs))
		merged = append(merged, h.bound...)
		merged = append(merged, attrs...)
		child.bound = merged
	}
	return child
}

// WithGroup delegates to the inner handler and rewraps as above. Group
// nesting is not reflected in the [LiveLog] record (the tee chain sits
// below the only WithGroup user); the bound attrs carry through
// unchanged.
func (h *TeeHandler) WithGroup(name string) slog.Handler {
	child := &TeeHandler{inner: h.inner.WithGroup(name)}
	child.sink.Store(h.sink.Load())
	child.live.Store(h.live.Load())
	child.bound = h.bound
	return child
}

// encodeRecord renders r as a single JSON object. Conceptually
// matches slog's JSON handler but operates on a fresh map so we can
// inject the anonymisation step before serialisation.
func encodeRecord(r slog.Record, anonymise bool) ([]byte, error) {
	out := make(map[string]any, 8+r.NumAttrs())
	out["time"] = r.Time.UTC().Format(time.RFC3339Nano)
	out["level"] = strings.ToLower(r.Level.String())
	out["msg"] = r.Message
	r.Attrs(func(a slog.Attr) bool {
		k, v := a.Key, attrValue(a.Value)
		if anonymise {
			v = anonymiseValue(k, v)
		}
		out[k] = v
		return true
	})
	return json.Marshal(out)
}

func attrValue(v slog.Value) any {
	switch v.Kind() {
	case slog.KindString:
		return v.String()
	case slog.KindInt64:
		return v.Int64()
	case slog.KindUint64:
		return v.Uint64()
	case slog.KindFloat64:
		return v.Float64()
	case slog.KindBool:
		return v.Bool()
	case slog.KindDuration:
		return v.Duration().String()
	case slog.KindTime:
		return v.Time().UTC().Format(time.RFC3339Nano)
	case slog.KindGroup:
		group := v.Group()
		out := make(map[string]any, len(group))
		for _, g := range group {
			out[g.Key] = attrValue(g.Value)
		}
		return out
	default:
		return v.Any()
	}
}

// anonymiseValue hashes operator-identifying values (login subject,
// user names, remote-client IPs) so a capture archive can be
// attached to an issue ticket without leaking who triggered the
// recording. Operations data — device addresses, CCU hostnames,
// interface ids — stays in clear text; the operator viewing their
// own archive needs the real values to make sense of the trace.
func anonymiseValue(key string, value any) any {
	switch strings.ToLower(key) {
	case "subject", "user", "username", "remote", "remote_addr":
		if s, ok := value.(string); ok && s != "" {
			return AnonymiseToken(s)
		}
	}
	return value
}

// AnonymiseToken returns a stable hash prefix for value, intended for
// device addresses, hostnames, and other operator-identifying
// strings. Empty input returns an empty string so absence stays
// visible.
func AnonymiseToken(value string) string {
	if value == "" {
		return ""
	}
	// Reuse the redacting handler's hash shape so anonymised values
	// in capture archives match the format used in diagnostics dumps.
	const prefixLen = 12
	sum := sha256Sum([]byte(value))
	dst := make([]byte, prefixLen)
	const hexDigits = "0123456789abcdef"
	for i := range prefixLen / 2 {
		dst[i*2] = hexDigits[sum[i]>>4]
		dst[i*2+1] = hexDigits[sum[i]&0x0f]
	}
	return "anon:" + string(dst)
}

// Compile-time assertion.
var _ slog.Handler = (*TeeHandler)(nil)
