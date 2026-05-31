// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Tests for MasterPoller (master_poll.go).

package backends

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// fakeGetter is a minimal MasterGetter for MasterPoller tests.
type fakeGetter struct {
	result map[string]any
	err    error
}

func (f *fakeGetter) GetParamset(_ context.Context, _ string, _ hmenum.ParamsetKey) (map[string]any, error) {
	return f.result, f.err
}

func TestMasterPollerNewAndClose(t *testing.T) {
	t.Parallel()
	p := NewMasterPoller(&fakeGetter{result: map[string]any{"LEVEL": 0.5}})
	if p == nil {
		t.Fatal("NewMasterPoller returned nil")
	}
	// Close on a fresh poller with no scheduled polls must not panic.
	p.Close()
	// Second close is a no-op.
	p.Close()
}

func TestMasterPollerNilReceiverSafe(t *testing.T) {
	t.Parallel()
	var p *MasterPoller
	// SchedulePoll and Close on nil receiver must not panic.
	p.SchedulePoll("ADDR:1", hmenum.ParamsetKeyMaster)
	p.Close()
}

func TestMasterPollerSchedulePollNoOpWithoutOnRefresh(t *testing.T) {
	t.Parallel()
	g := &fakeGetter{result: map[string]any{"X": 1}}
	p := NewMasterPoller(g)
	p.OnRefresh = nil // explicit: no callback configured
	// Must return silently; no goroutine launched (getter never called).
	p.SchedulePoll("ADDR:1", hmenum.ParamsetKeyMaster)
	p.Close()
}

func TestMasterPollerDeliversFreshValues(t *testing.T) {
	t.Parallel()
	want := map[string]any{"LEVEL": float64(0.75)}
	g := &fakeGetter{result: want}
	p := NewMasterPoller(g)
	p.Interval = 5 * time.Millisecond

	refreshed := make(chan map[string]any, 1)
	p.OnRefresh = func(_ string, _ hmenum.ParamsetKey, values map[string]any) {
		refreshed <- values
	}

	p.SchedulePoll("VCU1234567:0", hmenum.ParamsetKeyMaster)

	select {
	case got := <-refreshed:
		if got["LEVEL"] != float64(0.75) {
			t.Fatalf("LEVEL=%v, want 0.75", got["LEVEL"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OnRefresh was never called")
	}
	p.Close()
}

func TestMasterPollerTwoDistinctKeysBothFire(t *testing.T) {
	t.Parallel()
	g := &fakeGetter{result: map[string]any{}}
	p := NewMasterPoller(g)
	p.Interval = 10 * time.Millisecond

	fired := make(chan string, 10)
	p.OnRefresh = func(address string, _ hmenum.ParamsetKey, _ map[string]any) {
		fired <- address
	}

	p.SchedulePoll("ADDR:0", hmenum.ParamsetKeyMaster)
	p.SchedulePoll("ADDR:1", hmenum.ParamsetKeyMaster)

	got := make(map[string]bool)
	deadline := time.After(2 * time.Second)
	for len(got) < 2 {
		select {
		case addr := <-fired:
			got[addr] = true
		case <-deadline:
			t.Fatalf("only got addresses %v, want both ADDR:0 and ADDR:1", got)
		}
	}
	p.Close()
}

func TestMasterPollerCallsOnErrorOnGetterFailure(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("rpc error")
	g := &fakeGetter{err: sentinel}
	p := NewMasterPoller(g)
	p.Interval = 5 * time.Millisecond
	p.OnRefresh = func(_ string, _ hmenum.ParamsetKey, _ map[string]any) {
		// Should not be called on error.
		t.Error("OnRefresh called on getter error")
	}
	errCh := make(chan error, 1)
	p.OnError = func(_ string, _ hmenum.ParamsetKey, err error) {
		errCh <- err
	}

	p.SchedulePoll("ADDR:0", hmenum.ParamsetKeyMaster)

	select {
	case got := <-errCh:
		if !errors.Is(got, sentinel) {
			t.Fatalf("err=%v, want sentinel", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OnError was never called")
	}
	p.Close()
}

func TestMasterPollerCancelOnClose(t *testing.T) {
	t.Parallel()
	// Interval is long; Close must cancel the pending poll before it fires.
	fired := make(chan struct{}, 1)
	g := &fakeGetter{result: map[string]any{}}
	p := NewMasterPoller(g)
	p.Interval = 10 * time.Second
	p.OnRefresh = func(_ string, _ hmenum.ParamsetKey, _ map[string]any) {
		fired <- struct{}{}
	}

	p.SchedulePoll("ADDR:0", hmenum.ParamsetKeyMaster)
	// Close immediately — the timer should be cancelled before it fires.
	p.Close()

	select {
	case <-fired:
		t.Fatal("OnRefresh must not be called after Close")
	case <-time.After(100 * time.Millisecond):
		// expected: no call
	}
}

func TestMasterPollerScheduleAfterCloseIsNoop(t *testing.T) {
	t.Parallel()
	g := &fakeGetter{result: map[string]any{}}
	p := NewMasterPoller(g)
	p.OnRefresh = func(_ string, _ hmenum.ParamsetKey, _ map[string]any) {}
	p.Close()
	// SchedulePoll after Close must not panic or launch a goroutine.
	p.SchedulePoll("ADDR:0", hmenum.ParamsetKeyMaster)
}

func TestMasterPollerDefaultIntervalUsedWhenZero(t *testing.T) {
	t.Parallel()
	// Verify that Interval <= 0 uses the 2s default without panicking.
	// Close immediately to avoid waiting the full default interval.
	g := &fakeGetter{result: map[string]any{}}
	p := NewMasterPoller(g)
	p.Interval = 0 // triggers the "use default" branch in run()
	p.OnRefresh = func(_ string, _ hmenum.ParamsetKey, _ map[string]any) {}
	p.SchedulePoll("ADDR:0", hmenum.ParamsetKeyMaster)
	// Immediate close cancels the long timer — no hang.
	p.Close()
}
