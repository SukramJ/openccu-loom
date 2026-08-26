// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"testing"
	"time"

	gosql "database/sql"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client/reliability"
	"github.com/SukramJ/openccu-loom/internal/store/session"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestSeamEffect_IncidentRecorder_ReachesTheStoreForALaterCentral asserts
// what the audit.incident_recorder seam's Why claims: that a central's
// reliability incident lands where GET /incidents reads it.
//
// The central is registered AFTER the wiring runs. A seam that walked the
// registry once at wiring time would pass a test that registered first and
// fail every operator who adopts a CCU at runtime, so registering late is
// the only ordering that tells the two apart.
func TestSeamEffect_IncidentRecorder_ReachesTheStoreForALaterCentral(t *testing.T) {
	db := openMigratedTestDB(t, "seam_effect_incident.db")
	reg := central.NewRegistry()

	store, teardown := wireIncidentRecorder(db, reg, discardTestLogger())
	t.Cleanup(teardown)
	if store == nil {
		t.Fatal("wireIncidentRecorder returned no store — there would be nothing to read back")
	}

	unit := registerSeamEffectCentral(t, reg, "late-central")
	if unit.Cache == nil {
		t.Fatal("the registered central has no cache coordinator — the seam's collaborator " +
			"has nowhere to attach, so this test would measure the fixture")
	}

	rec := unit.Cache.GetIncidentRecorder()
	if rec == nil {
		t.Fatal("no incident recorder reached the central's cache coordinator: a reliability " +
			"incident is dropped, GET /incidents stays empty, and no IncidentRecordedEvent " +
			"reaches the webhook bridge")
	}
	if err := rec.RecordIncident(context.Background(), reliability.IncidentRecord{
		CentralName: "late-central",
		InterfaceID: "late-central-HmIP-RF",
		Type:        hmenum.IncidentTypePingPongMismatch,
		Severity:    hmenum.IncidentSeverityWarning,
		Message:     "seam effect probe",
	}); err != nil {
		t.Fatalf("record incident: %v", err)
	}

	rows, err := store.GetAllIncidents(context.Background(), "late-central")
	if err != nil {
		t.Fatalf("read incidents: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("store holds %d incident(s), want 1 — the recorder is attached but what it "+
			"records does not reach the store the REST surface reads", len(rows))
	}
}

// TestSeamEffect_SessionRecorderPersistence_SurvivesALaterCentral asserts
// the audit.session_recorder_persistence seam's Why: that a central's
// recorded session is written somewhere a support bundle can find it after
// a restart.
//
// The store is seeded directly and read back through the central, because
// that is the direction the Why is about. A store written but never read
// back leaves the bundle just as empty, and ReloadRecorderFromPersistence
// returns without doing anything when no store was wired — nothing else
// sets that field, so a session that comes back is attributable to this
// seam and to nothing else.
func TestSeamEffect_SessionRecorderPersistence_SurvivesALaterCentral(t *testing.T) {
	db := openMigratedTestDB(t, "seam_effect_session.db")
	reg := central.NewRegistry()

	teardown := wireSessionRecorderPersistence(db, reg, discardTestLogger())
	t.Cleanup(teardown)

	unit := registerSeamEffectCentral(t, reg, "late-central")
	if unit.Recorder == nil {
		t.Fatal("the registered central has no session recorder — the seam has nothing to " +
			"persist, so this test would measure the fixture")
	}

	seedPersistedSession(t, db, "late-central")

	unit.ReloadRecorderFromPersistence(context.Background())
	if len(unit.Recorder.GetSessions()) == 0 {
		t.Error("a persisted session did not come back through the central: no store reached " +
			"its recorder, so what the CCU said is written nowhere and a support bundle " +
			"taken after a restart has no trace of it")
	}
}

// TestSeamEffect_SessionRecorderPersistence_IsAttributableToTheSeam is the
// negative control: with the wiring not run, the same seeded session must
// NOT come back. Without it, the assertion above would also pass on a
// recorder that loaded from somewhere else.
func TestSeamEffect_SessionRecorderPersistence_IsAttributableToTheSeam(t *testing.T) {
	db := openMigratedTestDB(t, "seam_effect_session_control.db")
	reg := central.NewRegistry()

	unit := registerSeamEffectCentral(t, reg, "late-central")
	seedPersistedSession(t, db, "late-central")

	unit.ReloadRecorderFromPersistence(context.Background())
	if len(unit.Recorder.GetSessions()) != 0 {
		t.Error("the session came back without the seam being wired — something else reads " +
			"the store, so the test above proves nothing about this seam")
	}
}

// seedPersistedSession writes one row straight into the persistence store,
// standing in for a session recorded before the restart.
func seedPersistedSession(t *testing.T, db *gosql.DB, centralName string) {
	t.Helper()

	store := sqlitestore.NewSessionRecorderStore(db)
	err := store.PersistAll(context.Background(), centralName, "default", []session.PersistRow{{
		CentralName:  centralName,
		Slug:         "default",
		RPCType:      string(session.RPCTypeXML),
		Method:       "listDevices",
		FrozenParams: "[]",
		ResponseJSON: "[]",
		RecordedAt:   time.Now(),
		TTLSeconds:   3600,
	}})
	if err != nil {
		t.Fatalf("seed persisted session: %v", err)
	}
}

// registerSeamEffectCentral registers a central after the wiring under
// test has already run, which is the ordering every per-central seam has
// to survive.
func registerSeamEffectCentral(t *testing.T, reg *central.Registry, name string) *central.Unit {
	t.Helper()

	unit, err := central.New(central.Config{Name: name})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	if err := reg.Register(unit); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}
	return unit
}
