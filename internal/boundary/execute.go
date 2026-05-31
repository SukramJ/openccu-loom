// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package boundary

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"

	"github.com/SukramJ/openccu-loom/internal/clock"
	"github.com/SukramJ/openccu-loom/internal/metrics"
)

// Metrics bundles the counters/gauges Execute pings per call.
type Metrics struct {
	Count       *metrics.Counter // incremented on every call
	ErrorCount  *metrics.Counter // incremented on non-nil error
	PanicCount  *metrics.Counter // incremented on recovered panic
	LatencySecs *metrics.Gauge   // last-seen duration in seconds
}

// Config parametrises Execute. All fields are optional.
type Config struct {
	Name    string
	Logger  *slog.Logger
	Metrics Metrics

	// ReRaisePanic propagates recovered panics as errors. Default
	// is true; set to false in tests that intentionally panic.
	ReRaisePanic *bool

	// Clock is the time source for the elapsed-duration measurement.
	// Nil falls back to [clock.New] (real wall clock). Pass a
	// [clock.Fake] when latency assertions need to be deterministic.
	Clock clock.Clock
}

// ErrPanic is returned when a panic is caught and Config.ReRaisePanic
// is nil/true. Wraps the runtime value.
var ErrPanic = errors.New("boundary: recovered panic")

// Execute invokes fn with panic protection, structured logging, and
// metrics emission. Returns fn's error (or ErrPanic on recovery).
func Execute(ctx context.Context, cfg Config, fn func(context.Context) error) (err error) {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	clk := cfg.Clock
	if clk == nil {
		clk = clock.New()
	}
	if cfg.Metrics.Count != nil {
		cfg.Metrics.Count.Inc()
	}

	start := clk.Now()
	defer func() {
		if rec := recover(); rec != nil {
			if cfg.Metrics.PanicCount != nil {
				cfg.Metrics.PanicCount.Inc()
			}
			logger.LogAttrs(
				ctx, slog.LevelError, "boundary.panic",
				slog.String("name", cfg.Name),
				slog.String("value", fmt.Sprint(rec)),
				slog.String("stack", string(debug.Stack())),
			)
			reraise := cfg.ReRaisePanic == nil || *cfg.ReRaisePanic
			if reraise {
				err = fmt.Errorf("%w: %v", ErrPanic, rec)
			}
		}
		elapsed := clk.Now().Sub(start)
		if cfg.Metrics.LatencySecs != nil {
			cfg.Metrics.LatencySecs.Set(elapsed.Seconds())
		}
		if err != nil {
			if cfg.Metrics.ErrorCount != nil {
				cfg.Metrics.ErrorCount.Inc()
			}
			logger.LogAttrs(
				ctx, slog.LevelWarn, "boundary.error",
				slog.String("name", cfg.Name),
				slog.Duration("elapsed", elapsed),
				slog.String("err", err.Error()),
			)
			return
		}
		logger.LogAttrs(
			ctx, slog.LevelDebug, "boundary.ok",
			slog.String("name", cfg.Name),
			slog.Duration("elapsed", elapsed),
		)
	}()

	err = fn(ctx)
	return err
}

// ExecuteResult is Execute for functions that return (T, error).
//
// loom:reachable:reason="generic helper used by any boundary caller that needs to extract a typed result"
func ExecuteResult[T any](ctx context.Context, cfg Config, fn func(context.Context) (T, error)) (T, error) {
	var out T
	err := Execute(ctx, cfg, func(ctx context.Context) error {
		v, e := fn(ctx)
		if e != nil {
			return e
		}
		out = v
		return nil
	})
	return out, err
}
