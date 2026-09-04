// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package client

import (
	"context"
	"errors"
	"math"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// DPKValue pairs a DataPointKey with the value the caller expects to see
// confirmed by the CCU.
type DPKValue struct {
	Key   hmtypes.DataPointKey
	Value any
}

// ErrStateChangeTimeout is returned by [WaitForStateChangeOrTimeout] when one
// or more of the requested keys did not see a matching CCU confirmation
// within the wait window.
var ErrStateChangeTimeout = errors.New("client: timeout waiting for CCU state-change callback")

// WaitForStateChangeOrTimeout subscribes to
// [hmevent.DataPointValueChangedEvent] on the given bus and blocks
// until every (key, value) pair in `dpkValues` has been confirmed —
// i.e. an event arrived whose Key matches and whose NewValue equals
// the expected value (with fuzzy float comparison rounded to 2
// Decimal places. Returns nil on
// successful confirmation of every entry; returns
// [ErrStateChangeTimeout] when the deadline elapses before the
// confirmation set is complete; returns the context error when the
// caller's context is canceled.
//
// `wait_for_state_change_or_timeout(device, dpk_values, wait_for_callback)`
// (`client/state_change.py:34-49`). The Go signature drops the
// `device` argument because the bus subscription is by Key
// (the bus already routes events by key); resolution of the key to a
// DataPoint happens at the caller's level when needed.
//
// Empty `dpkValues` returns nil immediately. A timeout of 0 falls
// Back to a 60-second default — mirrors
// `wait_for_callback` parameter when callers pass None.
func WaitForStateChangeOrTimeout(
	ctx context.Context,
	bus *events.Bus,
	dpkValues []DPKValue,
	timeout time.Duration,
) error {
	if len(dpkValues) == 0 {
		return nil
	}
	if bus == nil {
		return errors.New("client.WaitForStateChangeOrTimeout: bus is required")
	}
	if timeout <= 0 {
		timeout = 60 * time.Second
	}

	// Use a single waitgroup-style counter — each unique key starts
	// pending and decrements on confirmation. We hold a mutex to
	// avoid races between the handler firing and the goroutine
	// finishing setup.
	var (
		mu      sync.Mutex
		pending = make(map[hmtypes.DataPointKey]any, len(dpkValues))
		done    = make(chan struct{})
	)
	for _, p := range dpkValues {
		pending[p.Key] = p.Value
	}

	var once sync.Once
	closeDone := func() { once.Do(func() { close(done) }) }

	unsub := events.Subscribe(bus, func(ev hmevent.DataPointValueChangedEvent) {
		mu.Lock()
		expected, ok := pending[ev.Key]
		if !ok {
			mu.Unlock()
			return
		}
		if !valuesMatch(expected, ev.NewValue.Unwrap()) {
			mu.Unlock()
			return
		}
		delete(pending, ev.Key)
		empty := len(pending) == 0
		mu.Unlock()
		if empty {
			closeDone()
		}
	})
	defer unsub()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return ErrStateChangeTimeout
	}
}

// valuesMatch reports whether `expected` and `actual` represent the same
// logical value. Floats are compared in a fixed 2-decimal bucket; every other
// type is compared with `==`.
//
// The 2-decimal bucket is a POLICY CHOICE of this daemon, and it is
// UNVERIFIED as a model of what the CCU echoes — the firmware does not round
// float echoes to a fixed precision at all. What it does: on a write it
// quantises the logical value to round((v+offset)*factor) — half away from
// zero — and on every read or event it echoes physical/factor - offset
// (OpenCCU-Base src/libhsscomm/HSSTypeConversionFloatInteger.cpp:56-77). The
// quantum is therefore 1/factor, declared per parameter in the device-type
// XML; across OpenCCU-Base firmware/rftypes the observed factors run from 1
// to 1000, so the real quantum straddles 0.01 in both directions. The wire
// text is not two decimals either: XML-RPC serialises doubles with "%f", six
// decimals (src/libXmlRpc/src/XmlRpcValue.cpp:65).
//
// Both directions of the mismatch are reachable, and
// [TestHmCliValuesMatchTwoDecimalBucketIsAPolicy] measures them: with
// factor=2 (SET_TEMPERATURE) a write of 20.3 is echoed as 20.5 and REJECTED,
// and with factor=200 (LEVEL on blinds) two distinct positions 0.115 and 0.12
// collapse into one bucket and CONFIRM each other.
//
// The correct per-parameter tolerance is NOT PERFORMABLE from anything the
// daemon reads at runtime: the factor never leaves the device-type XML that
// rfd loads at startup — getParamsetDescription emits only TYPE/MIN/MAX/
// DEFAULT/SPECIAL for a FLOAT (src/libhsscomm/HSSLogicalTypeFloat.cpp:105-119)
// — so deriving the quantum from the descriptor is not possible, and picking a
// different constant would be the same guess with a different number. What
// would settle it: carrying the conversion factor into the paramset
// description this daemon holds, or dropping the value comparison and treating
// any echo for the key as the confirmation. Until one of those happens, the
// bucket stays as a documented policy rather than a wire fact.
//
// The exposure is bounded today: this path is reached only through
// [WaitForStateChangeOrTimeout], which both write call sites gate on
// WriteOptions.WaitForCallback, and no production caller sets that flag.
func valuesMatch(expected, actual any) bool {
	switch ev := expected.(type) {
	case float64:
		av, ok := toFloat64(actual)
		if !ok {
			return false
		}
		return roundTo2(ev) == roundTo2(av)
	case float32:
		av, ok := toFloat64(actual)
		if !ok {
			return false
		}
		return roundTo2(float64(ev)) == roundTo2(av)
	default:
		return expected == actual
	}
}

func toFloat64(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	default:
		return 0, false
	}
}

func roundTo2(v float64) float64 {
	return math.Round(v*100) / 100
}
