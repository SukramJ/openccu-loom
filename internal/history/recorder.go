// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package history

import (
	"context"
	"log/slog"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// Defaults used when the operator did not override the corresponding
// config knob. Kept here so the daemon wiring and tests share one source.
const (
	// DefaultFlushInterval is the recorder's batch-flush cadence. 5s
	// keeps a SPA chart near-live while still amortising SQLite writes
	// across a burst of sensor pushes.
	DefaultFlushInterval = 5 * time.Second

	// DefaultRetention is how long raw samples are kept by default
	// (30 days). Tuned for the short-to-medium-term SPA charting goal;
	// long-term archival is the exporter's job.
	DefaultRetention = 720 * time.Hour

	// DefaultMaxBuffer bounds the in-memory sample buffer between
	// flushes. On overflow the oldest sample is dropped so the event
	// handler never blocks and the daemon never OOMs.
	DefaultMaxBuffer = 8192

	// retentionInterval is how often the retention purge runs. Hourly is
	// frequent enough for a day-to-week window and cheap on a single
	// indexed DELETE.
	retentionInterval = time.Hour
)

// liveSourced is the subset of the wire data-point surface the recorder
// needs to apply the provenance guard. generic.DataPoint satisfies it.
type liveSourced interface {
	Source() hmenum.ValueSource
}

// Recorder captures numeric measurement history. Construct it with New,
// then Wire it against the central registry. Multi-CCU safe: every
// sample is tagged with its central name and canonical interface id.
type Recorder struct {
	store         *sqlite.MeasurementStore
	exporter      MeasurementExporter
	enabledFor    func(centralName string) bool
	include       []string
	exclude       []string
	flushInterval time.Duration
	retention     time.Duration
	maxBuffer     int
	logger        *slog.Logger

	mu  sync.Mutex
	buf []sqlite.MeasurementSample

	dropped  atomic.Int64
	recorded atomic.Int64
}

// Options configures a Recorder. The zero value of each field falls back
// to the package default.
type Options struct {
	// EnabledFor reports whether a given central should be recorded.
	// Nil records every central.
	EnabledFor func(centralName string) bool
	// Include / Exclude are parameter-name globs (path.Match syntax).
	Include []string
	Exclude []string
	// FlushInterval, Retention, MaxBuffer override the defaults when > 0.
	FlushInterval time.Duration
	Retention     time.Duration
	MaxBuffer     int
	Logger        *slog.Logger
	// Exporter, when non-nil, receives every recorded sample for
	// forwarding to an external time-series store. Optional.
	Exporter MeasurementExporter
}

// New returns a Recorder backed by store. A nil store yields a Recorder
// whose Wire is a no-op, so callers can wire unconditionally.
func New(store *sqlite.MeasurementStore, opts Options) *Recorder {
	r := &Recorder{
		store:         store,
		exporter:      opts.Exporter,
		enabledFor:    opts.EnabledFor,
		include:       opts.Include,
		exclude:       opts.Exclude,
		flushInterval: opts.FlushInterval,
		retention:     opts.Retention,
		maxBuffer:     opts.MaxBuffer,
		logger:        opts.Logger,
	}
	if r.enabledFor == nil {
		r.enabledFor = func(string) bool { return true }
	}
	if r.flushInterval <= 0 {
		r.flushInterval = DefaultFlushInterval
	}
	if r.retention <= 0 {
		r.retention = DefaultRetention
	}
	if r.maxBuffer <= 0 {
		r.maxBuffer = DefaultMaxBuffer
	}
	if r.logger == nil {
		r.logger = slog.Default()
	}
	return r
}

// Metrics is a point-in-time snapshot of the recorder's own counters.
type Metrics struct {
	Recorded int64
	Dropped  int64
}

// Metrics returns the recorder's cumulative counters since start.
func (r *Recorder) Metrics() Metrics {
	if r == nil {
		return Metrics{}
	}
	return Metrics{Recorded: r.recorded.Load(), Dropped: r.dropped.Load()}
}

// Wire subscribes the recorder to every enabled central's EventBus and
// starts the background flush + retention loop. The returned closer
// unsubscribes, stops the loop, and runs one final flush so no buffered
// sample is lost on a graceful stop. Safe to call on a nil/disabled
// recorder (returns a no-op closer).
func (r *Recorder) Wire(reg *central.Registry) func() {
	if r == nil || r.store == nil || reg == nil {
		return func() {}
	}

	var unsubs []func()
	for _, unit := range reg.List() {
		if unit == nil || unit.EventBus == nil || unit.ModelRegistry == nil {
			continue
		}
		if !r.enabledFor(unit.Name()) {
			continue
		}
		u := unit
		unsub := events.Subscribe(u.EventBus, func(e hmevent.DataPointValueChangedEvent) {
			r.onValueChanged(u, e)
		})
		unsubs = append(unsubs, unsub)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go r.loop(ctx, done)

	var once sync.Once
	return func() {
		once.Do(func() {
			// Stop new samples first so the final flush drains a quiet
			// buffer, then stop the loop and wait for the final flush.
			for _, u := range unsubs {
				u()
			}
			cancel()
			<-done
			if r.exporter != nil {
				shutCtx, cancelShut := context.WithTimeout(context.Background(), 5*time.Second)
				_ = r.exporter.Shutdown(shutCtx)
				cancelShut()
			}
		})
	}
}

// loop runs the periodic batch flush and the retention purge until ctx
// is cancelled, then performs one final flush.
func (r *Recorder) loop(ctx context.Context, done chan<- struct{}) {
	defer close(done)
	flushTicker := time.NewTicker(r.flushInterval)
	defer flushTicker.Stop()
	retentionTicker := time.NewTicker(retentionInterval)
	defer retentionTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Shutdown: flush the last buffered samples on a fresh ctx —
			// the loop ctx is already cancelled, but the final write must
			// still land.
			//nolint:contextcheck // shutdown flush must not inherit the cancelled loop ctx
			r.flush(context.Background())
			return
		case <-flushTicker.C:
			r.flush(ctx)
		case <-retentionTicker.C:
			r.purge(ctx)
		}
	}
}

// onValueChanged is the EventBus handler. It runs synchronously on the
// publisher goroutine, so it must stay cheap: a few map lookups + an
// enqueue, never disk I/O.
func (r *Recorder) onValueChanged(unit *central.Unit, e hmevent.DataPointValueChangedEvent) {
	// Only runtime VALUES carry measurements.
	if e.Key.ParamsetKey != hmenum.ParamsetKeyValues {
		return
	}
	// Numeric-only: a real 0 is kept, but booleans/strings/enums are not
	// measurements. The provenance guard below handles the boot-time
	// pseudo-value; the value magnitude is never a filter.
	val, ok := numericValue(e.NewValue)
	if !ok {
		return
	}
	if !allow(e.Key.Parameter, r.include, r.exclude) {
		return
	}
	// Provenance guard (ADR 0040): record only genuine live observations.
	// Look up the live DP and require ValueSource == live, which excludes
	// the unobserved zero default (unobserved), a value replayed at boot
	// (cache), and a frozen value after connection loss (stale).
	dev, ok := unit.ModelRegistry.Get(deviceAddressOf(e.Key.ChannelAddress))
	if !ok || dev == nil {
		return
	}
	dp := dev.DataPoint(e.Key.ChannelAddress, hmenum.Parameter(e.Key.Parameter))
	src, ok := dp.(liveSourced)
	if !ok || src.Source() != hmenum.ValueSourceLive {
		return
	}

	sample := sqlite.MeasurementSample{
		CentralName:    unit.Name(),
		InterfaceID:    e.Key.InterfaceID,
		ChannelAddress: e.Key.ChannelAddress,
		Parameter:      e.Key.Parameter,
		TS:             e.Timestamp(),
		Value:          val,
	}
	r.enqueue(sample)
	if r.exporter != nil {
		// Non-blocking by contract; the exporter buffers internally.
		r.exporter.Export(sample)
	}
}

// enqueue appends a sample, dropping the oldest when the buffer is full
// so the caller (the bus handler) never blocks.
func (r *Recorder) enqueue(s sqlite.MeasurementSample) {
	r.mu.Lock()
	if len(r.buf) >= r.maxBuffer {
		// Drop oldest: shift down by one. Only happens when a stalled
		// disk lets the buffer fill, so the O(n) shift is not a hot path.
		copy(r.buf, r.buf[1:])
		r.buf = r.buf[:len(r.buf)-1]
		r.dropped.Add(1)
	}
	r.buf = append(r.buf, s)
	r.mu.Unlock()
}

// drain swaps out the current buffer for an empty one and returns the
// captured samples.
func (r *Recorder) drain() []sqlite.MeasurementSample {
	r.mu.Lock()
	if len(r.buf) == 0 {
		r.mu.Unlock()
		return nil
	}
	out := r.buf
	r.buf = make([]sqlite.MeasurementSample, 0, len(out))
	r.mu.Unlock()
	return out
}

// Flush writes any buffered samples immediately. The background loop
// calls the same path on its ticker; exposed so callers (and tests) can
// drain synchronously.
func (r *Recorder) Flush(ctx context.Context) {
	if r == nil {
		return
	}
	r.flush(ctx)
}

// flush writes the buffered samples in one batch. On error the samples
// are lost (history is best-effort, never a source of backpressure).
func (r *Recorder) flush(ctx context.Context) {
	batch := r.drain()
	if len(batch) == 0 {
		return
	}
	if err := r.store.SaveBatch(ctx, batch); err != nil {
		r.logger.Warn("history.flush_err",
			slog.Int("samples", len(batch)),
			slog.String("err", err.Error()))
		return
	}
	r.recorded.Add(int64(len(batch)))
}

// purge drops samples older than the retention window.
func (r *Recorder) purge(ctx context.Context) {
	if r.retention <= 0 {
		return
	}
	cutoff := time.Now().Add(-r.retention)
	n, err := r.store.DeleteOlderThan(ctx, cutoff)
	if err != nil {
		r.logger.Warn("history.purge_err", slog.String("err", err.Error()))
		return
	}
	if n > 0 {
		r.logger.Debug("history.purged", slog.Int64("rows", n))
	}
}

// numericValue extracts a float64 from a numeric ParamValue. Bool,
// string, list and none are not measurements and return ok=false.
func numericValue(v hmtypes.ParamValue) (float64, bool) {
	switch v.Kind {
	case hmtypes.ValueKindInt:
		return float64(v.Int), true
	case hmtypes.ValueKindFloat:
		return v.Float, true
	default:
		return 0, false
	}
}

// allow reports whether a parameter name passes the include/exclude
// globs. Exclude always wins; an empty include list allows everything.
func allow(parameter string, include, exclude []string) bool {
	for _, pat := range exclude {
		if globMatch(pat, parameter) {
			return false
		}
	}
	if len(include) == 0 {
		return true
	}
	for _, pat := range include {
		if globMatch(pat, parameter) {
			return true
		}
	}
	return false
}

// globMatch matches a parameter name against a path.Match glob. Parameter
// names contain no "/", so "*" matches any run of characters. A malformed
// pattern never matches.
func globMatch(pattern, name string) bool {
	ok, err := path.Match(pattern, name)
	return err == nil && ok
}

// deviceAddressOf returns the device part of a channel address
// ("ABC123:4" -> "ABC123"). Device addresses never contain a colon; the
// colon separates the channel number.
func deviceAddressOf(channelAddress string) string {
	addr, _, _ := strings.Cut(channelAddress, ":")
	return addr
}
