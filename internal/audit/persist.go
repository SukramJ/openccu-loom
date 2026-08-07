// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package audit

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SukramJ/openccu-loom/internal/clock"
)

// SinkFunc is the persistence side of an audit recorder. It returns
// nil on success; errors are logged but never propagate to the
// producer — the audit path must not block domain operations.
type SinkFunc func(ctx context.Context, entry Entry) error

// PersistedRecorder layers durable persistence on top of an in-memory
// [Buffer]. Writes go to both: the sink is the audit-trail of record and
// backs `GET /api/v1/audit` whenever a durable store is wired, while the
// buffer serves that endpoint's fallback path and the MCP tool. The two
// sides assign [Entry.ID] independently — the buffer receives a copy, so
// its sequence never reaches the store's primary key.
//
// The persistence call runs synchronously by default; pass an
// async-friendly Sink (e.g. one that goroutines its work) when
// blocking the producer is unacceptable.
type PersistedRecorder struct {
	buf    *Buffer
	sink   SinkFunc
	logger *slog.Logger
	clk    clock.Clock
}

// NewPersistedRecorder wires a buffer + sink combination. Either may
// be nil (the sink-only or buffer-only configurations are valid).
// Timestamps default to the real wall clock.
func NewPersistedRecorder(buf *Buffer, sink SinkFunc, logger *slog.Logger) *PersistedRecorder {
	return NewPersistedRecorderWithClock(buf, sink, logger, clock.New())
}

// NewPersistedRecorderWithClock is the test seam: pass a [clock.Fake]
// to make timestamps deterministic. A nil clk falls back to
// [clock.New].
func NewPersistedRecorderWithClock(buf *Buffer, sink SinkFunc, logger *slog.Logger, clk clock.Clock) *PersistedRecorder {
	if logger == nil {
		logger = slog.Default()
	}
	if clk == nil {
		clk = clock.New()
	}
	return &PersistedRecorder{buf: buf, sink: sink, logger: logger, clk: clk}
}

// Record stamps the entry's timestamp (if zero) and forwards to the
// buffer + sink. Sink errors are logged with slog at warn level so a
// down database does not silently swallow the audit trail.
func (r *PersistedRecorder) Record(entry Entry) {
	if entry.Timestamp.IsZero() {
		entry.Timestamp = r.clk.Now().UTC()
	}
	if r.buf != nil {
		r.buf.Record(entry)
	}
	if r.sink == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), persistTimeout)
	defer cancel()
	if err := r.sink(ctx, entry); err != nil {
		r.logger.Warn(
			"audit.persist",
			slog.String("action", string(entry.Action)),
			slog.String("device", entry.DeviceAddress),
			slog.String("err", err.Error()),
		)
	}
}

// List delegates to the buffer (fast in-memory snapshot). Persistent
// reads go directly through the [SinkFunc]'s owner — the buffer is
// authoritative for the live dashboard.
func (r *PersistedRecorder) List(limit int) []Entry {
	if r.buf == nil {
		return nil
	}
	return r.buf.List(limit)
}

// persistTimeout caps the audit-write latency. Hitting the timeout
// drops the audit row but keeps the buffered copy.
const persistTimeout = 5 * time.Second

// ErrAuditOverflow is returned by the durable sink when a producer's
// per-call block deadline expired before queue capacity opened up.
// Producers should treat this as "audit-row-not-persisted" and decide
// whether to retry, drop, or escalate. SPEC §13 ("append-only change-
// log") is violated when callers ignore the error.
var ErrAuditOverflow = errors.New("audit: persistence queue full")

// DurableSinkStats surfaces overflow / latency telemetry from
// [NewDurableSink]. The pointer is returned alongside the sink so
// admin and health endpoints can read it without touching the
// goroutine that owns the queue.
type DurableSinkStats struct {
	enqueued   *atomic.Uint64
	dropped    *atomic.Uint64
	sinkErrors *atomic.Uint64
}

// Enqueued returns the cumulative number of audit entries that
// successfully landed in the queue.
func (s *DurableSinkStats) Enqueued() uint64 {
	if s == nil || s.enqueued == nil {
		return 0
	}
	return s.enqueued.Load()
}

// Dropped returns the cumulative number of entries rejected with
// [ErrAuditOverflow] (or ctx-cancelled) because the producer-side
// block deadline expired.
func (s *DurableSinkStats) Dropped() uint64 {
	if s == nil || s.dropped == nil {
		return 0
	}
	return s.dropped.Load()
}

// SinkErrors returns the cumulative number of persistence errors
// returned by the wrapped sink AFTER enqueue. Non-zero indicates
// database trouble that the in-memory buffer masked from producers.
func (s *DurableSinkStats) SinkErrors() uint64 {
	if s == nil || s.sinkErrors == nil {
		return 0
	}
	return s.sinkErrors.Load()
}

// DurableSinkOptions parametrises [NewDurableSink].
type DurableSinkOptions struct {
	// Capacity is the in-flight queue depth. Recommended: 4× the
	// expected steady-state Record rate per second.
	Capacity int
	// BlockTimeout caps how long a producer's Record waits when the
	// queue is full. Zero blocks indefinitely (matches SPEC's
	// append-only contract — audit must not be silently dropped).
	// Negative falls back to drop-on-full (legacy compatibility).
	BlockTimeout time.Duration
	// Logger receives one warn record per non-zero overflow burst.
	Logger *slog.Logger
}

// NewDurableSink wraps sink in a background goroutine with a bounded
// queue, surfacing overflows as [ErrAuditOverflow] instead of silent
// drops. Closes audit R11: the previous [AsyncSink] dropped under
// pressure with only a once-per-process slog warning, breaking the
// SPEC §13 "append-only" guarantee. The durable sink converts
// overflow into a typed error the producer can act on.
//
// Returns the sink function, a pointer to live stats, and a stop
// closure that flushes the queue and joins the worker goroutine.
//
//nolint:gocritic // unnamedResult: same closure pattern as AsyncSink
func NewDurableSink(sink SinkFunc, opts DurableSinkOptions) (SinkFunc, *DurableSinkStats, func()) {
	if sink == nil {
		return nil, &DurableSinkStats{}, func() {}
	}
	capacity := opts.Capacity
	if capacity < 1 {
		capacity = 64
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	ch := make(chan Entry, capacity)
	stop := make(chan struct{})
	done := make(chan struct{})

	stats := &DurableSinkStats{
		enqueued:   &atomic.Uint64{},
		dropped:    &atomic.Uint64{},
		sinkErrors: &atomic.Uint64{},
	}
	stEnq := stats.enqueued
	stDrop := stats.dropped
	stErr := stats.sinkErrors

	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				// Drain remaining entries before exiting so an
				// orderly shutdown does not lose the tail of the
				// queue. The wrapped sink's own ctx-handling caps
				// the total drain time.
				for {
					select {
					case e := <-ch:
						ctx, cancel := context.WithTimeout(context.Background(), persistTimeout)
						if err := sink(ctx, e); err != nil {
							stErr.Add(1)
							logger.Warn("audit.durable_sink.drain",
								slog.String("action", string(e.Action)),
								slog.String("err", err.Error()))
						}
						cancel()
					default:
						return
					}
				}
			case e := <-ch:
				ctx, cancel := context.WithTimeout(context.Background(), persistTimeout)
				if err := sink(ctx, e); err != nil {
					stErr.Add(1)
					logger.Warn("audit.durable_sink",
						slog.String("action", string(e.Action)),
						slog.String("err", err.Error()))
				}
				cancel()
			}
		}
	}()

	enqueue := func(ctx context.Context, e Entry) error {
		// Fast path: queue has slack — never wait.
		select {
		case ch <- e:
			stEnq.Add(1)
			return nil
		default:
		}
		// Slow path: queue saturated. Honour the configured block
		// policy.
		switch {
		case opts.BlockTimeout < 0:
			// Legacy behaviour — fail fast.
			stDrop.Add(1)
			return ErrAuditOverflow
		case opts.BlockTimeout == 0:
			// SPEC default: block until the queue accepts. Producer
			// ctx cancel still aborts.
			select {
			case ch <- e:
				stEnq.Add(1)
				return nil
			case <-ctx.Done():
				stDrop.Add(1)
				return ctx.Err()
			}
		default:
			t := time.NewTimer(opts.BlockTimeout)
			defer t.Stop()
			select {
			case ch <- e:
				stEnq.Add(1)
				return nil
			case <-t.C:
				stDrop.Add(1)
				return ErrAuditOverflow
			case <-ctx.Done():
				stDrop.Add(1)
				return ctx.Err()
			}
		}
	}
	closer := func() {
		close(stop)
		<-done
	}
	return enqueue, stats, closer
}

// AsyncSink is retained for legacy callers (drop-on-full semantics).
// New code should prefer [NewDurableSink], which surfaces overflow as
// a typed error and exposes telemetry. SPEC §13 expects audit to be
// append-only; the durable variant honours that contract, this one
// does not.
//
// Drops are logged once per drop_window so a fully-saturated database
// does not flood the logs.
// gocritic unnamedResult: the second return is the canonical "stop"
// closure pattern; naming it would shadow the local `stop` channel
// declared inside the body.
//
//nolint:gocritic // see comment above
func AsyncSink(sink SinkFunc, capacity int, logger *slog.Logger) (SinkFunc, func()) {
	if sink == nil {
		return nil, func() {}
	}
	if capacity < 1 {
		capacity = 64
	}
	if logger == nil {
		logger = slog.Default()
	}
	ch := make(chan Entry, capacity)
	stop := make(chan struct{})
	done := make(chan struct{})
	var dropOnce sync.Once
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			case e := <-ch:
				ctx, cancel := context.WithTimeout(context.Background(), persistTimeout)
				if err := sink(ctx, e); err != nil {
					logger.Warn(
						"audit.async_sink",
						slog.String("action", string(e.Action)),
						slog.String("err", err.Error()),
					)
				}
				cancel()
			}
		}
	}()
	enqueue := func(_ context.Context, e Entry) error {
		select {
		case ch <- e:
		default:
			dropOnce.Do(func() {
				logger.Warn("audit.async_sink.queue_full", slog.Int("capacity", capacity))
			})
		}
		return nil
	}
	closer := func() {
		close(stop)
		<-done
	}
	return enqueue, closer
}
