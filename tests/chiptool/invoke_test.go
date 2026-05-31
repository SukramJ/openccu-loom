// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build chiptool

package chiptool

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/tests/chiptool/harness"
)

// TestInvoke_OnOff_OnToggleOffCycle drives an On → Toggle → Off
// cycle through an OnOff endpoint, asserting both the invoke
// responses and the readback after each step. Writes go against
// godevccu (in-process simulator); the live CCU is never touched.
// Final step is an explicit `off` so the test leaves the simulator
// in a deterministic state — mirrors the v9 brief's T6 housekeeping
// rule even though we are not on real hardware.
//
// Mirrors v9 capability report T6 (against godevccu rather than the
// sanctioned write target on the live CCU).
func TestInvoke_OnOff_OnToggleOffCycle(t *testing.T) {
	b := requireBridge(t)
	eps := discoverEndpointsWith(t, b, 0x0006, 1)
	if len(eps) == 0 {
		t.Skip("no OnOff endpoint — godevccu fleet lacks a Switch/PSM device")
	}
	ep := eps[0]
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// 1. Force OFF baseline so the test is reproducible no matter
	// what the simulator's last state was.
	if _, err := b.SharedCtl.Invoke(ctx, t, "onoff", "off", ep); err != nil {
		t.Fatalf("baseline off: %v", err)
	}

	// 2. Read OnOff — expect FALSE.
	readOut, err := b.SharedCtl.ReadAttr(ctx, t, "onoff", "on-off", ep)
	if err != nil {
		t.Fatalf("read after baseline off: %v", err)
	}
	if v, ok := harness.FindAttrBool(readOut, "OnOff"); ok && v {
		t.Errorf("after baseline off, OnOff=TRUE")
	}

	// 3. ON → expect TRUE.
	if _, err := b.SharedCtl.Invoke(ctx, t, "onoff", "on", ep); err != nil {
		t.Fatalf("on: %v", err)
	}
	readOut, err = b.SharedCtl.ReadAttr(ctx, t, "onoff", "on-off", ep)
	if err != nil {
		t.Fatalf("read after on: %v", err)
	}
	if v, ok := harness.FindAttrBool(readOut, "OnOff"); ok && !v {
		t.Errorf("after on, OnOff=FALSE")
	}

	// 4. TOGGLE → expect FALSE.
	if _, err := b.SharedCtl.Invoke(ctx, t, "onoff", "toggle", ep); err != nil {
		t.Fatalf("toggle: %v", err)
	}
	readOut, err = b.SharedCtl.ReadAttr(ctx, t, "onoff", "on-off", ep)
	if err != nil {
		t.Fatalf("read after toggle: %v", err)
	}
	if v, ok := harness.FindAttrBool(readOut, "OnOff"); ok && v {
		t.Errorf("after toggle, OnOff=TRUE")
	}

	// 5. Housekeeping: explicit OFF so the simulator ends OFF.
	if _, err := b.SharedCtl.Invoke(ctx, t, "onoff", "off", ep); err != nil {
		t.Errorf("housekeeping off: %v", err)
	}
}

// TestInvoke_Identify finds an endpoint advertising the Identify
// cluster (0x0003) and invokes `identify` with a 1 s duration. The
// invoke must return a SUCCESS status.
func TestInvoke_Identify(t *testing.T) {
	b := requireBridge(t)
	eps := discoverEndpointsWith(t, b, 0x0003, 1)
	if len(eps) == 0 {
		t.Skip("no Identify endpoint")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	out, err := b.SharedCtl.Invoke(ctx, t, "identify", "identify", eps[0], "1")
	if err != nil {
		t.Fatalf("invoke identify: %v", err)
	}
	if !harness.CommandSuccess(out) {
		t.Errorf("identify invoke did not report success:\n%s", out)
	}
}
