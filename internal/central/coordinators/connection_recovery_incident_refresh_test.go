// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package coordinators

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestIncidentLogAppendAndHistory verifies ring buffer appends
// and returns entries correctly.
func TestIncidentLogAppendAndHistory(t *testing.T) {
	bus := events.NewBus()
	c := NewConnectionRecoveryCoordinator("main", bus)

	if entries := c.AllIncidents(); len(entries) != 0 {
		t.Fatalf("fresh coordinator: want 0 incidents, got %d", len(entries))
	}

	c.AppendIncident(IncidentEntry{
		InterfaceID: "HmIP-RF",
		Stage:       hmenum.RecoveryStageReconnecting,
		Reason:      hmenum.FailureReasonTimeout,
		Message:     "timeout after 30s",
	})
	c.AppendIncident(IncidentEntry{
		InterfaceID: "BidCos-RF",
		Stage:       hmenum.RecoveryStageFailed,
		Reason:      hmenum.FailureReasonAuth,
		Message:     "auth failure",
	})

	all := c.AllIncidents()
	if len(all) != 2 {
		t.Fatalf("expected 2 incidents, got %d", len(all))
	}
	// Oldest first.
	if all[0].InterfaceID != "HmIP-RF" || all[1].InterfaceID != "BidCos-RF" {
		t.Fatalf("order mismatch: %+v", all)
	}
}

// TestIncidentLogRingEviction verifies that the ring buffer caps at 100
// entries.
func TestIncidentLogRingEviction(t *testing.T) {
	bus := events.NewBus()
	c := NewConnectionRecoveryCoordinator("main", bus)

	for i := range 120 {
		c.AppendIncident(IncidentEntry{
			InterfaceID: "iface",
			Message:     string(rune('a' + i%26)),
		})
	}

	all := c.AllIncidents()
	if len(all) != incidentRingSize {
		t.Fatalf("ring exceeded cap: len=%d, want %d", len(all), incidentRingSize)
	}
}

// TestIncidentTimestampAutoFilled verifies that a zero Timestamp is
// filled in by AppendIncident.
func TestIncidentTimestampAutoFilled(t *testing.T) {
	bus := events.NewBus()
	c := NewConnectionRecoveryCoordinator("main", bus)
	before := time.Now()
	c.AppendIncident(IncidentEntry{InterfaceID: "x"})
	after := time.Now()

	all := c.AllIncidents()
	if all[0].Timestamp.Before(before) || all[0].Timestamp.After(after) {
		t.Fatalf("Timestamp not auto-filled: %v", all[0].Timestamp)
	}
}

// fakehubRefresher is a test double for HubRefresher.
type fakeHubRefresher struct {
	sysvarCalled       bool
	programCalled      bool
	systemUpdateCalled bool
	failSysvar         error
	failProgram        error
	failSystemUpdate   error
}

func (f *fakeHubRefresher) RefreshSystemUpdate(_ context.Context) error {
	f.systemUpdateCalled = true
	return f.failSystemUpdate
}

func (f *fakeHubRefresher) RefreshSysvars(_ context.Context) error {
	f.sysvarCalled = true
	return f.failSysvar
}

func (f *fakeHubRefresher) RefreshPrograms(_ context.Context) error {
	f.programCalled = true
	return f.failProgram
}

// TestRefreshHubDataAfterRecoveryNoRefresher verifies no-op
// when no HubRefresher is wired.
func TestRefreshHubDataAfterRecoveryNoRefresher(t *testing.T) {
	bus := events.NewBus()
	c := NewConnectionRecoveryCoordinator("main", bus)
	step := c.RefreshHubDataAfterRecovery()
	if err := step(context.Background()); err != nil {
		t.Fatalf("no-op step should return nil, got %v", err)
	}
}

// TestRefreshHubDataAfterRecoveryDelegates verifies all three refresh methods
// are called: SystemUpdate first, then Sysvars, then Programs.
func TestRefreshHubDataAfterRecoveryDelegates(t *testing.T) {
	bus := events.NewBus()
	c := NewConnectionRecoveryCoordinator("main", bus)
	fr := &fakeHubRefresher{}
	c.SetHubRefresher(fr)

	step := c.RefreshHubDataAfterRecovery()
	if err := step(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !fr.systemUpdateCalled || !fr.sysvarCalled || !fr.programCalled {
		t.Fatalf("not all refresh methods called: system_update=%v sysvars=%v programs=%v",
			fr.systemUpdateCalled, fr.sysvarCalled, fr.programCalled)
	}
}

// TestRefreshHubDataAfterRecoverySystemUpdateBestEffort verifies that a
// SystemUpdate (ReGa) failure is best-effort: the step still returns nil and
// continues on to Sysvars + Programs, so a transient hub-metadata failure
// cannot block the interface from reaching the ready state.
func TestRefreshHubDataAfterRecoverySystemUpdateBestEffort(t *testing.T) {
	bus := events.NewBus()
	c := NewConnectionRecoveryCoordinator("main", bus)
	fr := &fakeHubRefresher{failSystemUpdate: errors.New("system update rpc failed")}
	c.SetHubRefresher(fr)

	step := c.RefreshHubDataAfterRecovery()
	if err := step(context.Background()); err != nil {
		t.Fatalf("hub refresh must be best-effort, got error: %v", err)
	}
	if !fr.systemUpdateCalled || !fr.sysvarCalled || !fr.programCalled {
		t.Fatalf("a SystemUpdate failure must not stop the remaining refreshes: "+
			"system_update=%v sysvars=%v programs=%v",
			fr.systemUpdateCalled, fr.sysvarCalled, fr.programCalled)
	}
}

// TestRefreshHubDataAfterRecoveryContinuesOnSysvarError verifies that a
// Sysvars failure is best-effort too: the step returns nil and still reloads
// Programs. Every hub refresh is independent and self-heals on the periodic
// hub jobs.
func TestRefreshHubDataAfterRecoveryContinuesOnSysvarError(t *testing.T) {
	bus := events.NewBus()
	c := NewConnectionRecoveryCoordinator("main", bus)
	fr := &fakeHubRefresher{failSysvar: errors.New("sysvar rpc failed")}
	c.SetHubRefresher(fr)

	step := c.RefreshHubDataAfterRecovery()
	if err := step(context.Background()); err != nil {
		t.Fatalf("hub refresh must be best-effort, got error: %v", err)
	}
	if !fr.programCalled {
		t.Fatal("RefreshPrograms must still run after a Sysvars failure")
	}
}

// TestRefreshHubDataAfterRecoverySucceedsDespiteHubFailure is the regression
// test for the original symptom: when the post-recovery hub refresh hits a
// ReGa failure (all three calls fail here), the recovery pipeline must still
// reach RecoveryResultSuccess so the interface's already-enumerated devices
// become visible instead of staying hidden behind a failed data-loading stage.
func TestRefreshHubDataAfterRecoverySucceedsDespiteHubFailure(t *testing.T) {
	bus := events.NewBus()
	c := NewConnectionRecoveryCoordinator("main", bus)
	fr := &fakeHubRefresher{
		failSystemUpdate: errors.New("rega get_system_update_info timeout"),
		failSysvar:       errors.New("rega sysvar timeout"),
		failProgram:      errors.New("rega program timeout"),
	}
	c.SetHubRefresher(fr)

	pipeline := []Pipeline{
		{Stage: hmenum.RecoveryStageReconnecting, Run: func(_ context.Context) error { return nil }},
		{Stage: hmenum.RecoveryStageDataLoading, Run: c.RefreshHubDataAfterRecovery()},
	}
	if result := c.Run(context.Background(), "HmIP-RF", pipeline); result != hmenum.RecoveryResultSuccess {
		t.Fatalf("recovery must succeed despite hub-refresh failures, got %v", result)
	}
}

// TestRefreshHubDataAfterRecoveryAsLastPipelineStage verifies that the
// step can be added to a [Pipeline] and runs on successful recovery.
func TestRefreshHubDataAfterRecoveryAsLastPipelineStage(t *testing.T) {
	bus := events.NewBus()
	c := NewConnectionRecoveryCoordinator("main", bus)
	fr := &fakeHubRefresher{}
	c.SetHubRefresher(fr)

	pipeline := []Pipeline{
		{Stage: hmenum.RecoveryStageReconnecting, Run: func(_ context.Context) error { return nil }},
		{Stage: hmenum.RecoveryStageDataLoading, Run: c.RefreshHubDataAfterRecovery()},
	}

	result := c.Run(context.Background(), "HmIP-RF", pipeline)
	if result != hmenum.RecoveryResultSuccess {
		t.Fatalf("expected success, got %v", result)
	}
	if !fr.sysvarCalled || !fr.programCalled {
		t.Fatalf("hub refresh not called: sysvars=%v programs=%v", fr.sysvarCalled, fr.programCalled)
	}
}
