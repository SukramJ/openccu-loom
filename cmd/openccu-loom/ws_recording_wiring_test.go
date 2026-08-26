// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"log/slog"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/north/rest/ws"
)

// TestWSRecordingCommandsSurviveABootWithoutCentrals pins the recording.*
// command family against the first-run install.
//
// WS commands are registered once, at boot. Deciding whether to register the
// family by probing the registry for a central that already exposes a recorder
// answers nothing about the recorder — every central.New builds one — and
// everything about whether any CCU exists yet. On a fresh install the operator
// adds the first CCU through the onboarding wizard, which adopts it live, and
// recording.start/stop/status kept answering unknown_command until the daemon
// was restarted.
func TestWSRecordingCommandsSurviveABootWithoutCentrals(t *testing.T) {
	t.Parallel()
	hub := ws.NewHub()
	reg := central.NewRegistry() // fresh install: no centrals configured

	wireWSCommands(hub, wsCommandWiring{
		registry: reg,
		logger:   slog.New(slog.DiscardHandler),
	})

	router := hub.Router()
	for _, cmd := range []string{"recording.start", "recording.stop", "recording.status"} {
		if !router.Has(cmd) {
			t.Fatalf("%s is unregistered after a boot with no centrals — it answers unknown_command "+
				"for the whole run, including after the wizard adopts the first CCU", cmd)
		}
	}

	// Before any CCU exists the family answers, rather than 404s: nothing is
	// recording.
	res := router.Dispatch(context.Background(), "recording.status", nil)
	if res.Error != nil {
		t.Fatalf("recording.status on an empty fleet: %+v", res.Error)
	}

	// A CCU adopted at runtime is picked up without re-registering commands:
	// the recorder re-walks the registry per call.
	unit, err := central.New(central.Config{Name: "ccu-wizard"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	if err := reg.Register(unit); err != nil {
		t.Fatalf("Register: %v", err)
	}
	// recording.start is admin-gated, matching the REST write route; dispatch
	// it with a resolved admin identity.
	adminCtx := auth.ContextWithIdentity(context.Background(),
		auth.Identity{Subject: "admin", Role: auth.RoleAdmin})
	if res := router.Dispatch(adminCtx, "recording.start", nil); res.Error != nil {
		t.Fatalf("recording.start after a runtime adopt: %+v", res.Error)
	}
	if !unit.Recorder.IsActive() {
		t.Error("recording.start did not reach the adopted central's recorder")
	}
}

// TestWSRecordingStartWritesAnAuditRowAndArmsTheAutoStop pins that a WS
// recording.start goes through the same recorder domain method the REST
// /diagnostics/rpc-recording route uses: it writes an audit row (with the
// operator identity from the command context) and arms the auto-stop safety
// timer. The old path poked every central's recorder directly and did
// neither, so a WS-armed recording had no audit trail and could run forever.
func TestWSRecordingStartWritesAnAuditRowAndArmsTheAutoStop(t *testing.T) {
	t.Parallel()
	hub := ws.NewHub()
	reg := central.NewRegistry()
	unit, err := central.New(central.Config{Name: "ccu-audit"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	if err := reg.Register(unit); err != nil {
		t.Fatalf("Register: %v", err)
	}

	// The shared recorder instance the WS commands drive; querying it back
	// proves the auto-stop timer was armed (EndsAt set).
	rpcRecorder := adapter.NewRPCRecorderAdapter(reg, "")
	auditBuf := audit.NewBuffer(16)
	t.Cleanup(func() { rpcRecorder.Stop(nil) }) // cancel the armed timer

	wireWSCommands(hub, wsCommandWiring{
		registry:    reg,
		rpcRecorder: rpcRecorder,
		auditRec:    auditBuf,
		logger:      slog.New(slog.DiscardHandler),
	})

	adminCtx := auth.ContextWithIdentity(context.Background(),
		auth.Identity{Subject: "admin", Role: auth.RoleAdmin})
	if res := hub.Router().Dispatch(adminCtx, "recording.start", nil); res.Error != nil {
		t.Fatalf("recording.start: %+v", res.Error)
	}

	// Audit row written, attributed to the requesting operator.
	rows := auditBuf.List(0)
	var found bool
	for _, e := range rows {
		if e.Action == "diagnostics.rpc_recording_start" {
			found = true
			if e.User != "admin" {
				t.Errorf("audit row user = %q, want the requesting operator %q", e.User, "admin")
			}
		}
	}
	if !found {
		t.Errorf("recording.start wrote no diagnostics.rpc_recording_start audit row; got %d rows", len(rows))
	}

	// Auto-stop timer armed: the shared recorder reports a deadline.
	var armed bool
	for _, s := range rpcRecorder.Status() {
		if s.Central == "ccu-audit" && s.EndsAt != "" {
			armed = true
		}
	}
	if !armed {
		t.Error("recording.start did not arm the auto-stop timer (no EndsAt on the recorder status)")
	}
}
