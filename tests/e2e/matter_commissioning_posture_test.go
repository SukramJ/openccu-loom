// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

//go:build e2e

package e2e

import (
	"testing"

	"github.com/SukramJ/openccu-loom/tests/e2e/harness"
)

// TestE2EMatterWindowOnDemandFollowsTheSetting drives
// north.matter.commissioning.window_on_demand through the built daemon,
// both values, the way the Web UI sets it — a north.matter section save:
//
//   - off (the default): the unpaired bridge opens its own commissioning
//     window at start;
//   - the save of true is kept for the next start (restart-required, not
//     applied live), and after the restart the unpaired bridge has no
//     window open;
//   - the pairing page's open makes it commissionable.
func TestE2EMatterWindowOnDemandFollowsTheSetting(t *testing.T) {
	h := harness.Start(t, harness.Options{EnableMatter: true, StartCCUNotReady: true, AuthMode: harness.AuthSession})
	if err := h.REST().LoginSession(harness.AdminUser, harness.AdminPass); err != nil {
		t.Fatalf("login: %v", err)
	}
	h.CCU().V().SetReady(true)
	awaitCentralReady(t, h, harness.CentralName)

	if st := h.NodeStatus(t); !st.WindowOpen {
		t.Fatalf("window_on_demand off: the unpaired bridge has no window open at start (kind %q)", st.WindowKind)
	}

	applied, restart := h.SaveMatterSection(t, func(s map[string]any) {
		cm, _ := s["commissioning"].(map[string]any)
		if cm == nil {
			t.Fatal("the effective north.matter section carries no commissioning block")
		}
		cm["window_on_demand"] = true
	})
	if applied || !restart {
		t.Fatalf("north.matter save with window_on_demand true = applied %v, restart_required %v; want saved for the next start (applied false, restart_required true)", applied, restart)
	}

	h.Restart(t)
	if st := h.NodeStatus(t); st.WindowOpen {
		t.Fatalf("window_on_demand on: the unpaired bridge opened a window by itself after the restart (kind %q)", st.WindowKind)
	}

	// The pairing page refuses a window while the node exposes no endpoint
	// (a controller would cache an empty parts list).
	if n := h.EnableAllExposures(t); n == 0 {
		t.Fatal("the node offers no source to expose")
	}
	h.OpenPairingWindow(t, 180)
	if st := h.NodeStatus(t); !st.WindowOpen {
		t.Fatal("window_on_demand on: no window is open after the pairing page's open")
	}
}
