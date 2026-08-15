// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"log/slog"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/central"
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
	// recording.start is role-gated like the REST write path, so dispatch it
	// with a resolved operator identity.
	opCtx := auth.ContextWithIdentity(context.Background(),
		auth.Identity{Subject: "operator", Role: auth.RoleOperator})
	if res := router.Dispatch(opCtx, "recording.start", nil); res.Error != nil {
		t.Fatalf("recording.start after a runtime adopt: %+v", res.Error)
	}
	if !unit.Recorder.IsActive() {
		t.Error("recording.start did not reach the adopted central's recorder")
	}
}
