// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package codes

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/internal/clock"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// fakeStore is an in-memory Store for facade tests.
type fakeStore struct {
	mu   sync.Mutex
	rows []sqlitestore.AlarmCodeRow
}

func (s *fakeStore) GetAll(context.Context) ([]sqlitestore.AlarmCodeRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]sqlitestore.AlarmCodeRow, len(s.rows))
	copy(out, s.rows)
	return out, nil
}

// fakeJournal records every appended entry.
type fakeJournal struct {
	mu      sync.Mutex
	entries []engine.JournalEntry
}

func (j *fakeJournal) Append(_ context.Context, e engine.JournalEntry) (int64, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.entries = append(j.entries, e)
	return int64(len(j.entries)), nil
}

func (j *fakeJournal) events() []string {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make([]string, len(j.entries))
	for i, e := range j.entries {
		out[i] = e.Event
	}
	return out
}

func pinRow(t *testing.T, id, name, pin string, duress bool, perms Perms, areas []string) sqlitestore.AlarmCodeRow {
	t.Helper()
	hash, err := HashPIN(pin)
	if err != nil {
		t.Fatalf("HashPIN: %v", err)
	}
	permsJSON := `{"arm":false,"disarm":false,"silence":false}`
	if perms.Arm || perms.Disarm || perms.Silence {
		permsJSON = `{"arm":` + boolStr(perms.Arm) + `,"disarm":` + boolStr(perms.Disarm) + `,"silence":` + boolStr(perms.Silence) + `}`
	}
	areasJSON := "[]"
	if len(areas) > 0 {
		areasJSON = `["` + areas[0] + `"]`
	}
	return sqlitestore.AlarmCodeRow{
		ID: id, Name: name, Kind: string(KindPIN), Hash: hash, Duress: duress,
		PermsJSON: permsJSON, AreasJSON: areasJSON, BindingJSON: "{}",
		Enabled: true, CreatedAtMS: 1000, UpdatedAtMS: 1000,
	}
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// TestHashPINVerifyPINRoundTrip verifies a hashed PIN verifies against
// the same PIN and rejects a wrong one.
func TestHashPINVerifyPINRoundTrip(t *testing.T) {
	hash, err := HashPIN("1234")
	if err != nil {
		t.Fatalf("HashPIN: %v", err)
	}
	if !VerifyPIN(hash, "1234") {
		t.Error("VerifyPIN: correct PIN did not verify")
	}
	if VerifyPIN(hash, "4321") {
		t.Error("VerifyPIN: wrong PIN verified")
	}
	if VerifyPIN("", "1234") {
		t.Error("VerifyPIN: empty hash must never verify")
	}
	if VerifyPIN("not-a-hash", "1234") {
		t.Error("VerifyPIN: malformed hash must never verify")
	}
}

// TestHashPINUniqueSalt verifies two hashes of the same PIN differ
// (fresh random salt per call).
func TestHashPINUniqueSalt(t *testing.T) {
	h1, err := HashPIN("1234")
	if err != nil {
		t.Fatalf("HashPIN 1: %v", err)
	}
	h2, err := HashPIN("1234")
	if err != nil {
		t.Fatalf("HashPIN 2: %v", err)
	}
	if h1 == h2 {
		t.Error("two hashes of the same PIN must differ (distinct salts)")
	}
}

// TestFacadeValidateCorrectPINReturnsIdentity verifies a correct PIN
// with the required permission returns its owner's name and no
// duress/error.
func TestFacadeValidateCorrectPINReturnsIdentity(t *testing.T) {
	store := &fakeStore{rows: []sqlitestore.AlarmCodeRow{
		pinRow(t, "c1", "Markus", "1234", false, Perms{Disarm: true}, nil),
	}}
	f := New(Deps{Store: store, Clock: clock.NewFake(time.Unix(0, 0))})

	identity, duress, err := f.Validate(context.Background(), "area-1", "disarm", "1234", "rest-operator")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if identity != "Markus" {
		t.Errorf("identity=%q want Markus", identity)
	}
	if duress {
		t.Error("duress=true want false")
	}
}

// TestFacadeValidateDuressCode verifies a duress-flagged code reports
// duress=true with a nil error (the caller proceeds normally).
func TestFacadeValidateDuressCode(t *testing.T) {
	store := &fakeStore{rows: []sqlitestore.AlarmCodeRow{
		pinRow(t, "c1", "Under Duress", "9999", true, Perms{Disarm: true}, nil),
	}}
	f := New(Deps{Store: store, Clock: clock.NewFake(time.Unix(0, 0))})

	identity, duress, err := f.Validate(context.Background(), "area-1", "disarm", "9999", "mqtt")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if identity != "Under Duress" {
		t.Errorf("identity=%q want %q", identity, "Under Duress")
	}
	if !duress {
		t.Error("duress=false want true")
	}
}

// TestFacadeValidateWrongCodeReturnsErrInvalidCode verifies a wrong
// PIN is refused.
func TestFacadeValidateWrongCodeReturnsErrInvalidCode(t *testing.T) {
	store := &fakeStore{rows: []sqlitestore.AlarmCodeRow{
		pinRow(t, "c1", "Markus", "1234", false, Perms{Disarm: true}, nil),
	}}
	j := &fakeJournal{}
	f := New(Deps{Store: store, Journal: j, Clock: clock.NewFake(time.Unix(0, 0))})

	_, _, err := f.Validate(context.Background(), "area-1", "disarm", "0000", "mqtt")
	if !errors.Is(err, engine.ErrInvalidCode) {
		t.Errorf("err=%v want engine.ErrInvalidCode", err)
	}
	if got := j.events(); len(got) != 1 || got[0] != "invalid_code" {
		t.Errorf("journal events=%v want [invalid_code]", got)
	}
}

// TestFacadeValidateEmptyCodeNoApplicableCodeIsInert verifies an empty
// code against an area with no applicable enabled pin code is a
// pass-through: nil error, empty identity, no duress.
func TestFacadeValidateEmptyCodeNoApplicableCodeIsInert(t *testing.T) {
	f := New(Deps{Store: &fakeStore{}, Clock: clock.NewFake(time.Unix(0, 0))})

	identity, duress, err := f.Validate(context.Background(), "area-1", "disarm", "", "mqtt")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if identity != "" || duress {
		t.Errorf("identity=%q duress=%v want empty/false", identity, duress)
	}
}

// TestFacadeValidateEmptyCodeWithApplicableCodeIsRefused verifies an
// empty code against an area that does have an applicable enabled pin
// code is refused (a code is required and none was supplied).
func TestFacadeValidateEmptyCodeWithApplicableCodeIsRefused(t *testing.T) {
	store := &fakeStore{rows: []sqlitestore.AlarmCodeRow{
		pinRow(t, "c1", "Markus", "1234", false, Perms{Disarm: true}, nil),
	}}
	f := New(Deps{Store: store, Clock: clock.NewFake(time.Unix(0, 0))})

	_, _, err := f.Validate(context.Background(), "area-1", "disarm", "", "mqtt")
	if !errors.Is(err, engine.ErrInvalidCode) {
		t.Errorf("err=%v want engine.ErrInvalidCode", err)
	}
}

// TestFacadeValidateAreaScoping verifies a code restricted to a
// different area does not authenticate for the requested area.
func TestFacadeValidateAreaScoping(t *testing.T) {
	store := &fakeStore{rows: []sqlitestore.AlarmCodeRow{
		pinRow(t, "c1", "Markus", "1234", false, Perms{Disarm: true}, []string{"area-2"}),
	}}
	f := New(Deps{Store: store, Clock: clock.NewFake(time.Unix(0, 0))})

	_, _, err := f.Validate(context.Background(), "area-1", "disarm", "1234", "mqtt")
	if !errors.Is(err, engine.ErrInvalidCode) {
		t.Errorf("err=%v want engine.ErrInvalidCode (code scoped to a different area)", err)
	}

	identity, _, err := f.Validate(context.Background(), "area-2", "disarm", "1234", "mqtt")
	if err != nil {
		t.Fatalf("Validate area-2: %v", err)
	}
	if identity != "Markus" {
		t.Errorf("identity=%q want Markus for the code's own area", identity)
	}
}

// TestFacadeValidatePermissionDenied verifies a correct PIN lacking
// the requested verb's permission is refused without touching the
// rate limiter (a legitimate credential used for an unauthorized verb
// is not a guessing attack).
func TestFacadeValidatePermissionDenied(t *testing.T) {
	store := &fakeStore{rows: []sqlitestore.AlarmCodeRow{
		pinRow(t, "c1", "Markus", "1234", false, Perms{Disarm: true}, nil), // no Arm perm
	}}
	f := New(Deps{Store: store, Clock: clock.NewFake(time.Unix(0, 0))})

	_, _, err := f.Validate(context.Background(), "area-1", "arm", "1234", "mqtt")
	if !errors.Is(err, engine.ErrInvalidCode) {
		t.Errorf("err=%v want engine.ErrInvalidCode", err)
	}

	// The same code still authenticates for disarm right after — proof
	// the permission-denied path did not lock it out.
	identity, _, err := f.Validate(context.Background(), "area-1", "disarm", "1234", "mqtt")
	if err != nil {
		t.Fatalf("Validate disarm: %v", err)
	}
	if identity != "Markus" {
		t.Errorf("identity=%q want Markus", identity)
	}
}

// TestFacadeValidateLockoutAfterRepeatedFailures verifies a source is
// locked out after five wrong attempts, and that a correct code is
// also refused while locked out.
func TestFacadeValidateLockoutAfterRepeatedFailures(t *testing.T) {
	store := &fakeStore{rows: []sqlitestore.AlarmCodeRow{
		pinRow(t, "c1", "Markus", "1234", false, Perms{Disarm: true}, nil),
	}}
	fc := clock.NewFake(time.Unix(0, 0))
	j := &fakeJournal{}
	f := New(Deps{Store: store, Journal: j, Clock: fc})
	ctx := context.Background()

	for i := range rateLimitMaxAttempts {
		if _, _, err := f.Validate(ctx, "area-1", "disarm", "0000", "keypad:1"); !errors.Is(err, engine.ErrInvalidCode) {
			t.Fatalf("attempt %d: err=%v want engine.ErrInvalidCode", i, err)
		}
	}

	// The source is now locked out — even the correct code is refused.
	if _, _, err := f.Validate(ctx, "area-1", "disarm", "1234", "keypad:1"); !errors.Is(err, engine.ErrInvalidCode) {
		t.Errorf("locked-out correct code: err=%v want engine.ErrInvalidCode", err)
	}

	// A different source is unaffected.
	identity, _, err := f.Validate(ctx, "area-1", "disarm", "1234", "keypad:2")
	if err != nil {
		t.Fatalf("Validate other source: %v", err)
	}
	if identity != "Markus" {
		t.Errorf("identity=%q want Markus", identity)
	}

	foundLockout := false
	for _, e := range j.events() {
		if e == "code_lockout" {
			foundLockout = true
		}
	}
	if !foundLockout {
		t.Errorf("journal events=%v want a code_lockout entry", j.events())
	}

	// After the lockout window elapses, the source recovers.
	fc.Advance(rateLimitBaseLockout + time.Second)
	identity, _, err = f.Validate(ctx, "area-1", "disarm", "1234", "keypad:1")
	if err != nil {
		t.Fatalf("Validate after lockout elapsed: %v", err)
	}
	if identity != "Markus" {
		t.Errorf("identity=%q want Markus after lockout elapsed", identity)
	}
}

// TestFacadeRowsProjectsHardwareBindings verifies Rows parses a
// keypad_slot row's binding and omits the hash field entirely (there
// is none to omit — Row carries no Hash field at all).
func TestFacadeRowsProjectsHardwareBindings(t *testing.T) {
	store := &fakeStore{rows: []sqlitestore.AlarmCodeRow{
		{
			ID: "k1", Name: "Front Door Slot 1", Kind: string(KindKeypadSlot),
			PermsJSON:   `{"arm":true,"disarm":true,"silence":false}`,
			AreasJSON:   `["area-1"]`,
			BindingJSON: `{"central":"ccu1","device_address":"0001ABCD","slot":1,"arm_mode":"full","area_id":"area-1"}`,
			Enabled:     true,
		},
	}}
	f := New(Deps{Store: store, Clock: clock.NewFake(time.Unix(0, 0))})

	rows, err := f.Rows(context.Background())
	if err != nil {
		t.Fatalf("Rows: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("len=%d want 1", len(rows))
	}
	got := rows[0]
	if got.Kind != KindKeypadSlot {
		t.Errorf("Kind=%q want %q", got.Kind, KindKeypadSlot)
	}
	if got.Binding.DeviceAddress != "0001ABCD" || got.Binding.Slot != 1 || got.Binding.AreaID != "area-1" {
		t.Errorf("Binding=%+v unexpected", got.Binding)
	}
	if len(got.Areas) != 1 || got.Areas[0] != "area-1" {
		t.Errorf("Areas=%v want [area-1]", got.Areas)
	}
}

// TestFacadeRowsSkipsMalformedRow verifies a row with unparsable JSON
// is skipped rather than failing the whole call.
func TestFacadeRowsSkipsMalformedRow(t *testing.T) {
	store := &fakeStore{rows: []sqlitestore.AlarmCodeRow{
		{ID: "bad", Kind: string(KindRemoteKey), BindingJSON: "{not json", Enabled: true},
		{ID: "good", Kind: string(KindRemoteKey), BindingJSON: "{}", Enabled: true},
	}}
	f := New(Deps{Store: store, Clock: clock.NewFake(time.Unix(0, 0))})

	rows, err := f.Rows(context.Background())
	if err != nil {
		t.Fatalf("Rows: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != "good" {
		t.Errorf("rows=%+v want only the well-formed row", rows)
	}
}
