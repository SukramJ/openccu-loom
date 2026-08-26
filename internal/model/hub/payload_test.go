// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hub

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// --- Program payload methods ---

func TestProgramInfoPayload(t *testing.T) {
	t.Parallel()
	w := &stubProgram{}
	p := NewProgram("ccu1", "p1", "MyProg", "desc", false, w)
	info, _ := p.Info().(*payload.ProgramInfo)
	if info == nil {
		t.Fatal("InfoPayload must not be nil")
	}
	if info.ID != "p1" {
		t.Fatalf("id = %v, want p1", info.ID)
	}
	if info.Category != "program" {
		t.Fatalf("category = %v, want program", info.Category)
	}
	// nil receiver
	if (*Program)(nil).Info() != nil {
		t.Fatal("nil Program.InfoPayload must return nil")
	}
}

func TestProgramConfigPayload(t *testing.T) {
	t.Parallel()
	w := &stubProgram{}
	p := NewProgram("ccu1", "p1", "MyProg", "desc", false, w)
	m := p.Config()
	if m == nil {
		t.Fatal("ConfigPayload must not be nil")
	}
	if (*Program)(nil).Config() != nil {
		t.Fatal("nil Program.ConfigPayload must return nil")
	}
}

func TestProgramStatePayload(t *testing.T) {
	t.Parallel()
	w := &stubProgram{}
	p := NewProgram("ccu1", "p1", "MyProg", "desc", false, w)
	m := p.State()
	if m == nil {
		t.Fatal("StatePayload must not be nil")
	}
	if (*Program)(nil).State() != nil {
		t.Fatal("nil Program.StatePayload must return nil")
	}
}

func TestProgramButtonAndSwitch(t *testing.T) {
	t.Parallel()
	w := &stubProgram{}
	p := NewProgram("ccu1", "p1", "MyProg", "desc", false, w)
	if p.Button() == nil {
		t.Fatal("Button() must not be nil")
	}
	if p.Switch() == nil {
		t.Fatal("Switch() must not be nil")
	}
}

func TestProgramLastExecuteTimeString(t *testing.T) {
	t.Parallel()
	w := &stubProgram{}
	p := NewProgram("ccu1", "p1", "MyProg", "desc", false, w)
	if got := p.LastExecuteTimeString(); got != "" {
		t.Fatalf("fresh program: LastExecuteTimeString = %q, want empty", got)
	}
}

// --- Sysvar payload methods ---

func TestSysvarInfoPayload(t *testing.T) {
	t.Parallel()
	sv := NewSysvar("ccu1", "sv1", "desc", hmenum.HubValueTypeLogic, nil)
	info, _ := sv.Info().(*payload.SysvarInfo)
	if info == nil {
		t.Fatal("InfoPayload must not be nil")
	}
	if info.Category != "sysvar" {
		t.Fatalf("category = %v, want sysvar", info.Category)
	}
	if (*Sysvar)(nil).Info() != nil {
		t.Fatal("nil Sysvar.InfoPayload must return nil")
	}
}

func TestSysvarConfigPayload(t *testing.T) {
	t.Parallel()
	sv := NewSysvar("ccu1", "sv1", "desc", hmenum.HubValueTypeLogic, nil)
	m := sv.Config()
	if m == nil {
		t.Fatal("ConfigPayload must not be nil")
	}
	if (*Sysvar)(nil).Config() != nil {
		t.Fatal("nil Sysvar.ConfigPayload must return nil")
	}
}

func TestSysvarStatePayload(t *testing.T) {
	t.Parallel()
	sv := NewSysvar("ccu1", "sv1", "desc", hmenum.HubValueTypeLogic, nil)
	m := sv.State()
	if m == nil {
		t.Fatal("StatePayload must not be nil")
	}
	if (*Sysvar)(nil).State() != nil {
		t.Fatal("nil Sysvar.StatePayload must return nil")
	}
}

// --- Update payload methods ---

func TestUpdatePayloads(t *testing.T) {
	t.Parallel()
	u := NewUpdate()
	if u.Info() == nil {
		t.Fatal("Update.InfoPayload must not be nil")
	}
	if u.Config() != nil {
		t.Fatal("Update.ConfigPayload must be nil")
	}
	m := u.State()
	if m == nil {
		t.Fatal("Update.StatePayload must not be nil")
	}
	if (*Update)(nil).Info() != nil {
		t.Fatal("nil Update.InfoPayload must return nil")
	}
	if (*Update)(nil).State() != nil {
		t.Fatal("nil Update.StatePayload must return nil")
	}
}

// --- AlarmMessages payload methods ---

func TestAlarmMessagesPayloads(t *testing.T) {
	t.Parallel()
	am := NewAlarmMessages(nil)
	if am.Info() == nil {
		t.Fatal("AlarmMessages.InfoPayload must not be nil")
	}
	// ConfigPayload returns nil by design.
	if am.State() == nil {
		t.Fatal("AlarmMessages.StatePayload must not be nil")
	}
	if (*AlarmMessages)(nil).Info() != nil {
		t.Fatal("nil AlarmMessages.InfoPayload must return nil")
	}
	if (*AlarmMessages)(nil).State() != nil {
		t.Fatal("nil AlarmMessages.StatePayload must return nil")
	}
}

// --- ServiceMessages payload methods ---

func TestServiceMessagesPayloads(t *testing.T) {
	t.Parallel()
	sm := NewServiceMessages(nil)
	if sm.Info() == nil {
		t.Fatal("ServiceMessages.InfoPayload must not be nil")
	}
	// ConfigPayload returns nil by design.
	if sm.State() == nil {
		t.Fatal("ServiceMessages.StatePayload must not be nil")
	}
	if (*ServiceMessages)(nil).Info() != nil {
		t.Fatal("nil ServiceMessages.InfoPayload must return nil")
	}
}

// --- InstallMode payload methods ---

func TestInstallModePayloads(t *testing.T) {
	t.Parallel()
	m := NewInstallMode("HmIP-RF", nil)
	if m.Info() == nil {
		t.Fatal("InstallMode.InfoPayload must not be nil")
	}
	if m.Config() != nil {
		t.Fatal("InstallMode.ConfigPayload must be nil")
	}
	if m.State() == nil {
		t.Fatal("InstallMode.StatePayload must not be nil")
	}
	if (*InstallMode)(nil).Info() != nil {
		t.Fatal("nil InstallMode.InfoPayload must return nil")
	}
	if (*InstallMode)(nil).State() != nil {
		t.Fatal("nil InstallMode.StatePayload must return nil")
	}
}

// --- Connectivity payload methods ---

func TestConnectivityPayloads(t *testing.T) {
	t.Parallel()
	c := NewConnectivity()
	if c.Info() == nil {
		t.Fatal("Connectivity.InfoPayload must not be nil")
	}
	if c.Config() != nil {
		t.Fatal("Connectivity.ConfigPayload must be nil")
	}
	if c.State() == nil {
		t.Fatal("Connectivity.StatePayload must not be nil")
	}
	if (*Connectivity)(nil).Info() != nil {
		t.Fatal("nil Connectivity.InfoPayload must return nil")
	}
}

// --- Inbox payload methods ---

func TestInboxPayloads(t *testing.T) {
	t.Parallel()
	inbox := NewInbox()
	if inbox.Info() == nil {
		t.Fatal("Inbox.InfoPayload must not be nil")
	}
	// ConfigPayload returns nil by design.
	if inbox.State() == nil {
		t.Fatal("Inbox.StatePayload must not be nil")
	}
	if (*Inbox)(nil).Info() != nil {
		t.Fatal("nil Inbox.InfoPayload must return nil")
	}
}

// --- Metrics payload methods ---

func TestMetricsPayloads(t *testing.T) {
	t.Parallel()
	m := NewMetrics()
	if m.Info() == nil {
		t.Fatal("Metrics.InfoPayload must not be nil")
	}
	// ConfigPayload returns nil by design.
	if m.State() == nil {
		t.Fatal("Metrics.StatePayload must not be nil")
	}
	if (*Metrics)(nil).Info() != nil {
		t.Fatal("nil Metrics.InfoPayload must return nil")
	}
}

// --- Hub payload methods ---

func TestHubPayloads(t *testing.T) {
	t.Parallel()
	h := NewHub("ccu1")
	if h.Info() == nil {
		t.Fatal("Hub.InfoPayload must not be nil")
	}
	if h.Config() != nil {
		t.Fatal("Hub.ConfigPayload must be nil")
	}
	if h.State() == nil {
		t.Fatal("Hub.StatePayload must not be nil")
	}
}

// --- InstallMode service registration (enable/disable) ---

func TestInstallModeEnableDisableServices(t *testing.T) {
	t.Parallel()

	var lastCall [3]any
	w := &stubInstall{}
	_ = w

	m := NewInstallMode("HmIP-RF", &installModeWriterStub{})

	// enable service — should write
	err := m.Invoke(context.Background(), "enable", map[string]any{"seconds": int32(30)}, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("enable service: %v", err)
	}
	_ = lastCall

	// disable service
	if err := m.Invoke(context.Background(), "disable", nil, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("disable service: %v", err)
	}
}

type installModeWriterStub struct{}

func (s *installModeWriterStub) SetInstallMode(_ context.Context, _ string, _ bool, _ time.Duration) error {
	return nil
}
