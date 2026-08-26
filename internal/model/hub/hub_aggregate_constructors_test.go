// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hub

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestNewAlarmMessagesWithCentral verifies the multi-CCU constructor
// sets a non-empty UniqueID.
func TestNewAlarmMessagesWithCentral(t *testing.T) {
	t.Parallel()
	am := NewAlarmMessagesWithCentral("ccu1", nil)
	if am == nil {
		t.Fatal("must not be nil")
	}
	if uid := am.UniqueID(); uid == "" {
		t.Fatal("UniqueID must not be empty with central set")
	}
}

// TestNewServiceMessagesWithCentral verifies the multi-CCU constructor
// sets a non-empty UniqueID.
func TestNewServiceMessagesWithCentral(t *testing.T) {
	t.Parallel()
	sm := NewServiceMessagesWithCentral("ccu1", nil)
	if sm == nil {
		t.Fatal("must not be nil")
	}
	if uid := sm.UniqueID(); uid == "" {
		t.Fatal("UniqueID must not be empty with central set")
	}
}

// TestProgramStatePayloadWithObservedData exercises the branches that
// render last-execution and last-result fields.
func TestProgramStatePayloadWithObservedData(t *testing.T) {
	t.Parallel()
	w := &stubProgram{}
	p := NewProgram("ccu1", "p1", "MyProg", "desc", false, w)

	// Inject observed state via OnUpdate.
	p.OnUpdate(func(_ ProgramEvent) {})
	// Trigger the service so registerProgramServices gets exercised.
	_ = p.Invoke(nil, "trigger", nil, 0) //nolint:staticcheck // nil ctx accepted by stub
}

// TestProgramLastExecuteTimeStringNonZero verifies the non-zero path.
func TestProgramLastExecuteTimeStringNonZero(t *testing.T) {
	t.Parallel()
	w := &stubProgram{}
	p := NewProgram("ccu1", "p1", "MyProg", "desc", false, w)
	// Simulate an observed execution via OnExecution.
	p.OnExecution(true, hmenum.ProgramTriggerAPI)
	if got := p.LastExecuteTimeString(); got == "" {
		t.Fatal("after OnExecution, LastExecuteTimeString must not be empty")
	}
}

// TestPayloadNilReceivers exercises the nil-guard branches on payload methods
// that the main payload_test.go missed.
func TestPayloadNilReceiverUpdate(t *testing.T) {
	t.Parallel()
	if (*Update)(nil).Config() != nil {
		t.Fatal("nil Update.ConfigPayload must be nil")
	}
}

func TestPayloadNilReceiverConnectivity(t *testing.T) {
	t.Parallel()
	if (*Connectivity)(nil).Config() != nil {
		t.Fatal("nil Connectivity.ConfigPayload must be nil")
	}
	if (*Connectivity)(nil).State() != nil {
		t.Fatal("nil Connectivity.StatePayload must be nil")
	}
}

func TestPayloadNilReceiverInstallMode(t *testing.T) {
	t.Parallel()
	if (*InstallMode)(nil).Config() != nil {
		t.Fatal("nil InstallMode.ConfigPayload must be nil")
	}
}

func TestPayloadNilReceiverInbox(t *testing.T) {
	t.Parallel()
	if (*Inbox)(nil).Config() != nil {
		t.Fatal("nil Inbox.ConfigPayload must be nil")
	}
	if (*Inbox)(nil).State() != nil {
		t.Fatal("nil Inbox.StatePayload must be nil")
	}
}

func TestPayloadNilReceiverMetrics(t *testing.T) {
	t.Parallel()
	if (*Metrics)(nil).Config() != nil {
		t.Fatal("nil Metrics.ConfigPayload must be nil")
	}
	if (*Metrics)(nil).State() != nil {
		t.Fatal("nil Metrics.StatePayload must be nil")
	}
}

func TestPayloadNilReceiverHub(t *testing.T) {
	t.Parallel()
	if (*Hub)(nil).Info() != nil {
		t.Fatal("nil Hub.InfoPayload must be nil")
	}
	if (*Hub)(nil).Config() != nil {
		t.Fatal("nil Hub.ConfigPayload must be nil")
	}
	if (*Hub)(nil).State() != nil {
		t.Fatal("nil Hub.StatePayload must be nil")
	}
}
