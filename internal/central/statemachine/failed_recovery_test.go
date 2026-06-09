// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package statemachine

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestCentralRecoversFromFailed is the regression tripwire for the
// FAILED-state trap. When every interface reconnects outside an active
// recovery pipeline (the clients' own reconnect path, in_recovery=false),
// evaluate_central_state computes RUNNING (or DEGRADED) and calls
// TransitionTo. If FAILED → RUNNING / FAILED → DEGRADED are not permitted,
// the transition is silently rejected and the central is stuck in FAILED
// forever: /health returns 503 and every heartbeat logs a futile
// failed→running while connectivity is actually fine.
//
// FAILED must be recoverable (only STOPPED is terminal), mirroring the client
// state machine, where ClientStateFailed transitions back into the connect
// path.
func TestCentralRecoversFromFailed(t *testing.T) {
	t.Parallel()

	toFailed := func(t *testing.T) *Central {
		t.Helper()
		m := NewCentral("test", nil)
		for _, s := range []hmenum.CentralState{
			hmenum.CentralStateInitializing,
			hmenum.CentralStateFailed,
		} {
			if err := m.TransitionTo(s, hmenum.FailureReasonNone); err != nil {
				t.Fatalf("boot transition to %s: %v", s, err)
			}
		}
		if m.State() != hmenum.CentralStateFailed {
			t.Fatalf("precondition: state=%s, want FAILED", m.State())
		}
		return m
	}

	t.Run("failed_to_running", func(t *testing.T) {
		t.Parallel()
		m := toFailed(t)
		if err := m.TransitionTo(hmenum.CentralStateRunning, hmenum.FailureReasonNone); err != nil {
			t.Fatalf("FAILED → RUNNING must be permitted so a central whose "+
				"clients reconnected can leave FAILED: %v", err)
		}
		if m.State() != hmenum.CentralStateRunning {
			t.Fatalf("state=%s, want RUNNING", m.State())
		}
	})

	t.Run("failed_to_degraded", func(t *testing.T) {
		t.Parallel()
		m := toFailed(t)
		if err := m.TransitionTo(hmenum.CentralStateDegraded, hmenum.FailureReasonNetwork); err != nil {
			t.Fatalf("FAILED → DEGRADED must be permitted (partial reconnect): %v", err)
		}
		if m.State() != hmenum.CentralStateDegraded {
			t.Fatalf("state=%s, want DEGRADED", m.State())
		}
	})
}
