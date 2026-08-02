// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hub_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/internal/payload"
)

// TestProgramDeclaresItsTwoControls pins that the program model — not the
// bridge — decides which controls a program surfaces, per ADR 0011.
func TestProgramDeclaresItsTwoControls(t *testing.T) {
	t.Parallel()
	p := hub.NewProgram("ccu", "1234", "All off", "", true, nil)

	var addressable payload.MQTTRoleAddressable = p
	roles := addressable.MQTTRoles("loom", "ccu")
	if len(roles) != 2 {
		t.Fatalf("got %d roles, want 2 (activity toggle + execution)", len(roles))
	}

	toggle, execute := roles[0], roles[1]
	if toggle.Key != "" {
		t.Errorf("the activity toggle must stay the principal role, got key %q", toggle.Key)
	}
	if toggle.Component != "switch" {
		t.Errorf("toggle component = %q, want switch", toggle.Component)
	}
	if toggle.Topics.Set == "" || toggle.Topics.State == "" {
		t.Errorf("the toggle needs both a command and a state topic: %+v", toggle.Topics)
	}
	if toggle.Topics.Availability != "" {
		t.Error("the toggle must not be gated — it is what reactivates a program")
	}

	if execute.Key != hub.ProgramRoleExecute {
		t.Errorf("execute role key = %q, want %q", execute.Key, hub.ProgramRoleExecute)
	}
	if execute.Component != "button" {
		t.Errorf("execute component = %q, want button", execute.Component)
	}
	if execute.Topics.Trigger == "" {
		t.Error("the execution needs a trigger topic")
	}
	if execute.Topics.Availability == "" {
		t.Error("the execution must carry an availability topic — it is refused while inactive")
	}
}
