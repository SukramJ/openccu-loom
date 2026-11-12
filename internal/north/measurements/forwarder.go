// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package measurements

import (
	"context"
	"log/slog"
	"path"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/wiring"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// Defaults used when the operator did not override the corresponding
// config knob. Kept here so the daemon wiring and tests share one source.
const (
	// DefaultFlushInterval is an exporter's batch-flush cadence. 5s
	// keeps the external store near-live while amortising its writes
	// across a burst of sensor pushes.
	DefaultFlushInterval = 5 * time.Second
	// DefaultMaxBuffer bounds an exporter's in-memory sample buffer
	// between flushes. On overflow the oldest sample is dropped so the
	// event handler never blocks and the daemon never runs out of memory.
	DefaultMaxBuffer = 8192
)

// liveSourced is the subset of the wire data-point surface the forwarder
// needs to apply the provenance guard. generic.DataPoint satisfies it.
type liveSourced interface {
	Source() hmenum.ValueSource
}

// Forwarder hands numeric measurements to an Exporter. Construct it with
// New, then Wire it against the central registry. Multi-CCU safe: every
// sample is tagged with its central name and canonical interface id.
type Forwarder struct {
	exporter   Exporter
	enabledFor func(centralName string) bool
	include    []string
	exclude    []string
	overrides  *RecordingOverrides
	logger     *slog.Logger

	forwarded atomic.Int64
}

// Options configures a Forwarder.
type Options struct {
	// EnabledFor reports whether a given central is forwarded. Nil
	// forwards every central.
	EnabledFor func(centralName string) bool
	// Include / Exclude are parameter-name globs (path.Match syntax).
	Include []string
	Exclude []string
	// Overrides, when non-nil, is the per-data-point recording overlay
	// consulted on the hot path: a present override wins over the
	// Include/Exclude glob policy. Nil means "glob policy only".
	Overrides *RecordingOverrides
	Logger    *slog.Logger
}

// New returns a Forwarder that feeds exporter. A nil exporter yields a
// Forwarder whose Wire is a no-op, so callers can wire unconditionally.
func New(exporter Exporter, opts Options) *Forwarder {
	f := &Forwarder{
		exporter:   exporter,
		enabledFor: opts.EnabledFor,
		include:    opts.Include,
		exclude:    opts.Exclude,
		overrides:  opts.Overrides,
		logger:     opts.Logger,
	}
	if f.enabledFor == nil {
		f.enabledFor = func(string) bool { return true }
	}
	if f.logger == nil {
		f.logger = slog.Default()
	}
	return f
}

// Metrics is a point-in-time snapshot of the forwarder's own counters.
type Metrics struct {
	// Forwarded counts the samples handed to the exporter. What the
	// exporter then delivered, dropped or failed on is its own count.
	Forwarded int64
}

// Metrics returns the forwarder's cumulative counters since start.
func (f *Forwarder) Metrics() Metrics {
	if f == nil {
		return Metrics{}
	}
	return Metrics{Forwarded: f.forwarded.Load()}
}

// Wire subscribes the forwarder to every enabled central's EventBus — the
// centrals registered now and every one registered later. The returned
// closer unsubscribes and shuts the exporter down, which flushes its
// buffered tail, so no accepted sample is lost on a graceful stop. Safe
// to call on a nil forwarder or one without an exporter (returns a no-op
// closer).
func (f *Forwarder) Wire(reg *central.Registry) func() {
	if f == nil || f.exporter == nil || reg == nil {
		return func() {}
	}
	removeObserver := reg.OnRegisterDeclared(wiring.Seam{
		Name:         "measurements.forwarder",
		Collaborator: "*measurements.Forwarder",
		Phase:        wiring.PhasePerCentral,
		Why:          "no value change is ever forwarded, so the external time-series store receives nothing while the daemon reports the export as enabled",
	}, f.WireCentral)
	var once sync.Once
	return func() {
		once.Do(func() {
			// Stop new samples first so the exporter's final flush
			// drains a quiet buffer.
			removeObserver()
			shutCtx, cancelShut := context.WithTimeout(context.Background(), 5*time.Second)
			_ = f.exporter.Shutdown(shutCtx)
			cancelShut()
		})
	}
}

// WireCentral subscribes the forwarder to one central's EventBus and
// returns the unsubscribe (nil when the central carries no bus / model,
// or when forwarding is not enabled for it). It is the observer the
// registry runs per central, so a central adopted at runtime is
// forwarded like one present at boot.
func (f *Forwarder) WireCentral(unit *central.Unit) func() {
	if f == nil || f.exporter == nil || unit == nil {
		return nil
	}
	if unit.EventBus == nil || unit.ModelRegistry == nil {
		return nil
	}
	if !f.enabledFor(unit.Name()) {
		return nil
	}
	u := unit
	return u.EventBus.Subscribe(func(e hmevent.DataPointValueChangedEvent) {
		f.onValueChanged(u, e)
	})
}

// onValueChanged is the EventBus handler. It runs on the publisher's
// goroutine, so it must not block: it does a cheap filter, a registry
// lookup for the provenance guard, and a non-blocking hand-over.
func (f *Forwarder) onValueChanged(unit *central.Unit, e hmevent.DataPointValueChangedEvent) {
	if e.Key.ParamsetKey != hmenum.ParamsetKeyValues {
		return
	}
	val, ok := numericValue(e.NewValue)
	if !ok {
		return
	}
	if f.policy(unit.Name(), e.Key.InterfaceID, e.Key.ChannelAddress, e.Key.Parameter) != "" {
		return
	}
	dev, ok := unit.ModelRegistry.Get(hmtypes.DeviceAddress(e.Key.ChannelAddress))
	if !ok || dev == nil {
		return
	}
	// Provenance guard: forward only a genuine live wire observation.
	// The boot-time zero default, a cache replay and a source-only flip
	// are not measurements.
	if provenance(dev.DataPoint(e.Key.ChannelAddress, hmenum.Parameter(e.Key.Parameter))) != "" {
		return
	}
	f.exporter.Export(Sample{
		CentralName:    unit.Name(),
		InterfaceID:    e.Key.InterfaceID,
		ChannelAddress: e.Key.ChannelAddress,
		Parameter:      e.Key.Parameter,
		TS:             e.Timestamp(),
		Value:          val,
	})
	f.forwarded.Add(1)
}

// WithheldReason names the rule by which the forwarder leaves a data
// point's value out. The set is closed.
type WithheldReason string

const (
	// WithheldNotNumeric means: the value is not a number; a switch
	// state or a text is not a measurement.
	WithheldNotNumeric WithheldReason = "not_numeric"
	// WithheldPatternExcludes means: the include / exclude patterns of
	// north.measurements leave the parameter out.
	WithheldPatternExcludes WithheldReason = "pattern_excludes"
	// WithheldOverrideOff means: the operator switched recording off for
	// this data point.
	WithheldOverrideOff WithheldReason = "override_off"
	// WithheldNoLiveValue means: the daemon holds no value it observed on
	// the wire since it started. It passes with the next value.
	WithheldNoLiveValue WithheldReason = "no_live_value"
)

// policy is the recording decision for one data point: an override wins
// over the patterns. "" forwards.
func (f *Forwarder) policy(centralName, iface, channel, parameter string) WithheldReason {
	if record, ok := f.overrides.lookup(centralName, iface, channel, parameter); ok {
		if record {
			return ""
		}
		return WithheldOverrideOff
	}
	if allow(parameter, f.include, f.exclude) {
		return ""
	}
	return WithheldPatternExcludes
}

// provenance is the guard on where a data point's value came from. "" is
// a value observed live. dp is any because the model's data points share
// no type that carries the source.
func provenance(dp any) WithheldReason {
	src, ok := dp.(liveSourced)
	if !ok || src.Source() != hmenum.ValueSourceLive {
		return WithheldNoLiveValue
	}
	return ""
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
