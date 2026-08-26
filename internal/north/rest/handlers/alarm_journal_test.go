// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/auth"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// seedJournalEntry appends a raw journal row directly through the
// store, bypassing the engine — the journal query tests only care
// about the store round trip and the handler's filter parsing.
func seedJournalEntry(t *testing.T, fx *alarmPanelFixture, zoneID string, class hmenum.AlarmJournalClass, event string, when time.Time) int64 {
	t.Helper()
	id, err := fx.stores.Journal.Append(context.Background(), sqlitestore.AlarmJournalEntry{
		TsMS: when.UnixMilli(), ZoneID: zoneID, Class: class, Event: event,
	})
	if err != nil {
		t.Fatalf("seed journal entry: %v", err)
	}
	return id
}

// seedHiddenDuressEntry appends a hidden duress journal row directly
// through the store, mirroring engine.fireDuress (Event "duress",
// Hidden:true). Hidden rows stay off every operator-visible and
// notification surface but are fully retained for the authorized audit
// reader (notes/concepts/alarm-concept.md §16).
func seedHiddenDuressEntry(t *testing.T, fx *alarmPanelFixture, zoneID string, when time.Time) int64 {
	t.Helper()
	id, err := fx.stores.Journal.Append(context.Background(), sqlitestore.AlarmJournalEntry{
		TsMS: when.UnixMilli(), ZoneID: zoneID, Class: hmenum.AlarmJournalClassDisarm,
		Event: "duress", Hidden: true,
	})
	if err != nil {
		t.Fatalf("seed hidden duress entry: %v", err)
	}
	return id
}

// adminRequest wraps a journal request with a resolved admin identity in
// its context, as the auth middleware would in production.
func adminRequest(t *testing.T, query url.Values) *http.Request {
	t.Helper()
	r := journalRequest(t, query)
	id := auth.Identity{Subject: "root", Scheme: auth.SchemeSession, Role: auth.RoleAdmin}
	return r.WithContext(auth.ContextWithIdentity(r.Context(), id))
}

// operatorRequest wraps a journal request with a resolved operator
// identity — the non-admin role that must never see hidden rows even
// when it explicitly asks.
func operatorRequest(t *testing.T, query url.Values) *http.Request {
	t.Helper()
	r := journalRequest(t, query)
	id := auth.Identity{Subject: "op", Scheme: auth.SchemeSession, Role: auth.RoleOperator}
	return r.WithContext(auth.ContextWithIdentity(r.Context(), id))
}

// TestListAlarmJournal_HiddenDuressAuthorizedReadPath covers the duress
// audit-recoverability rule (notes/concepts/alarm-concept.md §16): a
// duress event entered under coercion is written Hidden:true so it never
// surfaces on an operator-visible screen an intruder could read, but it
// must stay recoverable by an authorized reader — otherwise, at
// duress_visibility=hidden with no webhook, the coerced-under-duress
// event leaves no retrievable trace on any surface. "Hidden" governs the
// operator and notification surfaces, not permanent audit erasure.
func TestListAlarmJournal_HiddenDuressAuthorizedReadPath(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)
	base := alarmFixtureStart
	seedJournalEntry(t, fx, "eg", hmenum.AlarmJournalClassDisarm, "disarmed", base)
	seedHiddenDuressEntry(t, fx, "eg", base.Add(time.Minute))

	hasDuress := func(body []hmapi.AlarmJournalEntry) bool {
		for i := range body {
			if body[i].Event == "duress" {
				return true
			}
		}
		return false
	}
	decode := func(t *testing.T, w *httptest.ResponseRecorder) []hmapi.AlarmJournalEntry {
		t.Helper()
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
		}
		var body []hmapi.AlarmJournalEntry
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		return body
	}

	// Default operator feed (no opt-in, no identity): the hidden duress
	// row is excluded so an intruder standing next to the operator sees a
	// clean journal.
	w := httptest.NewRecorder()
	ListAlarmJournal(fx).ServeHTTP(w, journalRequest(t, nil))
	if body := decode(t, w); hasDuress(body) {
		t.Fatalf("default journal feed leaked the hidden duress row: %+v — attacker-visible screens "+
			"must stay clean", body)
	}

	// Even an admin's ordinary browsing (no explicit opt-in) stays clean:
	// the row surfaces only on a deliberate audit read.
	w = httptest.NewRecorder()
	ListAlarmJournal(fx).ServeHTTP(w, adminRequest(t, nil))
	if body := decode(t, w); hasDuress(body) {
		t.Fatalf("an admin's default journal feed leaked the hidden duress row: %+v", body)
	}

	// An operator who explicitly asks is still refused — the role gate
	// keeps hidden duress off every non-admin surface.
	w = httptest.NewRecorder()
	ListAlarmJournal(fx).ServeHTTP(w, operatorRequest(t, url.Values{"include_hidden": {"true"}}))
	if body := decode(t, w); hasDuress(body) {
		t.Fatalf("an operator with ?include_hidden=true saw the hidden duress row: %+v — only an admin "+
			"audit read may", body)
	}

	// The sanctioned authorized-reader path: an admin who explicitly opts
	// in retrieves the hidden duress row.
	w = httptest.NewRecorder()
	ListAlarmJournal(fx).ServeHTTP(w, adminRequest(t, url.Values{"include_hidden": {"true"}}))
	if body := decode(t, w); !hasDuress(body) {
		t.Fatalf("the admin audit read (?include_hidden=true) did not return the hidden duress row: "+
			"%+v — a duress event under coercion must remain recoverable by an authorized reader", body)
	}
}

func journalRequest(t *testing.T, query url.Values) *http.Request {
	t.Helper()
	target := "/api/v1/alarm/journal"
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	return httptest.NewRequest(http.MethodGet, target, http.NoBody)
}

func TestListAlarmJournal_FiltersByZone(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)
	base := alarmFixtureStart
	seedJournalEntry(t, fx, "eg", hmenum.AlarmJournalClassArm, "armed", base)
	seedJournalEntry(t, fx, "og", hmenum.AlarmJournalClassArm, "armed", base)

	q := url.Values{"zone": {"eg"}}
	w := httptest.NewRecorder()
	ListAlarmJournal(fx).ServeHTTP(w, journalRequest(t, q))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	var body []hmapi.AlarmJournalEntry
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) != 1 || body[0].ZoneID != "eg" {
		t.Fatalf("entries = %+v, want exactly one for zone=eg", body)
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
