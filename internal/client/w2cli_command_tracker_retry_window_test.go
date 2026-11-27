// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package client

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/client/reliability"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

const w2CliCentral = "w2cli-tracker"

// w2CliDutyCycleWriter is the production write path in miniature: a
// [ValueWriter] whose registered backend drives its wire calls through
// [InterfaceClient.Call] (via [BackendCaller], as the daemon wires every
// CCU backend), and whose command-tracker hook records into that client's
// tracker the way the composition root's hook does.
type w2CliDutyCycleWriter struct {
	ic     *InterfaceClient
	writer *ValueWriter
	wireID hmtypes.WireInterfaceID
	calls  atomic.Int64
}

// w2CliNewDutyCycleWriter answers the first `failures` setValue wire calls
// with the CCU's DUTY_CYCLE fault (-8) and succeeds afterwards. The retrier
// waits dutyCycleDelay between attempts on that fault, and the client's
// command tracker keeps entries for ttl. A ttl shorter than the retry window
// is what makes the ordering observable at all.
func w2CliNewDutyCycleWriter(t *testing.T, failures int64, dutyCycleDelay, ttl time.Duration) *w2CliDutyCycleWriter {
	t.Helper()

	w := &w2CliDutyCycleWriter{}
	retrier := reliability.NewRetrier(reliability.RetryConfig{
		MaxAttempts:    3,
		Initial:        time.Microsecond,
		Max:            time.Microsecond,
		DutyCycleDelay: dutyCycleDelay,
	})
	ic, err := New(Config{
		CentralName: w2CliCentral,
		Interface:   hmenum.InterfaceHmIPRF,
		Caller: CallerFunc(func(_ context.Context, method string, _ []any) (any, error) {
			if method == "setValue" && w.calls.Add(1) <= failures {
				return nil, &hmerr.XMLRPCFault{Code: int(hmerr.XMLRPCFaultDutyCycle), Message: "not enough DutyCycle free"}
			}
			return nil, nil
		}),
		Retrier: retrier,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ic.commandTracker = reliability.NewCommandTracker(
		string(hmtypes.NewWireInterfaceID(ic.cfg.CentralName, ic.cfg.Interface)),
		reliability.CommandTrackerConfig{TTL: ttl},
	)

	w.ic = ic
	w.wireID = hmtypes.NewWireInterfaceID(w2CliCentral, hmenum.InterfaceHmIPRF)
	w.writer = NewValueWriter()
	w.writer.Register(w2CliCentral, w.wireID, backends.NewCcuBackend(NewBackendCaller(ic, hmenum.CommandPriorityLow), nil, nil))
	w.writer.SetCommandTrackerFn(func(_, channelAddress string, parameter hmenum.Parameter, paramsetKey hmenum.ParamsetKey, value any) {
		ic.WriteUnconfirmedValue(channelAddress, parameter, paramsetKey, value)
	})
	return w
}

func (w *w2CliDutyCycleWriter) setLevel(channelAddress string, value float64) error {
	return w.writer.SetValue(
		context.Background(), w2CliCentral, w.wireID.String(), channelAddress,
		hmenum.ParameterLevel, value, hmenum.CommandPriorityLow,
	)
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
	w := w2CliNewDutyCycleWriter(t, 2, delay, ttl)

	start := time.Now()
	if err := w.setLevel("VCU0000001:1", 0.5); err != nil {
		t.Fatalf("SetValue after two DUTY_CYCLE faults: %v", err)
	}
	elapsed := time.Since(start)
	if elapsed < 2*delay {
		t.Fatalf("retry window collapsed: SetValue returned after %v, want at least %v — the guard cannot distinguish stamp-before from stamp-after", elapsed, 2*delay)
	}
	if got := w.calls.Load(); got != 3 {
		t.Fatalf("setValue reached the wire %d times, want 3 (two DUTY_CYCLE faults then success)", got)
	}

	value, ok := w.ic.CommandTracker().GetLastSentValue(w2CliTrackerKey(w.ic, "VCU0000001:1", hmenum.ParameterLevel))
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

	w := w2CliNewDutyCycleWriter(t, 99, time.Millisecond, 60*time.Second)

	if err := w.setLevel("VCU0000002:1", 0.5); err == nil {
		t.Fatal("SetValue with a permanently duty-cycle-blocked backend returned nil, want the fault")
	}
	if _, ok := w.ic.CommandTracker().GetLastSentValue(w2CliTrackerKey(w.ic, "VCU0000002:1", hmenum.ParameterLevel)); ok {
		t.Errorf("GetLastSentValue after a failed write = (value, true): a write the CCU rejected with DUTY_CYCLE left an optimistic value that north-bound reads will report as the device state")
	}
}
