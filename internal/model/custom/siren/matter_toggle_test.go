// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package siren

import (
	"context"
	"slices"
	"testing"

	"github.com/SukramJ/go-fabric/cluster/onoff"
)

// TestSirenOnOffToggleIsAdvertised pins Toggle onto the siren's OnOff
// cluster. matter.js declares it with conformance "!OFFONLY"
// (on-off.element.ts:39): mandatory unless the cluster advertises the
// OffOnly feature. The siren advertises LT and not OffOnly, so a controller
// that met a siren answering Toggle with an error would see a mandatory
// command missing — the shape that ends a commissioning in a pair-abort.
func TestSirenOnOffToggleIsAdvertised(t *testing.T) {
	t.Parallel()
	_, srv, _ := matterSiren(t)
	if !slices.Contains(srv.MatterAcceptedCommands(), onoff.CmdToggle) {
		t.Errorf("AcceptedCommandList = %#x, want Toggle (0x02): mandatory while OffOnly is unset",
			srv.MatterAcceptedCommands())
	}
	if fm, _ := srv.MatterRead(0xFFFC); fm != onoff.FeatureLighting {
		t.Errorf("FeatureMap = %v, want LT alone (0x%02X) — the premise that OffOnly is unset", fm, onoff.FeatureLighting)
	}
}

// TestSirenOnOffToggleFromUnobservedRaisesTheAlarm pins Toggle's direction
// on a siren nothing was observed for: the stored OnOff starts false, so
// Toggle raises the alarm through Siren.TurnOn.
func TestSirenOnOffToggleFromUnobservedRaisesTheAlarm(t *testing.T) {
	t.Parallel()
	_, srv, w := matterSiren(t)
	if _, err := srv.MatterInvoke(context.Background(), onoff.CmdToggle, nil); err != nil {
		t.Fatalf("Toggle: %v", err)
	}
	calls := w.snapshot()
	if len(calls) != 1 {
		t.Fatalf("Toggle: %d CCU write(s) %+v, want exactly one", len(calls), calls)
	}
	wantSirenWrite(t, calls[0], "FREQUENCY_RISING")
}

// TestSirenOnOffToggleFromActiveEndsTheAlarm pins the other direction: a
// siren the CCU confirmed active toggles off through Siren.TurnOff.
func TestSirenOnOffToggleFromActiveEndsTheAlarm(t *testing.T) {
	t.Parallel()
	r, srv, w := matterSiren(t)
	r.acousticActiveDP.OnEvent(true)
	if _, err := srv.MatterInvoke(context.Background(), onoff.CmdToggle, nil); err != nil {
		t.Fatalf("Toggle: %v", err)
	}
	calls := w.snapshot()
	if len(calls) != 1 {
		t.Fatalf("Toggle: %d CCU write(s) %+v, want exactly one", len(calls), calls)
	}
	wantSirenWrite(t, calls[0], acousticDisableLabel)
}
