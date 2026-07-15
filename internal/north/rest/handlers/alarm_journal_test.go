// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// seedJournalEntry appends a raw journal row directly through the
// store, bypassing the engine — the journal query tests only care
// about the store round trip and the handler's filter parsing.
func seedJournalEntry(t *testing.T, fx *alarmPanelFixture, areaID string, class hmenum.AlarmJournalClass, event string, when time.Time) int64 {
	t.Helper()
	id, err := fx.stores.Journal.Append(context.Background(), sqlitestore.AlarmJournalEntry{
		TsMS: when.UnixMilli(), AreaID: areaID, Class: class, Event: event,
	})
	if err != nil {
		t.Fatalf("seed journal entry: %v", err)
	}
	return id
}

func journalRequest(t *testing.T, query url.Values) *http.Request {
	t.Helper()
	target := "/api/v1/alarm/journal"
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	return httptest.NewRequest(http.MethodGet, target, http.NoBody)
}

func TestListAlarmJournal_FiltersByArea(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)
	base := alarmFixtureStart
	seedJournalEntry(t, fx, "eg", hmenum.AlarmJournalClassArm, "armed", base)
	seedJournalEntry(t, fx, "og", hmenum.AlarmJournalClassArm, "armed", base)

	q := url.Values{"area": {"eg"}}
	w := httptest.NewRecorder()
	ListAlarmJournal(fx).ServeHTTP(w, journalRequest(t, q))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	var body []hmapi.AlarmJournalEntry
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) != 1 || body[0].AreaID != "eg" {
		t.Fatalf("entries = %+v, want exactly one for area=eg", body)
	}
}

func TestListAlarmJournal_FiltersByClass(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)
	base := alarmFixtureStart
	seedJournalEntry(t, fx, "eg", hmenum.AlarmJournalClassArm, "armed", base)
	seedJournalEntry(t, fx, "eg", hmenum.AlarmJournalClassDisarm, "disarmed", base.Add(time.Minute))

	q := url.Values{"class": {string(hmenum.AlarmJournalClassDisarm)}}
	w := httptest.NewRecorder()
	ListAlarmJournal(fx).ServeHTTP(w, journalRequest(t, q))

	var body []hmapi.AlarmJournalEntry
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) != 1 || body[0].Class != string(hmenum.AlarmJournalClassDisarm) {
		t.Fatalf("entries = %+v, want exactly one disarm entry", body)
	}
}

func TestListAlarmJournal_InvalidClass_Returns400(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)

	q := url.Values{"class": {"not-a-real-class"}}
	w := httptest.NewRecorder()
	ListAlarmJournal(fx).ServeHTTP(w, journalRequest(t, q))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", w.Code, w.Body.String())
	}
}

// TestListAlarmJournal_FromToRFC3339 verifies the from/to bounds parse
// RFC3339 timestamps and scope the result to entries within [from, to).
func TestListAlarmJournal_FromToRFC3339(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)
	base := alarmFixtureStart
	seedJournalEntry(t, fx, "eg", hmenum.AlarmJournalClassTrigger, "t0", base)
	seedJournalEntry(t, fx, "eg", hmenum.AlarmJournalClassTrigger, "t0+1h", base.Add(time.Hour))
	seedJournalEntry(t, fx, "eg", hmenum.AlarmJournalClassTrigger, "t0+2h", base.Add(2*time.Hour))

	q := url.Values{
		"from": {base.Add(time.Hour).Format(time.RFC3339)},
		"to":   {base.Add(2 * time.Hour).Format(time.RFC3339)},
	}
	w := httptest.NewRecorder()
	ListAlarmJournal(fx).ServeHTTP(w, journalRequest(t, q))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	var body []hmapi.AlarmJournalEntry
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) != 1 || body[0].Event != "t0+1h" {
		t.Fatalf("entries = %+v, want only the t0+1h entry (to is exclusive)", body)
	}
}

func TestListAlarmJournal_InvalidFrom_Returns400(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)

	q := url.Values{"from": {"not-a-timestamp"}}
	w := httptest.NewRecorder()
	ListAlarmJournal(fx).ServeHTTP(w, journalRequest(t, q))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", w.Code, w.Body.String())
	}
}

func TestListAlarmJournal_InvalidTo_Returns400(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)

	q := url.Values{"to": {"not-a-timestamp"}}
	w := httptest.NewRecorder()
	ListAlarmJournal(fx).ServeHTTP(w, journalRequest(t, q))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", w.Code, w.Body.String())
	}
}

// TestListAlarmJournal_LimitClampedToMax verifies an oversized ?limit=
// is clamped rather than rejected or passed through unbounded.
func TestListAlarmJournal_LimitClampedToMax(t *testing.T) {
	t.Parallel()

	q := url.Values{"limit": {"999999"}}
	f, errMsg := parseAlarmJournalFilter(journalRequest(t, q))
	if errMsg != "" {
		t.Fatalf("parseAlarmJournalFilter: %v", errMsg)
	}
	if f.Limit != alarmJournalMaxLimit {
		t.Errorf("limit = %d, want clamped to %d", f.Limit, alarmJournalMaxLimit)
	}
}

func TestListAlarmJournal_DefaultLimitAppliedWhenAbsent(t *testing.T) {
	t.Parallel()
	f, errMsg := parseAlarmJournalFilter(journalRequest(t, nil))
	if errMsg != "" {
		t.Fatalf("parseAlarmJournalFilter: %v", errMsg)
	}
	if f.Limit != alarmJournalDefaultLimit {
		t.Errorf("limit = %d, want default %d", f.Limit, alarmJournalDefaultLimit)
	}
}
