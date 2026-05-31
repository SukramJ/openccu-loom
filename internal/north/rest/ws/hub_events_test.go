// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ws

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

func TestSysvarTopicFormat(t *testing.T) {
	got := SysvarTopic("home", "EnergyCounter")
	if want := "hub.home.sysvars.EnergyCounter"; got != want {
		t.Fatalf("SysvarTopic = %q, want %q", got, want)
	}
}

func TestProgramTopicFormat(t *testing.T) {
	got := ProgramTopic("home", "1234")
	if want := "hub.home.programs.1234"; got != want {
		t.Fatalf("ProgramTopic = %q, want %q", got, want)
	}
}

func TestHubEventsSubscriberNilSafe(t *testing.T) {
	s := NewHubEventsSubscriber(nil, nil)
	s.Start()
	s.Stop()
}

func TestInstallModeTopicFormat(t *testing.T) {
	got := InstallModeTopic("home")
	if want := "hub.home.install_mode"; got != want {
		t.Fatalf("InstallModeTopic = %q, want %q", got, want)
	}
}

func TestInstallModeChangedPayloadShape(t *testing.T) {
	p := InstallModeChangedPayload{
		Central:    "home",
		Enabled:    true,
		RemainingS: 42,
	}
	if p.Central != "home" || !p.Enabled || p.RemainingS != 42 {
		t.Fatalf("payload field round-trip failed: %+v", p)
	}
}

func TestSysvarChangedPayloadShape(t *testing.T) {
	p := SysvarChangedPayload{
		Central:   "home",
		Name:      "EnergyCounter",
		ValueType: hmenum.HubValueType("FLOAT"),
		Value:     42.5,
		Previous:  41.0,
	}
	if p.Central != "home" || p.Name != "EnergyCounter" {
		t.Fatalf("payload field round-trip failed: %+v", p)
	}
}

func TestProgramExecutedPayloadShape(t *testing.T) {
	p := ProgramExecutedPayload{
		Central:   "home",
		ProgramID: "42",
		Trigger:   hmenum.ProgramTrigger("MANUAL"),
		Success:   true,
	}
	if p.ProgramID != "42" || !p.Success {
		t.Fatalf("payload field round-trip failed: %+v", p)
	}
}
