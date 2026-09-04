// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package client

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/client/reliability"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// w2CliDutyCycleThenOKBackend answers the first `failures` SetValue calls
// with the CCU's DUTY_CYCLE fault (-8) and succeeds afterwards. Everything
// else is inherited from countingBackend (skip_retry_test.go).
type w2CliDutyCycleThenOKBackend struct {
	*countingBackend

	failures int64
	calls    atomic.Int64
}

func (b *w2CliDutyCycleThenOKBackend) SetValue(
	_ context.Context, _ string, _ hmenum.Parameter, _ any,
	_ hmenum.CommandPriority, _ hmenum.CommandRxMode,
) error {
	if b.calls.Add(1) <= b.failures {
		return &hmerr.XMLRPCFault{Code: int(hmerr.XMLRPCFaultDutyCycle), Message: "not enough DutyCycle free"}
	}
	return nil
}

// w2CliNewDutyCycleIC builds an InterfaceClient whose retrier waits
// dutyCycleDelay between attempts on fault -8, with a command tracker whose
// TTL is ttl. A ttl shorter than the retry window is what makes the ordering
// observable at all.
func w2CliNewDutyCycleIC(t *testing.T, failures int64, dutyCycleDelay, ttl time.Duration) (*InterfaceClient, *w2CliDutyCycleThenOKBackend) {
	t.Helper()

	retrier := reliability.NewRetrier(reliability.RetryConfig{
		MaxAttempts:    3,
		Initial:        time.Microsecond,
		Max:            time.Microsecond,
		DutyCycleDelay: dutyCycleDelay,
	})
	ic, err := New(Config{
		CentralName: "w2cli-tracker",
		Interface:   hmenum.InterfaceHmIPRF,
		Caller:      CallerFunc(func(_ context.Context, _ string, _ []any) (any, error) { return nil, nil }),
		Retrier:     retrier,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ic.commandTracker = reliability.NewCommandTracker(
		string(hmtypes.NewWireInterfaceID(ic.cfg.CentralName, ic.cfg.Interface)),
		reliability.CommandTrackerConfig{TTL: ttl},
	)
	return ic, &w2CliDutyCycleThenOKBackend{countingBackend: &countingBackend{}, failures: failures}
}

func w2CliTrackerKey(ic *InterfaceClient, channelAddress string, parameter hmenum.Parameter) hmtypes.DataPointKey {
	return hmtypes.DataPointKey{
		InterfaceID:    string(hmtypes.NewWireInterfaceID(ic.cfg.CentralName, ic.cfg.Interface)),
		ChannelAddress: channelAddress,
		ParamsetKey:    hmenum.ParamsetKeyValues,
		Parameter:      string(parameter),
	}
}

// TestW2CliCommandTrackerRecordsAfterTheRetryWindow pins the ordering that
// decides whether the tracker's TTL competes with the daemon's own retry
// waits: the optimistic entry is stamped only after the reliability stack
// has returned success, so every backoff — including the fixed
// DutyCycleDelay on fault -8 — is spent BEFORE sentAt exists, not against
// the TTL.
//
// Stamping the entry before the retry chain would silently subtract the
// whole retry window from the TTL: with the production defaults (TTL 60 s,
// DutyCycleDelay 40 s) a two-wait chain would leave an entry that is already
// two thirds expired the moment the write succeeds, and a three-wait chain
// one that never becomes readable at all.
//
// The test compresses that relation: the retry window (2 x 200 ms) is longer
// than the tracker TTL (150 ms), so a read taken immediately after a
// successful write answers true only when sentAt was taken after the waits.
func TestW2CliCommandTrackerRecordsAfterTheRetryWindow(t *testing.T) {
	t.Parallel()

	const (
		delay = 200 * time.Millisecond
		ttl   = 150 * time.Millisecond
	)
	ic, b := w2CliNewDutyCycleIC(t, 2, delay, ttl)

	start := time.Now()
	if err := ic.SetValue(
		context.Background(), b,
		"VCU0000001:1", hmenum.ParameterLevel, 0.5,
		hmenum.CommandPriorityLow, hmenum.CommandRxModeUnset,
		false,
	); err != nil {
		t.Fatalf("SetValue after two DUTY_CYCLE faults: %v", err)
	}
	elapsed := time.Since(start)
	if elapsed < 2*delay {
		t.Fatalf("retry window collapsed: SetValue returned after %v, want at least %v — the guard cannot distinguish stamp-before from stamp-after", elapsed, 2*delay)
	}
	if got := b.calls.Load(); got != 3 {
		t.Fatalf("backend.SetValue called %d times, want 3 (two DUTY_CYCLE faults then success)", got)
	}

	value, ok := ic.CommandTracker().GetLastSentValue(w2CliTrackerKey(ic, "VCU0000001:1", hmenum.ParameterLevel))
	if !ok {
		t.Errorf("GetLastSentValue right after a successful write = (nil, false): the tracker entry is older than the %v TTL although the write only just returned — the optimistic value was stamped before the %v retry window instead of after it, so every retry wait is subtracted from the TTL", ttl, elapsed)
	}
	if ok && value != 0.5 {
		t.Errorf("GetLastSentValue = %v, want 0.5", value)
	}
}

// TestW2CliCommandTrackerRecordsNothingForAFailedWrite is the other half of
// the same ordering: a write that exhausts its attempts must leave no
// optimistic value behind. A north-bound read that answered with the value
// of a write the CCU rejected would report a state the device never took.
//
// The TTL here is the production default, so an entry stamped anywhere in
// the chain is still live when the assertion runs.
func TestW2CliCommandTrackerRecordsNothingForAFailedWrite(t *testing.T) {
	t.Parallel()

	ic, b := w2CliNewDutyCycleIC(t, 99, time.Millisecond, 60*time.Second)

	err := ic.SetValue(
		context.Background(), b,
		"VCU0000002:1", hmenum.ParameterLevel, 0.5,
		hmenum.CommandPriorityLow, hmenum.CommandRxModeUnset,
		false,
	)
	if err == nil {
		t.Fatal("SetValue with a permanently duty-cycle-blocked backend returned nil, want the fault")
	}
	if _, ok := ic.CommandTracker().GetLastSentValue(w2CliTrackerKey(ic, "VCU0000002:1", hmenum.ParameterLevel)); ok {
		t.Errorf("GetLastSentValue after a failed write = (value, true): a write the CCU rejected with DUTY_CYCLE left an optimistic value that north-bound reads will report as the device state")
	}
}
