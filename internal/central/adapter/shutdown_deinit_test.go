// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"errors"
	"testing"
)

// recordingDeiniter captures the context a teardown closer hands to Deinit.
type recordingDeiniter struct {
	calls    int
	url      string
	ctxErr   error
	deadline bool
	ret      error
}

func (d *recordingDeiniter) Deinit(ctx context.Context, callbackURL string) error {
	d.calls++
	d.url = callbackURL
	d.ctxErr = ctx.Err()
	_, d.deadline = ctx.Deadline()
	return d.ret
}

// TestDeinitOnShutdownUsesLiveContext pins the contract every teardown closer
// depends on: the deinit RPC must run on a live, deadline-bounded context even
// though the caller's own bring-up context is already cancelled by the time
// teardown runs. Inheriting the cancelled context makes the call fail before
// it leaves the process, so the CCU keeps the stale callback registration and
// delivers every event twice once the next generation registers.
func TestDeinitOnShutdownUsesLiveContext(t *testing.T) {
	t.Parallel()

	d := &recordingDeiniter{}
	deinitOnShutdown(d, "xmlrpc_bin://10.0.0.9:8129", "ccu-01", "loom-ccu-01-CUxD", nil)

	if d.calls != 1 {
		t.Fatalf("Deinit called %d time(s), want 1", d.calls)
	}
	if d.ctxErr != nil {
		t.Fatalf("Deinit ran on a cancelled context (%v); it must get a fresh one", d.ctxErr)
	}
	if !d.deadline {
		t.Error("the shutdown deinit must be deadline-bounded so a wedged CCU cannot hold shutdown open")
	}
	if d.url != "xmlrpc_bin://10.0.0.9:8129" {
		t.Errorf("callbackURL = %q", d.url)
	}
}

// TestDeinitOnShutdownSkipsWithoutRegistration verifies the no-op cases: no
// backend and no callback URL both mean there is nothing registered on the CCU
// to sever.
func TestDeinitOnShutdownSkipsWithoutRegistration(t *testing.T) {
	t.Parallel()

	d := &recordingDeiniter{ret: errors.New("must not be called")}
	deinitOnShutdown(d, "", "ccu-01", "loom-ccu-01-CUxD", nil)
	if d.calls != 0 {
		t.Errorf("Deinit called %d time(s) without a callback URL, want 0", d.calls)
	}
	deinitOnShutdown(nil, "http://host/RPC2/ccu-01", "ccu-01", "loom-ccu-01-HmIP-RF", nil)
}
