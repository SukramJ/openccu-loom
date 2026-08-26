// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// closed_db_test.go exercises error-return branches in every store type by
// calling store methods against a closed *sql.DB. The pattern is:
//   1. Open a real DB (migrations applied).
//   2. Close the DB handle immediately.
//   3. Construct a store with the closed handle.
//   4. Call each method — every call must return a non-nil error.
//
// This approach is the only practical way to cover the `fmt.Errorf(...)` error
// branches inside every query/exec path without mocking the driver.

package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/client/reliability"
	"github.com/SukramJ/openccu-loom/internal/store/session"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// closedDB returns a *sql.DB that has been migrated and immediately closed.
func closedDB(t *testing.T) *closedStores {
	t.Helper()
	db := openTestDB(t, "closed.db")
	// Close the DB — all subsequent SQL calls will fail with
	// "sql: database is closed".
	_ = db.Close()
	return &closedStores{
		devices:    NewDeviceStore(db),
		paramsets:  NewParamsetStore(db),
		incidents:  NewIncidentStore(db),
		sessions:   NewSessionRecorderStore(db),
		audit:      NewAuditStore(db),
		visibility: NewVisibilityUnIgnoreStore(db),
	}
}

type closedStores struct {
	devices    *DeviceStore
	paramsets  *ParamsetStore
	incidents  *IncidentStore
	sessions   *SessionRecorderStore
	audit      *AuditStore
	visibility *VisibilityUnIgnoreStore
}

// ── DeviceStore ───────────────────────────────────────────────────────────────

func TestDeviceStore_ClosedDB_Upsert(t *testing.T) {
	t.Parallel()
	s := closedDB(t)
	err := s.devices.Upsert(context.Background(), baseDeviceRecord("c", "iface", "A"))
	if err == nil {
		t.Error("Upsert on closed DB: want error, got nil")
	}
}

func TestDeviceStore_ClosedDB_Get(t *testing.T) {
	t.Parallel()
	s := closedDB(t)
	_, err := s.devices.Get(context.Background(), "c", "iface", "A")
	if err == nil {
		t.Error("Get on closed DB: want error, got nil")
	}
}

func TestDeviceStore_ClosedDB_Delete(t *testing.T) {
	t.Parallel()
	s := closedDB(t)
	_, err := s.devices.Delete(context.Background(), "c", "iface", "A")
	if err == nil {
		t.Error("Delete on closed DB: want error, got nil")
	}
}

func TestDeviceStore_ClosedDB_Size(t *testing.T) {
	t.Parallel()
	s := closedDB(t)
	_, err := s.devices.Size(context.Background(), "c")
	if err == nil {
		t.Error("Size on closed DB: want error, got nil")
	}
}

func TestDeviceStore_ClosedDB_FindDeviceDescription(t *testing.T) {
	t.Parallel()
	s := closedDB(t)
	_, err := s.devices.FindDeviceDescription(context.Background(), "c", "iface", "A")
	if err == nil {
		t.Error("FindDeviceDescription on closed DB: want error, got nil")
	}
}

func TestDeviceStore_ClosedDB_GetAddresses(t *testing.T) {
	t.Parallel()
	s := closedDB(t)
	_, err := s.devices.GetAddresses(context.Background(), "c", "iface")
	if err == nil {
		t.Error("GetAddresses on closed DB: want error, got nil")
	}
}

func TestDeviceStore_ClosedDB_GetDeviceWithChannels(t *testing.T) {
	t.Parallel()
	s := closedDB(t)
	_, err := s.devices.GetDeviceWithChannels(context.Background(), "c", "iface", "A")
	if err == nil {
		t.Error("GetDeviceWithChannels on closed DB: want error, got nil")
	}
}

func TestDeviceStore_ClosedDB_GetInterfaceIDs(t *testing.T) {
	t.Parallel()
	s := closedDB(t)
	_, err := s.devices.GetInterfaceIDs(context.Background(), "c")
	if err == nil {
		t.Error("GetInterfaceIDs on closed DB: want error, got nil")
	}
}

func TestDeviceStore_ClosedDB_GetModel(t *testing.T) {
	t.Parallel()
	s := closedDB(t)
	_, err := s.devices.GetModel(context.Background(), "c", "A")
	if err == nil {
		t.Error("GetModel on closed DB: want error, got nil")
	}
}

func TestDeviceStore_ClosedDB_HasDeviceDescriptions(t *testing.T) {
	t.Parallel()
	s := closedDB(t)
	_, err := s.devices.HasDeviceDescriptions(context.Background(), "c", "iface")
	if err == nil {
		t.Error("HasDeviceDescriptions on closed DB: want error, got nil")
	}
}

func TestDeviceStore_ClosedDB_Clear(t *testing.T) {
	t.Parallel()
	s := closedDB(t)
	_, err := s.devices.Clear(context.Background(), "c", "iface")
	if err == nil {
		t.Error("Clear on closed DB: want error, got nil")
	}
}

func TestDeviceStore_ClosedDB_ListByInterface(t *testing.T) {
	t.Parallel()
	s := closedDB(t)
	_, err := s.devices.ListByInterface(context.Background(), "c", "iface")
	if err == nil {
		t.Error("ListByInterface on closed DB: want error, got nil")
	}
}

// ── AuditStore ────────────────────────────────────────────────────────────────

func TestAuditStore_ClosedDB_Append(t *testing.T) {
	t.Parallel()
	s := closedDB(t)
	err := s.audit.Append(context.Background(), audit.Entry{
		Action: "test_action",
		User:   "testuser",
	})
	if err == nil {
		t.Error("Append on closed DB: want error, got nil")
	}
}

func TestAuditStore_ClosedDB_List(t *testing.T) {
	t.Parallel()
	s := closedDB(t)
	_, err := s.audit.List(context.Background(), "", 10)
	if err == nil {
		t.Error("List on closed DB: want error, got nil")
	}
}

// ── IncidentStore ─────────────────────────────────────────────────────────────

func TestIncidentStore_ClosedDB_Record(t *testing.T) {
	t.Parallel()
	s := closedDB(t)
	_, err := s.incidents.Record(context.Background(), Incident{
		CentralName: "c",
		Type:        hmenum.IncidentTypeAuthFailure,
		Severity:    hmenum.IncidentSeverityError,
		Message:     "test",
	})
	if err == nil {
		t.Error("Record on closed DB: want error, got nil")
	}
}

func TestIncidentStore_ClosedDB_BumpIfRecent(t *testing.T) {
	t.Parallel()
	s := closedDB(t)
	_, err := s.incidents.BumpIfRecent(context.Background(), Incident{
		CentralName: "c",
		Type:        hmenum.IncidentTypeAuthFailure,
		Message:     "test",
	}, time.Hour)
	if err == nil {
		t.Error("BumpIfRecent on closed DB: want error, got nil")
	}
}

func TestIncidentStore_ClosedDB_GetAllIncidents(t *testing.T) {
	t.Parallel()
	s := closedDB(t)
	_, err := s.incidents.GetAllIncidents(context.Background(), "c")
	if err == nil {
		t.Error("GetAllIncidents on closed DB: want error, got nil")
	}
}

func TestIncidentStore_ClosedDB_GetDiagnostics(t *testing.T) {
	t.Parallel()
	s := closedDB(t)
	_, err := s.incidents.GetDiagnostics(context.Background(), "c", DefaultMaxPerType, DefaultMaxAgeDays)
	if err == nil {
		t.Error("GetDiagnostics on closed DB: want error, got nil")
	}
}

func TestIncidentStore_ClosedDB_GetIncidentsByType(t *testing.T) {
	t.Parallel()
	s := closedDB(t)
	_, err := s.incidents.GetIncidentsByType(context.Background(), "c", hmenum.IncidentTypeAuthFailure)
	if err == nil {
		t.Error("GetIncidentsByType on closed DB: want error, got nil")
	}
}

func TestIncidentStore_ClosedDB_PurgeOld(t *testing.T) {
	t.Parallel()
	s := closedDB(t)
	_, err := s.incidents.PurgeOld(context.Background(), "c", DefaultMaxAgeDays)
	if err == nil {
		t.Error("PurgeOld on closed DB: want error, got nil")
	}
}

func TestIncidentStore_ClosedDB_EnforcePerTypeCap(t *testing.T) {
	t.Parallel()
	s := closedDB(t)
	err := s.incidents.EnforcePerTypeCap(context.Background(), "c", DefaultMaxPerType)
	if err == nil {
		t.Error("EnforcePerTypeCap on closed DB: want error, got nil")
	}
}

func TestIncidentStore_ClosedDB_GetIncidentsByInterface(t *testing.T) {
	t.Parallel()
	s := closedDB(t)
	_, err := s.incidents.GetIncidentsByInterface(context.Background(), "c", "HmIP-RF")
	if err == nil {
		t.Error("GetIncidentsByInterface on closed DB: want error, got nil")
	}
}

func TestIncidentStore_ClosedDB_IncidentCount(t *testing.T) {
	t.Parallel()
	s := closedDB(t)
	_, err := s.incidents.IncidentCount(context.Background(), "c")
	if err == nil {
		t.Error("IncidentCount on closed DB: want error, got nil")
	}
}

func TestIncidentStore_ClosedDB_ClearIncidents(t *testing.T) {
	t.Parallel()
	s := closedDB(t)
	err := s.incidents.ClearIncidents(context.Background(), "c")
	if err == nil {
		t.Error("ClearIncidents on closed DB: want error, got nil")
	}
}

func TestIncidentStore_ClosedDB_RecordWithLimits(t *testing.T) {
	t.Parallel()
	s := closedDB(t)
	_, err := s.incidents.RecordWithLimits(context.Background(), Incident{
		CentralName: "c",
		Type:        hmenum.IncidentTypeAuthFailure,
		Severity:    hmenum.IncidentSeverityError,
		Message:     "test",
	}, DefaultMaxAgeDays, DefaultMaxPerType)
	if err == nil {
		t.Error("RecordWithLimits on closed DB: want error, got nil")
	}
}

func TestIncidentStore_ClosedDB_RecordIncident(t *testing.T) {
	t.Parallel()
	s := closedDB(t)
	err := s.incidents.RecordIncident(context.Background(), reliability.IncidentRecord{
		InterfaceID: "HmIP-RF",
	})
	if err == nil {
		t.Error("RecordIncident on closed DB: want error, got nil")
	}
}

func TestIncidentStore_ClosedDB_Recent(t *testing.T) {
	t.Parallel()
	s := closedDB(t)
	_, err := s.incidents.Recent(context.Background(), "c", 10)
	if err == nil {
		t.Error("Recent on closed DB: want error, got nil")
	}
}

// ── ParamsetStore ─────────────────────────────────────────────────────────────

func freshParamset() ParamsetRecord {
	return ParamsetRecord{
		CentralName:    "c",
		InterfaceID:    "HmIP-RF",
		ChannelAddress: "ABC:1",
		ParamsetKey:    hmenum.ParamsetKeyValues,
		Hash:           "h1",
		Paramset: hmproto.Paramset{
			"LEVEL": hmproto.ParameterData{Type: hmenum.ParameterTypeFloat},
		},
	}
}

func TestParamsetStore_ClosedDB_Upsert(t *testing.T) {
	t.Parallel()
	s := closedDB(t)
	err := s.paramsets.Upsert(context.Background(), freshParamset())
	if err == nil {
		t.Error("Upsert on closed DB: want error, got nil")
	}
}

func TestParamsetStore_ClosedDB_Get(t *testing.T) {
	t.Parallel()
	s := closedDB(t)
	_, err := s.paramsets.Get(context.Background(), "c", "HmIP-RF", "ABC:1", hmenum.ParamsetKeyValues)
	if err == nil {
		t.Error("Get on closed DB: want error, got nil")
	}
}

func TestParamsetStore_ClosedDB_Size(t *testing.T) {
	t.Parallel()
	s := closedDB(t)
	_, err := s.paramsets.Size(context.Background(), "c")
	if err == nil {
		t.Error("Size on closed DB: want error, got nil")
	}
}

func TestParamsetStore_ClosedDB_GetChannelParamsetDescriptions(t *testing.T) {
	t.Parallel()
	s := closedDB(t)
	_, err := s.paramsets.GetChannelParamsetDescriptions(context.Background(), "c", "HmIP-RF", "ABC:1")
	if err == nil {
		t.Error("GetChannelParamsetDescriptions on closed DB: want error, got nil")
	}
}

func TestParamsetStore_ClosedDB_GetParamsetKeys(t *testing.T) {
	t.Parallel()
	s := closedDB(t)
	_, err := s.paramsets.GetParamsetKeys(context.Background(), "c", "HmIP-RF", "ABC:1")
	if err == nil {
		t.Error("GetParamsetKeys on closed DB: want error, got nil")
	}
}

func TestParamsetStore_ClosedDB_HasInterfaceID(t *testing.T) {
	t.Parallel()
	s := closedDB(t)
	_, err := s.paramsets.HasInterfaceID(context.Background(), "c", "HmIP-RF")
	if err == nil {
		t.Error("HasInterfaceID on closed DB: want error, got nil")
	}
}

func TestParamsetStore_ClosedDB_HasParameter(t *testing.T) {
	t.Parallel()
	s := closedDB(t)
	_, err := s.paramsets.HasParameter(context.Background(), "c", "HmIP-RF", "ABC:1", hmenum.ParamsetKeyValues, "LEVEL")
	if err == nil {
		t.Error("HasParameter on closed DB: want error, got nil")
	}
}

func TestParamsetStore_ClosedDB_GetParameterData(t *testing.T) {
	t.Parallel()
	s := closedDB(t)
	_, err := s.paramsets.GetParameterData(context.Background(), "c", "HmIP-RF", "ABC:1", hmenum.ParamsetKeyValues, "LEVEL")
	if err == nil {
		t.Error("GetParameterData on closed DB: want error, got nil")
	}
}

func TestParamsetStore_ClosedDB_GetChannelAddressesByParamsetKey(t *testing.T) {
	t.Parallel()
	s := closedDB(t)
	_, err := s.paramsets.GetChannelAddressesByParamsetKey(context.Background(), "c", "HmIP-RF", "ABC")
	if err == nil {
		t.Error("GetChannelAddressesByParamsetKey on closed DB: want error, got nil")
	}
}

func TestParamsetStore_ClosedDB_ClearForInterface(t *testing.T) {
	t.Parallel()
	s := closedDB(t)
	_, err := s.paramsets.ClearForInterface(context.Background(), "c", "HmIP-RF")
	if err == nil {
		t.Error("ClearForInterface on closed DB: want error, got nil")
	}
}

func TestParamsetStore_ClosedDB_DeleteChannel(t *testing.T) {
	t.Parallel()
	s := closedDB(t)
	err := s.paramsets.DeleteChannel(context.Background(), "c", "HmIP-RF", "ABC:1")
	if err == nil {
		t.Error("DeleteChannel on closed DB: want error, got nil")
	}
}

func TestParamsetStore_ClosedDB_WipeOutdated(t *testing.T) {
	t.Parallel()
	s := closedDB(t)
	_, err := s.paramsets.WipeOutdated(context.Background())
	if err == nil {
		t.Error("WipeOutdated on closed DB: want error, got nil")
	}
}

// ── SessionRecorderStore ──────────────────────────────────────────────────────

func TestSessionRecorderStore_ClosedDB_PersistAll(t *testing.T) {
	t.Parallel()
	s := closedDB(t)
	err := s.sessions.PersistAll(context.Background(), "c", "slug", []session.PersistRow{
		{Method: "test", RecordedAt: time.Now()},
	})
	if err == nil {
		t.Error("PersistAll on closed DB: want error, got nil")
	}
}

func TestSessionRecorderStore_ClosedDB_Load(t *testing.T) {
	t.Parallel()
	s := closedDB(t)
	_, err := s.sessions.Load(context.Background(), "c", "slug", 10)
	if err == nil {
		t.Error("Load on closed DB: want error, got nil")
	}
}

func TestSessionRecorderStore_ClosedDB_DeleteAll(t *testing.T) {
	t.Parallel()
	s := closedDB(t)
	err := s.sessions.DeleteAll(context.Background(), "c", "slug")
	if err == nil {
		t.Error("DeleteAll on closed DB: want error, got nil")
	}
}

func TestSessionRecorderStore_ClosedDB_CountEntries(t *testing.T) {
	t.Parallel()
	s := closedDB(t)
	_, err := s.sessions.CountEntries(context.Background(), "c", "slug")
	if err == nil {
		t.Error("CountEntries on closed DB: want error, got nil")
	}
}

// ── VisibilityUnIgnoreStore ───────────────────────────────────────────────────

func TestVisibilityUnIgnoreStore_ClosedDB_List(t *testing.T) {
	t.Parallel()
	s := closedDB(t)
	_, err := s.visibility.List(context.Background(), "c")
	if err == nil {
		t.Error("List on closed DB: want error, got nil")
	}
}

// ── nil-receiver guards ───────────────────────────────────────────────────────

func TestVisibilityUnIgnoreStore_NilReceiver_List(t *testing.T) {
	t.Parallel()
	var s *VisibilityUnIgnoreStore
	entries, err := s.List(context.Background(), "c")
	if err != nil || entries != nil {
		t.Errorf("nil receiver List: want (nil, nil), got (%v, %v)", entries, err)
	}
}

func TestVisibilityUnIgnoreStore_NilReceiver_Replace(t *testing.T) {
	t.Parallel()
	var s *VisibilityUnIgnoreStore
	if err := s.Replace(context.Background(), "c", []string{"MODEL:CH:P"}, "user"); err != nil {
		t.Errorf("nil receiver Replace: want nil, got %v", err)
	}
}

func TestVisibilityUnIgnoreStore_NilReceiver_SeedIfEmpty(t *testing.T) {
	t.Parallel()
	var s *VisibilityUnIgnoreStore
	if err := s.SeedIfEmpty(context.Background(), "c", []string{"M:C:P"}); err != nil {
		t.Errorf("nil receiver SeedIfEmpty: want nil, got %v", err)
	}
}

func TestAuditStore_NilReceiver_Append(t *testing.T) {
	t.Parallel()
	var s *AuditStore
	if err := s.Append(context.Background(), audit.Entry{}); err != nil {
		t.Errorf("nil receiver Append: want nil, got %v", err)
	}
}

func TestAuditStore_NilReceiver_List(t *testing.T) {
	t.Parallel()
	var s *AuditStore
	entries, err := s.List(context.Background(), "", 10)
	if err != nil || entries != nil {
		t.Errorf("nil receiver List: want (nil, nil), got (%v, %v)", entries, err)
	}
}

func TestVisibilityUnIgnoreStore_ClosedDB_Patterns(t *testing.T) {
	t.Parallel()
	s := closedDB(t)
	_, err := s.visibility.Patterns(context.Background(), "c")
	if err == nil {
		t.Error("Patterns on closed DB: want error, got nil")
	}
}

func TestVisibilityUnIgnoreStore_ClosedDB_Replace(t *testing.T) {
	t.Parallel()
	s := closedDB(t)
	err := s.visibility.Replace(context.Background(), "c", []string{"MODEL:CH:PARAM"}, "user")
	if err == nil {
		t.Error("Replace on closed DB: want error, got nil")
	}
}

func TestVisibilityUnIgnoreStore_ClosedDB_SeedIfEmpty(t *testing.T) {
	t.Parallel()
	s := closedDB(t)
	err := s.visibility.SeedIfEmpty(context.Background(), "c", []string{"MODEL:CH:PARAM"})
	if err == nil {
		t.Error("SeedIfEmpty on closed DB: want error, got nil")
	}
}
