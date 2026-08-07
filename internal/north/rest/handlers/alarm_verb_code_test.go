// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/alarm"
	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	alarmjournal "github.com/SukramJ/openccu-loom/internal/alarm/journal"
	"github.com/SukramJ/openccu-loom/internal/alarm/outputs"
	"github.com/SukramJ/openccu-loom/internal/clock"
	"github.com/SukramJ/openccu-loom/internal/model/alarmpanel"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// This file covers the arm/disarm/silence verb handlers' `code` body
// field (docs/alarm-concept.md §11) against a real *engine.Engine wired
// with a stub CodeValidator — the codes facade itself is exercised by
// internal/alarm/codes's own test suite. Every /alarm write route
// attributes the fixed alarmSourceREST ("rest-operator") source, which
// the engine's CodePolicy treats as a break-glass surface (S6): a
// required code is never enforced, and even a code that fails to
// authenticate is swallowed rather than refused. What DOES still fire
// unconditionally whenever a code is supplied is duress detection —
// that is what these tests pin.

// fakeVerbCodeEntry is one entry a fakeVerbCodeValidator recognizes.
type fakeVerbCodeEntry struct {
	identity string
	duress   bool
	perms    map[string]bool
}

// fakeVerbCodeValidator is a minimal engine.CodeValidator double: an
// unrecognized code (or one lacking the requested verb's permission)
// answers engine.ErrInvalidCode, mirroring the codes facade's contract
// without pulling in argon2id hashing (already covered by
// internal/alarm/codes's own round-trip tests).
type fakeVerbCodeValidator struct {
	codes map[string]fakeVerbCodeEntry
}

func (f *fakeVerbCodeValidator) Validate(_ context.Context, _, verb, code, _ string) (identity string, duress bool, err error) {
	e, ok := f.codes[code]
	if !ok || !e.perms[verb] {
		return "", false, engine.ErrInvalidCode
	}
	return e.identity, e.duress, nil
}

// recordingSink is a minimal engine.EventSink double that records every
// published event so tests can assert on the silent AlarmDuressEvent
// fan-out (docs/alarm-concept.md §11: never broadcast over WS, but
// always published to the bus for the MQTT/webhook consumers).
type recordingSink struct {
	mu     sync.Mutex
	events []hmevent.Event
}

func (s *recordingSink) Publish(e hmevent.Event) {
	s.mu.Lock()
	s.events = append(s.events, e)
	s.mu.Unlock()
}

func (s *recordingSink) duressEvents() []hmevent.AlarmDuressEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []hmevent.AlarmDuressEvent
	for _, e := range s.events {
		if d, ok := e.(hmevent.AlarmDuressEvent); ok {
			out = append(out, d)
		}
	}
	return out
}

// alarmVerbCodeFixture is a real engine over a temp-file SQLite store
// bundle, wired with a fakeVerbCodeValidator and a recordingSink — the
// minimal AlarmPanel the code-carrying verb handlers need (Manager is
// never called by Arm/Disarm/Silence, so it stays nil).
type alarmVerbCodeFixture struct {
	t      *testing.T
	stores *alarm.Stores
	eng    *engine.Engine
	sink   *recordingSink
	clk    *clock.Fake
}

var _ AlarmPanel = (*alarmVerbCodeFixture)(nil)

func (f *alarmVerbCodeFixture) Engine() *engine.Engine     { return f.eng }
func (f *alarmVerbCodeFixture) Manager() *outputs.Manager  { return nil }
func (f *alarmVerbCodeFixture) Stores() *alarm.Stores      { return f.stores }
func (f *alarmVerbCodeFixture) Panels() []alarmpanel.Panel { return nil }

func (f *alarmVerbCodeFixture) Reload(ctx context.Context) error { return f.eng.Reload(ctx) }

func (f *alarmVerbCodeFixture) OutputCandidates(hmenum.AlarmOutputClass) []alarm.OutputCandidate {
	return nil
}

func (f *alarmVerbCodeFixture) OutputTargetEligible(string, string, hmenum.AlarmOutputClass) (eligible, known bool) {
	return true, false
}

func (f *alarmVerbCodeFixture) RemoteKeyCandidates() []alarm.RemoteKeyCandidate { return nil }

func (f *alarmVerbCodeFixture) SensorCandidates(context.Context) []alarm.SensorCandidate { return nil }

// newAlarmVerbCodeFixture builds an empty, started fixture wired with
// validator and a recordingSink.
func newAlarmVerbCodeFixture(t *testing.T, validator *fakeVerbCodeValidator) *alarmVerbCodeFixture {
	t.Helper()
	ctx := context.Background()
	db := openMigratedTestDB(t, "alarm-verb-code.db")

	stores := alarm.NewStores(db)
	clk := clock.NewFake(alarmFixtureStart)
	jrn := alarmjournal.New(stores.Journal, clk, nil, nil)
	sink := &recordingSink{}

	eng, err := engine.New(engine.Deps{
		Clock:     clk,
		Zones:     stores.Zones,
		Sensors:   stores.Sensors,
		State:     stores.State,
		Incidents: stores.Incidents,
		Runtime:   stores.Runtime,
		Journal:   jrn,
		Sink:      sink,
		Validator: validator,
	})
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	if err := eng.Start(ctx); err != nil {
		t.Fatalf("engine.Start: %v", err)
	}
	return &alarmVerbCodeFixture{t: t, stores: stores, eng: eng, sink: sink, clk: clk}
}

// seedCodePolicyZone persists a single-mode "full" zone with the given
// code policy and reloads the engine.
func (f *alarmVerbCodeFixture) seedCodePolicyZone(id, name string, policy engine.CodePolicy) {
	f.t.Helper()
	cfg := engine.ZoneConfig{
		Modes:      map[hmenum.AlarmMode]engine.ModeConfig{hmenum.AlarmModeFull: {TriggerSeconds: 60}},
		CodePolicy: policy,
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		f.t.Fatalf("marshal zone config: %v", err)
	}
	now := f.clk.Now().UnixMilli()
	if err := f.stores.Zones.Upsert(context.Background(), sqlitestore.AlarmZoneRow{
		ID: id, Name: name, ConfigJSON: string(b), CreatedAtMS: now, UpdatedAtMS: now,
	}); err != nil {
		f.t.Fatalf("seed zone %s: %v", id, err)
	}
	if err := f.eng.Reload(context.Background()); err != nil {
		f.t.Fatalf("reload: %v", err)
	}
}

// requireDisarmTrue is a CodePolicy.RequireDisarm pointer helper.
func requireDisarmTrue() *bool {
	v := true
	return &v
}

// hiddenJournalEvents returns every (including hidden) journal entry of
// zoneID for assertions on the duress fan-out's hidden journal row.
func (f *alarmVerbCodeFixture) hiddenJournalEvents(zoneID string) []sqlitestore.AlarmJournalEntry {
	f.t.Helper()
	rows, err := f.stores.Journal.Query(context.Background(), sqlitestore.AlarmJournalFilter{
		ZoneID: zoneID, IncludeHidden: true, Limit: 100,
	})
	if err != nil {
		f.t.Fatalf("journal query: %v", err)
	}
	return rows
}

// --- disarm ---

// TestDisarmAlarmZone_UnrecognizedCode_StillSucceeds_OperatorBypass pins
// the S6 break-glass rule at the REST surface: even with RequireDisarm
// forced on and a code supplied that the validator refuses, the
// operator-attributed rest-operator source is never blocked.
func TestDisarmAlarmZone_UnrecognizedCode_StillSucceeds_OperatorBypass(t *testing.T) {
	t.Parallel()
	validator := &fakeVerbCodeValidator{codes: map[string]fakeVerbCodeEntry{}}
	fx := newAlarmVerbCodeFixture(t, validator)
	fx.seedCodePolicyZone("eg", "Erdgeschoss", engine.CodePolicy{RequireDisarm: requireDisarmTrue()})
	if _, err := fx.eng.Arm(context.Background(), "eg", engine.ArmRequest{Mode: hmenum.AlarmModeFull, SkipDelay: true}); err != nil {
		t.Fatalf("arm: %v", err)
	}

	body := strings.NewReader(`{"code":"0000"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/alarm/zones/eg/disarm", body)
	req = withChiParam(req, "id", "eg")
	w := httptest.NewRecorder()
	DisarmAlarmZone(fx, nil).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (operator bypass), body=%s", w.Code, w.Body.String())
	}
	if snap, ok := fx.eng.Zone("eg"); !ok || snap.State != hmenum.AlarmZoneStateDisarmed {
		t.Fatalf("zone state = %+v, want disarmed", snap)
	}
}

// TestDisarmAlarmZone_DuressCode_FiresDuressEventAndHiddenJournalEntry
// covers the meaningful code behaviour REST cannot bypass: a duress
// code disarms normally (204, zone disarmed) while silently firing
// AlarmDuressEvent on the bus and a Hidden journal row — never a
// visible journal entry, never a WS broadcast (docs/alarm-concept.md
// §11).
func TestDisarmAlarmZone_DuressCode_FiresDuressEventAndHiddenJournalEntry(t *testing.T) {
	t.Parallel()
	validator := &fakeVerbCodeValidator{codes: map[string]fakeVerbCodeEntry{
		"9999": {identity: "Under Duress", duress: true, perms: map[string]bool{"disarm": true}},
	}}
	fx := newAlarmVerbCodeFixture(t, validator)
	fx.seedCodePolicyZone("eg", "Erdgeschoss", engine.CodePolicy{})
	if _, err := fx.eng.Arm(context.Background(), "eg", engine.ArmRequest{Mode: hmenum.AlarmModeFull, SkipDelay: true}); err != nil {
		t.Fatalf("arm: %v", err)
	}

	body := strings.NewReader(`{"code":"9999"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/alarm/zones/eg/disarm", body)
	req = withChiParam(req, "id", "eg")
	w := httptest.NewRecorder()
	DisarmAlarmZone(fx, nil).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body=%s", w.Code, w.Body.String())
	}
	if snap, ok := fx.eng.Zone("eg"); !ok || snap.State != hmenum.AlarmZoneStateDisarmed {
		t.Fatalf("zone state = %+v, want disarmed", snap)
	}

	duress := fx.sink.duressEvents()
	if len(duress) != 1 {
		t.Fatalf("duress events = %+v, want exactly one", duress)
	}
	// By carries "anonymous" here, not the code's own identity ("Under
	// Duress"): every REST call already resolves identityFromCtx to a
	// non-empty actor before reaching the engine, so the engine's
	// identity-substitution branch (`by == ""`) never triggers for this
	// surface — only MQTT/keypad callers with no prior identity adopt
	// the code's display name as `by`.
	if duress[0].Verb != "disarm" || duress[0].ZoneID != "eg" || duress[0].By != "anonymous" {
		t.Errorf("duress event = %+v, want verb=disarm zone=eg by=anonymous", duress[0])
	}

	found := false
	for _, e := range fx.hiddenJournalEvents("eg") {
		if e.Event == "duress" {
			found = true
			if !e.Hidden {
				t.Error("duress journal row must be Hidden")
			}
		}
	}
	if !found {
		t.Error("no hidden duress journal entry found")
	}
}

// TestDisarmAlarmZone_ValidNonDuressCode_NoDuressEvent is the negative
// counterpart: a code that authenticates but is not flagged duress must
// never fire the duress fan-out.
func TestDisarmAlarmZone_ValidNonDuressCode_NoDuressEvent(t *testing.T) {
	t.Parallel()
	validator := &fakeVerbCodeValidator{codes: map[string]fakeVerbCodeEntry{
		"1234": {identity: "Markus", duress: false, perms: map[string]bool{"disarm": true}},
	}}
	fx := newAlarmVerbCodeFixture(t, validator)
	fx.seedCodePolicyZone("eg", "Erdgeschoss", engine.CodePolicy{})
	if _, err := fx.eng.Arm(context.Background(), "eg", engine.ArmRequest{Mode: hmenum.AlarmModeFull, SkipDelay: true}); err != nil {
		t.Fatalf("arm: %v", err)
	}

	body := strings.NewReader(`{"code":"1234"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/alarm/zones/eg/disarm", body)
	req = withChiParam(req, "id", "eg")
	w := httptest.NewRecorder()
	DisarmAlarmZone(fx, nil).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body=%s", w.Code, w.Body.String())
	}
	if got := fx.sink.duressEvents(); len(got) != 0 {
		t.Errorf("duress events = %+v, want none", got)
	}
}

// --- arm ---

// TestArmAlarmZone_DuressCode_Returns200AndFiresDuressEvent covers the
// arm verb's own code field (hmapi.AlarmArmRequest.Code): a duress code
// arms normally and fires the same silent fan-out as disarm/silence.
func TestArmAlarmZone_DuressCode_Returns200AndFiresDuressEvent(t *testing.T) {
	t.Parallel()
	validator := &fakeVerbCodeValidator{codes: map[string]fakeVerbCodeEntry{
		"9999": {identity: "Under Duress", duress: true, perms: map[string]bool{"arm": true}},
	}}
	fx := newAlarmVerbCodeFixture(t, validator)
	fx.seedCodePolicyZone("eg", "Erdgeschoss", engine.CodePolicy{})

	reqBody, err := json.Marshal(hmapi.AlarmArmRequest{Mode: string(hmenum.AlarmModeFull), SkipDelay: true, Code: "9999"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/alarm/zones/eg/arm", strings.NewReader(string(reqBody)))
	req = withChiParam(req, "id", "eg")
	rec := &captureRecorder{}
	w := httptest.NewRecorder()
	ArmAlarmZone(fx, rec).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	var accepted hmapi.AlarmArmAccepted
	if err := json.Unmarshal(w.Body.Bytes(), &accepted); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if accepted.State != string(hmenum.AlarmZoneStateArmed) {
		t.Fatalf("state = %q, want armed", accepted.State)
	}
	duress := fx.sink.duressEvents()
	if len(duress) != 1 || duress[0].Verb != "arm" {
		t.Fatalf("duress events = %+v, want exactly one with verb=arm", duress)
	}
}

// --- silence ---

// TestSilenceAlarmZone_DuressCode_FiresDuressEvent covers the silence
// verb's own code field: even though silence is code-free by default
// (S3), a supplied duress code still fires the fan-out.
func TestSilenceAlarmZone_DuressCode_FiresDuressEvent(t *testing.T) {
	t.Parallel()
	validator := &fakeVerbCodeValidator{codes: map[string]fakeVerbCodeEntry{
		"9999": {identity: "Under Duress", duress: true, perms: map[string]bool{"silence": true}},
	}}
	fx := newAlarmVerbCodeFixture(t, validator)
	fx.seedCodePolicyZone("eg", "Erdgeschoss", engine.CodePolicy{})
	if _, err := fx.eng.Arm(context.Background(), "eg", engine.ArmRequest{Mode: hmenum.AlarmModeFull, SkipDelay: true}); err != nil {
		t.Fatalf("arm: %v", err)
	}

	body := strings.NewReader(`{"code":"9999"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/alarm/zones/eg/silence", body)
	req = withChiParam(req, "id", "eg")
	w := httptest.NewRecorder()
	SilenceAlarmZone(fx, nil).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body=%s", w.Code, w.Body.String())
	}
	duress := fx.sink.duressEvents()
	if len(duress) != 1 || duress[0].Verb != "silence" {
		t.Fatalf("duress events = %+v, want exactly one with verb=silence", duress)
	}
}
