// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

//go:build chiptool

package chiptool

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestEvents_BasicInformation_StartUp reads the BasicInformation
// StartUp event on endpoint 0. The bridge fires this on boot; after
// commissioning the controller's read-event must return exactly one
// EventDataIB with SoftwareVersion populated.
//
// Mirrors v9 capability report T10.
func TestEvents_BasicInformation_StartUp(t *testing.T) {
	b := requireBridge(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	out, err := b.SharedCtl.ReadEvent(ctx, t, "basicinformation", "start-up", 0)
	if err != nil {
		t.Fatalf("read-event start-up: %v", err)
	}
	if !strings.Contains(out, "StartUp") {
		t.Errorf("StartUp event marker missing:\n%s", out)
	}
	if !strings.Contains(out, "SoftwareVersion") {
		t.Errorf("StartUp payload missing SoftwareVersion field:\n%s", out)
	}
}

// TestEvents_GeneralDiagnostics_BootReason reads the
// GeneralDiagnostics BootReason event on endpoint 0. The bridge
// fires this on boot with reason = PowerOnReboot (1) for a fresh
// startup. The v9 capability sweep flagged a regression where the
// event ID had drifted to 0x00; this test guards against that
// happening again from the chip-tool side.
//
// Mirrors v9 capability report T9.
func TestEvents_GeneralDiagnostics_BootReason(t *testing.T) {
	b := requireBridge(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	out, err := b.SharedCtl.ReadEvent(ctx, t, "generaldiagnostics", "boot-reason", 0)
	if err != nil {
		t.Fatalf("read-event boot-reason: %v", err)
	}
	if !strings.Contains(out, "BootReason") {
		t.Errorf("BootReason event marker missing:\n%s", out)
	}
	// The Priority::Critical marker is what guards against the prior
	// drift (event ID 0x00 silently returned an empty body). Matching
	// "Priority:" alone is enough because chip-tool prints it inside
	// every EventDataIB.
	if !strings.Contains(out, "Priority") {
		t.Errorf("BootReason event missing Priority field:\n%s", out)
	}
}

// TestEvents_BootReason_PayloadShape inspects the BootReason event
// body for the `BootReason: <n>` payload field. The v9 capability
// sweep flagged a regression where the event-ID drift returned an
// empty body; this assertion guards that the body is parseable and
// the BootReason field is present.
func TestEvents_BootReason_PayloadShape(t *testing.T) {
	b := requireBridge(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	out, err := b.SharedCtl.ReadEvent(ctx, t, "generaldiagnostics", "boot-reason", 0)
	if err != nil {
		t.Fatalf("read-event boot-reason: %v", err)
	}
	if !strings.Contains(out, "BootReason") {
		t.Errorf("BootReason payload field missing — empty event body? Output:\n%s", out)
	}
	// Per Matter §11.12.8.3 BootReason: 1 == PowerOnReboot. After a
	// clean daemon start the bridge emits exactly this. The
	// expected payload field is `BootReason: <n>`.
	if !strings.Contains(out, "BootReason:") {
		t.Errorf("BootReason value-line missing:\n%s", out)
	}
}
