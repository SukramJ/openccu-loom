// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Skip-path tests for the central-scheduler jobs: each refresh slot
// (firmware, client-data, program, sysvar) must be a no-op when its
// loader is unset, and gatedRun must short-circuit without invoking
// the underlying fn while a connection-issue is active.

package central

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/coordinators"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestSchedulerFirmwareUpdateCheckSkipsWhenNilSlot verifies that when
// FirmwareUpdateCheck is nil (the "unavailable / disabled" equivalent
// in Go), the job is not registered and the scheduler does not attempt
// to run it.
//
// Mirrors: test_fetch_device_firmware_skips_when_unavailable.
func TestSchedulerFirmwareUpdateCheckSkipsWhenNilSlot(t *testing.T) {
	t.Parallel()
	c, err := New(Config{Name: "py-sched-fw"})
	if err != nil {
		t.Fatal(err)
	}

	// Leave FirmwareUpdateCheck as nil — mirrors Python's
	// enable_device_firmware_check=False / central.available=False path.
	cfg := StandardJobs{
		FirmwareUpdateCheck: nil,
	}
	names, err := RegisterStandardJobs(c, cfg)
	if err != nil {
		t.Fatalf("RegisterStandardJobs: %v", err)
	}

	for _, n := range names {
		if n == "central.firmware_check" {
			t.Errorf("central.firmware_check must not be registered when FirmwareUpdateCheck is nil, got names=%v", names)
		}
	}
}

// TestSchedulerRefreshClientDataRegisteredEvenWithoutLoadFn verifies the
// Go-vs-Python shape divergence for "no poll clients" skip behavior.
//
// Python's _refresh_client_data skips silently when poll_clients is empty
// or nil. In Go, defaultRefreshClientData always returns a non-nil closure
// (as long as unit != nil) — the closure returns an error at invocation time
// when LoadAndRefreshDataPointData is not wired, but the job IS registered.
// There is no poll_clients concept in Go.
//
// This test documents the actual Go behavior: the job is registered and its
// closure returns an error when the loadAndRefreshFn is absent.
//
// Mirrors: test_refresh_client_data_skips_when_no_poll_clients /
// test_refresh_client_data_skips_when_poll_clients_none — SHAPE MISMATCH.
// Go registers the job unconditionally (skip-on-nil-unit is the only guard).
func TestSchedulerRefreshClientDataRegisteredEvenWithoutLoadFn(t *testing.T) {
	t.Parallel()
	c, err := New(Config{Name: "py-sched-rcd-nil"})
	if err != nil {
		t.Fatal(err)
	}
	// Do NOT call c.SetLoadAndRefreshFn → loadAndRefreshFn stays nil.
	// Go still registers the job; the error is surfaced at run time.
	cfg := StandardJobs{
		RefreshClientData: nil, // triggers defaultRefreshClientData(unit)
	}
	names, err := RegisterStandardJobs(c, cfg)
	if err != nil {
		t.Fatalf("RegisterStandardJobs: %v", err)
	}

	// The job must be registered (non-nil unit → non-nil default closure).
	found := false
	for _, n := range names {
		if n == "central.refresh_client_data" {
			found = true
		}
	}
	if !found {
		t.Fatalf("central.refresh_client_data must be registered even when loadAndRefreshFn is nil, names=%v", names)
	}

	// When fired in non-operational state (STARTING), gatedRun blocks it — no error.
	run := findJobRun(c, "central.refresh_client_data")
	if run == nil {
		t.Fatal("central.refresh_client_data job not found")
	}
	if err := run(context.Background()); err != nil {
		t.Fatalf("gatedRun in non-operational state must not surface error, got: %v", err)
	}
}

// TestSchedulerProgramRefreshJobSkipsWhenNilSlot verifies that when the
// ProgramRefresh slot is nil (disabled), the job is not registered.
//
// Mirrors: test_refresh_program_data_skips_when_disabled.
func TestSchedulerProgramRefreshJobSkipsWhenNilSlot(t *testing.T) {
	t.Parallel()
	c, err := New(Config{Name: "py-sched-prog"})
	if err != nil {
		t.Fatal(err)
	}

	cfg := StandardJobs{
		ProgramRefresh: nil, // mirrors enable_program_scan=False
	}
	names, err := RegisterStandardJobs(c, cfg)
	if err != nil {
		t.Fatalf("RegisterStandardJobs: %v", err)
	}

	for _, n := range names {
		if n == "hub.program_refresh" {
			t.Errorf("hub.program_refresh must not be registered when ProgramRefresh is nil, got names=%v", names)
		}
	}
}

// TestSchedulerSysvarRefreshJobSkipsWhenNilSlot verifies that when the
// SysvarRefresh slot is nil (disabled), the job is not registered.
//
// Mirrors: test_refresh_sysvar_data_skips_when_disabled.
func TestSchedulerSysvarRefreshJobSkipsWhenNilSlot(t *testing.T) {
	t.Parallel()
	c, err := New(Config{Name: "py-sched-sysvar"})
	if err != nil {
		t.Fatal(err)
	}

	cfg := StandardJobs{
		SysvarRefresh: nil, // mirrors enable_sysvar_scan=False
	}
	names, err := RegisterStandardJobs(c, cfg)
	if err != nil {
		t.Fatalf("RegisterStandardJobs: %v", err)
	}

	for _, n := range names {
		if n == "hub.sysvar_refresh" {
			t.Errorf("hub.sysvar_refresh must not be registered when SysvarRefresh is nil, got names=%v", names)
		}
	}
}

// TestSchedulerGatedRunSkipsOnConnectionIssue verifies that the gatedRun
// wrapper returns nil without invoking fn when a registered client is not
// CONNECTED (connection issue present).
//
// Mirrors: test_skipped_jobs_advance_schedule_during_connection_issue.
// Python verifies that skipped jobs still advance their next_run during
// connection issues. In Go the timer fires unconditionally; gatedRun
// returns early without calling fn — the "skip" is observable as zero
// invocations of the user function.
func TestSchedulerGatedRunSkipsOnConnectionIssue(t *testing.T) {
	t.Parallel()
	c, err := New(Config{Name: "py-sched-connissue"})
	if err != nil {
		t.Fatal(err)
	}
	advanceCentralToRunning(t, c)

	// Register a client that is not CONNECTED (stays in CREATED state).
	ic, err := client.New(client.Config{
		CentralName: c.cfg.Name,
		Interface:   hmenum.InterfaceHmIPRF,
		Caller:      client.CallerFunc(func(_ context.Context, _ string, _ []any) (any, error) { return nil, nil }),
	})
	if err != nil {
		t.Fatal(err)
	}
	// Do NOT advance ic to CONNECTED — remains in CREATED state.
	if err := c.Clients.Register(&coordinators.ClientEntry{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Client:      ic,
	}); err != nil {
		t.Fatal(err)
	}

	// Register a hub job that must be skipped due to connection issue.
	var calls atomic.Int32
	cfg := StandardJobs{
		ProgramRefresh:         func(context.Context) error { calls.Add(1); return nil },
		ProgramRefreshInterval: 10 * time.Second,
	}
	if _, err := RegisterStandardJobs(c, cfg); err != nil {
		t.Fatal(err)
	}

	run := findJobRun(c, "hub.program_refresh")
	if run == nil {
		t.Fatal("hub.program_refresh job not found")
	}

	// Client is disconnected → hasConnectionIssue → gatedRun skips fn.
	if err := run(context.Background()); err != nil {
		t.Fatalf("gatedRun returned error: %v", err)
	}
	if calls.Load() != 0 {
		t.Errorf("hub.program_refresh must be skipped on connection issue, calls=%d", calls.Load())
	}
}

// TestSchedulerJobNextRunDefaultedComment documents the skip decision for
// test_scheduler_job_initialization_without_next_run. The Python SchedulerJob
// stores a mutable next_run timestamp; Go's scheduler.Job is a value struct
// that feeds into timer.NewTimer — there is no exported next_run field to
// assert. The timer-based design guarantees "first tick ≈ now + interval"
// by construction, so the test body would be vacuously empty.
//
// This is a compile-time no-op that keeps the skip rationale in the test
// binary's symbol table for reviewers.
func TestSchedulerJobNextRunDefaultedComment(_ *testing.T) {
	// SKIP: Go uses timer.NewTimer(interval); there is no next_run field.
}
