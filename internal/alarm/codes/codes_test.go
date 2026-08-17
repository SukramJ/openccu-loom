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

func pinRow(t *testing.T, id, name, pin string, duress bool, perms Perms, zones []string) sqlitestore.AlarmCodeRow {
	t.Helper()
	hash, err := HashPIN(pin)
	if err != nil {
		t.Fatalf("HashPIN: %v", err)
	}
	permsJSON := `{"arm":false,"disarm":false,"silence":false}`
	if perms.Arm || perms.Disarm || perms.Silence {
		permsJSON = `{"arm":` + boolStr(perms.Arm) + `,"disarm":` + boolStr(perms.Disarm) + `,"silence":` + boolStr(perms.Silence) + `}`
	}
	zonesJSON := "[]"
	if len(zones) > 0 {
		zonesJSON = `["` + zones[0] + `"]`
	}
	return sqlitestore.AlarmCodeRow{
		ID: id, Name: name, Kind: string(KindPIN), Hash: hash, Duress: duress,
		PermsJSON: permsJSON, ZonesJSON: zonesJSON, BindingJSON: "{}",
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

	identity, duress, err := f.Validate(context.Background(), "zone-1", "disarm", "1234", "rest-operator")
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

	identity, duress, err := f.Validate(context.Background(), "zone-1", "disarm", "9999", "mqtt")
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

	_, _, err := f.Validate(context.Background(), "zone-1", "disarm", "0000", "mqtt")
	if !errors.Is(err, engine.ErrInvalidCode) {
		t.Errorf("err=%v want engine.ErrInvalidCode", err)
	}
	if got := j.events(); len(got) != 1 || got[0] != "invalid_code" {
		t.Errorf("journal events=%v want [invalid_code]", got)
	}
}

// TestFacadeValidateEmptyCodeNoApplicableCodeIsInert verifies an empty
// code against an zone with no applicable enabled pin code is a
// pass-through: nil error, empty identity, no duress.
func TestFacadeValidateEmptyCodeNoApplicableCodeIsInert(t *testing.T) {
	f := New(Deps{Store: &fakeStore{}, Clock: clock.NewFake(time.Unix(0, 0))})

	identity, duress, err := f.Validate(context.Background(), "zone-1", "disarm", "", "mqtt")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if identity != "" || duress {
		t.Errorf("identity=%q duress=%v want empty/false", identity, duress)
	}
}

// TestFacadeValidateEmptyCodeWithApplicableCodeIsRefused verifies an
// empty code against an zone that does have an applicable enabled pin
// code is refused (a code is required and none was supplied).
func TestFacadeValidateEmptyCodeWithApplicableCodeIsRefused(t *testing.T) {
	store := &fakeStore{rows: []sqlitestore.AlarmCodeRow{
		pinRow(t, "c1", "Markus", "1234", false, Perms{Disarm: true}, nil),
	}}
	f := New(Deps{Store: store, Clock: clock.NewFake(time.Unix(0, 0))})

	_, _, err := f.Validate(context.Background(), "zone-1", "disarm", "", "mqtt")
	if !errors.Is(err, engine.ErrInvalidCode) {
		t.Errorf("err=%v want engine.ErrInvalidCode", err)
	}
}

// TestFacadeValidateZoneScoping verifies a code restricted to a
// different zone does not authenticate for the requested zone.
func TestFacadeValidateZoneScoping(t *testing.T) {
	store := &fakeStore{rows: []sqlitestore.AlarmCodeRow{
		pinRow(t, "c1", "Markus", "1234", false, Perms{Disarm: true}, []string{"zone-2"}),
	}}
	f := New(Deps{Store: store, Clock: clock.NewFake(time.Unix(0, 0))})

	_, _, err := f.Validate(context.Background(), "zone-1", "disarm", "1234", "mqtt")
	if !errors.Is(err, engine.ErrInvalidCode) {
		t.Errorf("err=%v want engine.ErrInvalidCode (code scoped to a different zone)", err)
	}

	identity, _, err := f.Validate(context.Background(), "zone-2", "disarm", "1234", "mqtt")
	if err != nil {
		t.Fatalf("Validate zone-2: %v", err)
	}
	if identity != "Markus" {
		t.Errorf("identity=%q want Markus for the code's own zone", identity)
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

	_, _, err := f.Validate(context.Background(), "zone-1", "arm", "1234", "mqtt")
	if !errors.Is(err, engine.ErrInvalidCode) {
		t.Errorf("err=%v want engine.ErrInvalidCode", err)
	}

	// The same code still authenticates for disarm right after — proof
	// the permission-denied path did not lock it out.
	identity, _, err := f.Validate(context.Background(), "zone-1", "disarm", "1234", "mqtt")
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
		if _, _, err := f.Validate(ctx, "zone-1", "disarm", "0000", "keypad:1"); !errors.Is(err, engine.ErrInvalidCode) {
			t.Fatalf("attempt %d: err=%v want engine.ErrInvalidCode", i, err)
		}
	}

	// The source is now locked out — even the correct code is refused.
	if _, _, err := f.Validate(ctx, "zone-1", "disarm", "1234", "keypad:1"); !errors.Is(err, engine.ErrInvalidCode) {
		t.Errorf("locked-out correct code: err=%v want engine.ErrInvalidCode", err)
	}

	// A different source is unaffected.
	identity, _, err := f.Validate(ctx, "zone-1", "disarm", "1234", "keypad:2")
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
	identity, _, err = f.Validate(ctx, "zone-1", "disarm", "1234", "keypad:1")
	if err != nil {
		t.Fatalf("Validate after lockout elapsed: %v", err)
	}
	if identity != "Markus" {
		t.Errorf("identity=%q want Markus after lockout elapsed", identity)
	}
}

// TestFacadeValidateOperatorSourceExemptFromLockoutPreservesDuressDetection
// verifies the rate-limiter exemption for operator sources: a run of
// wrong attempts from an operator source never engages a lockout, so a
// valid duress code entered right after still authenticates and
// reports duress=true instead of being masked by a lockout's
// ErrInvalidCode.
func TestFacadeValidateOperatorSourceExemptFromLockoutPreservesDuressDetection(t *testing.T) {
	store := &fakeStore{rows: []sqlitestore.AlarmCodeRow{
		pinRow(t, "c1", "Under Duress", "9999", true, Perms{Disarm: true}, nil),
	}}
	f := New(Deps{Store: store, Clock: clock.NewFake(time.Unix(0, 0))})
	ctx := context.Background()

	const attempts = rateLimitMaxAttempts + 1
	for i := range attempts {
		if _, _, err := f.Validate(ctx, "zone-1", "disarm", "0000", "rest-operator"); !errors.Is(err, engine.ErrInvalidCode) {
			t.Fatalf("attempt %d: err=%v want engine.ErrInvalidCode", i, err)
		}
	}

	identity, duress, err := f.Validate(ctx, "zone-1", "disarm", "9999", "rest-operator")
	if err != nil {
		t.Fatalf("duress code after repeated operator failures: %v", err)
	}
	if identity != "Under Duress" {
		t.Errorf("identity=%q want %q", identity, "Under Duress")
	}
	if !duress {
		t.Error("duress=false want true")
	}
}

// TestFacadeValidateNonOperatorSourceStillLocksOutAfterRepeatedFailures
// contrasts the operator exemption above: a non-operator source (mqtt)
// still engages the rate limiter after the same run of failures, and a
// correct (even duress) code offered within the lockout window is
// refused — existing lockout behavior is unchanged for sources that
// are not pre-authenticated.
func TestFacadeValidateNonOperatorSourceStillLocksOutAfterRepeatedFailures(t *testing.T) {
	store := &fakeStore{rows: []sqlitestore.AlarmCodeRow{
		pinRow(t, "c1", "Under Duress", "9999", true, Perms{Disarm: true}, nil),
	}}
	f := New(Deps{Store: store, Clock: clock.NewFake(time.Unix(0, 0))})
	ctx := context.Background()

	for i := range rateLimitMaxAttempts {
		if _, _, err := f.Validate(ctx, "zone-1", "disarm", "0000", "mqtt"); !errors.Is(err, engine.ErrInvalidCode) {
			t.Fatalf("attempt %d: err=%v want engine.ErrInvalidCode", i, err)
		}
	}

	if _, _, err := f.Validate(ctx, "zone-1", "disarm", "9999", "mqtt"); !errors.Is(err, engine.ErrInvalidCode) {
		t.Errorf("valid duress code while locked out: err=%v want engine.ErrInvalidCode", err)
	}
}

// TestFacadeValidateOperatorSourceFailuresNeverJournalLockout verifies
// repeated wrong operator attempts still journal each failed attempt
// (audit value) but never engage — or journal — a lockout, since
// operator sources bypass the rate limiter entirely.
func TestFacadeValidateOperatorSourceFailuresNeverJournalLockout(t *testing.T) {
	store := &fakeStore{rows: []sqlitestore.AlarmCodeRow{
		pinRow(t, "c1", "Markus", "1234", false, Perms{Disarm: true}, nil),
	}}
	j := &fakeJournal{}
	f := New(Deps{Store: store, Journal: j, Clock: clock.NewFake(time.Unix(0, 0))})
	ctx := context.Background()

	const attempts = rateLimitMaxAttempts + 2
	for i := range attempts {
		if _, _, err := f.Validate(ctx, "zone-1", "disarm", "0000", "rest-operator"); !errors.Is(err, engine.ErrInvalidCode) {
			t.Fatalf("attempt %d: err=%v want engine.ErrInvalidCode", i, err)
		}
	}

	events := j.events()
	if len(events) != attempts {
		t.Fatalf("journal events=%v want %d invalid_code faults, one per attempt", events, attempts)
	}
	for _, e := range events {
		if e != "invalid_code" {
			t.Fatalf("journal events=%v want only invalid_code faults, never code_lockout", events)
		}
	}
}

// TestFacadeMatchDuressWrongCodesNeverLockOutTheCodePlane is the
// reason MatchDuress exists at all: it is called where the verb is a
// no-op whatever the code is (a disarm of an already-disarmed zone),
// which is reachable by anyone who can publish an alarm command. A run
// of wrong codes there must leave both the code plane's lockout ledger
// and the fault journal untouched, or that path becomes a remote
// lockout of every zone for the source.
func TestFacadeMatchDuressWrongCodesNeverLockOutTheCodePlane(t *testing.T) {
	store := &fakeStore{rows: []sqlitestore.AlarmCodeRow{
		pinRow(t, "c1", "Markus", "1234", false, Perms{Disarm: true}, nil),
		pinRow(t, "c2", "Under Duress", "9999", true, Perms{Disarm: true}, nil),
	}}
	j := &fakeJournal{}
	f := New(Deps{Store: store, Journal: j, Clock: clock.NewFake(time.Unix(0, 0))})
	ctx := context.Background()

	for i := range rateLimitMaxAttempts * 3 {
		if identity, duress := f.MatchDuress(ctx, "zone-1", "disarm", "0000", "mqtt"); duress || identity != "" {
			t.Fatalf("attempt %d: MatchDuress(wrong code)=(%q,%v) want ('',false)", i, identity, duress)
		}
	}

	if events := j.events(); len(events) != 0 {
		t.Errorf("journal events=%v want none — a no-op path records no fault", events)
	}
	// The code plane is still usable for the same source.
	identity, _, err := f.Validate(ctx, "zone-1", "disarm", "1234", "mqtt")
	if err != nil {
		t.Fatalf("Validate after duress probes: %v, want the source not locked out", err)
	}
	if identity != "Markus" {
		t.Errorf("identity=%q want Markus", identity)
	}
}

// TestFacadeMatchDuressResolvesTheDuressCodeOnly verifies the covert
// channel still works through the pure matcher: the duress code
// reports its identity, an ordinary valid code does not, and neither
// call can refuse anything.
func TestFacadeMatchDuressResolvesTheDuressCodeOnly(t *testing.T) {
	store := &fakeStore{rows: []sqlitestore.AlarmCodeRow{
		pinRow(t, "c1", "Markus", "1234", false, Perms{Disarm: true}, nil),
		pinRow(t, "c2", "Under Duress", "9999", true, Perms{Disarm: true}, nil),
	}}
	f := New(Deps{Store: store, Clock: clock.NewFake(time.Unix(0, 0))})
	ctx := context.Background()

	identity, duress := f.MatchDuress(ctx, "zone-1", "disarm", "9999", "mqtt")
	if !duress || identity != "Under Duress" {
		t.Errorf("MatchDuress(duress code)=(%q,%v) want (%q,true)", identity, duress, "Under Duress")
	}
	if identity, duress := f.MatchDuress(ctx, "zone-1", "disarm", "1234", "mqtt"); duress || identity != "" {
		t.Errorf("MatchDuress(ordinary code)=(%q,%v) want ('',false)", identity, duress)
	}
	if identity, duress := f.MatchDuress(ctx, "zone-1", "disarm", "", "mqtt"); duress || identity != "" {
		t.Errorf("MatchDuress(no code)=(%q,%v) want ('',false)", identity, duress)
	}
}

// TestFacadeMatchDuressHonorsZoneScopeAndVerbPermission verifies the
// matcher applies the same applicability rules Validate does: a code
// scoped to another zone, or without the verb's permission, is not a
// duress match.
func TestFacadeMatchDuressHonorsZoneScopeAndVerbPermission(t *testing.T) {
	store := &fakeStore{rows: []sqlitestore.AlarmCodeRow{
		pinRow(t, "c1", "Other Zone", "9999", true, Perms{Disarm: true}, []string{"zone-2"}),
		pinRow(t, "c2", "Arm Only", "8888", true, Perms{Arm: true}, nil),
	}}
	f := New(Deps{Store: store, Clock: clock.NewFake(time.Unix(0, 0))})
	ctx := context.Background()

	if identity, duress := f.MatchDuress(ctx, "zone-1", "disarm", "9999", "mqtt"); duress {
		t.Errorf("MatchDuress(other zone)=(%q,%v) want ('',false)", identity, duress)
	}
	if identity, duress := f.MatchDuress(ctx, "zone-1", "disarm", "8888", "mqtt"); duress {
		t.Errorf("MatchDuress(no disarm permission)=(%q,%v) want ('',false)", identity, duress)
	}
}

// TestFacadeMatchDuressBoundsProbeWorkPerSource verifies the probe
// ledger: verifying an argon2id hash is expensive, so a source that
// keeps missing is cut off after the same attempt budget the code
// plane uses — on its own ledger, recovering when the window elapses,
// and without ever refusing a verb.
func TestFacadeMatchDuressBoundsProbeWorkPerSource(t *testing.T) {
	store := &fakeStore{rows: []sqlitestore.AlarmCodeRow{
		pinRow(t, "c1", "Under Duress", "9999", true, Perms{Disarm: true}, nil),
	}}
	fc := clock.NewFake(time.Unix(0, 0))
	f := New(Deps{Store: store, Clock: fc})
	ctx := context.Background()

	for i := range rateLimitMaxAttempts {
		if _, duress := f.MatchDuress(ctx, "zone-1", "disarm", "0000", "mqtt"); duress {
			t.Fatalf("attempt %d: wrong code reported duress", i)
		}
	}
	if _, duress := f.MatchDuress(ctx, "zone-1", "disarm", "9999", "mqtt"); duress {
		t.Error("probe budget exhausted: want no verification work until the window elapses")
	}
	// Another source is unaffected by the first one's budget.
	if _, duress := f.MatchDuress(ctx, "zone-1", "disarm", "9999", "keypad:1"); !duress {
		t.Error("a second source must have its own probe budget")
	}

	fc.Advance(rateLimitBaseLockout + time.Second)
	if _, duress := f.MatchDuress(ctx, "zone-1", "disarm", "9999", "mqtt"); !duress {
		t.Error("duress code after the probe window elapsed: want a match")
	}
}

// TestFacadeValidateCodeFreeMasterDisarmNeverLocksOutSource pins the
// aggregate-panel defect: an HA "master" alarm_control_panel disarms
// code-free, and the engine loops that one press across every zone. A
// code-required zone refuses it — correctly — but an absent code is not
// a wrong guess and must not charge the rate limiter, or a handful of
// code-required zones on a single press would lock the source out and
// then refuse the operator's immediately following correct per-zone
// PIN. A genuinely wrong code must still count, so the brute-force
// lockout is preserved.
//
// Bite: with the limiter charged on a missing code, the correct PIN
// after rateLimitMaxAttempts code-free presses is refused and the first
// assertion fails.
func TestFacadeValidateCodeFreeMasterDisarmNeverLocksOutSource(t *testing.T) {
	store := &fakeStore{rows: []sqlitestore.AlarmCodeRow{
		pinRow(t, "c1", "Markus", "1234", false, Perms{Disarm: true}, nil),
	}}
	f := New(Deps{Store: store, Clock: clock.NewFake(time.Unix(0, 0))})
	ctx := context.Background()

	// Far more code-free "master" disarms than the wrong-code budget.
	const presses = rateLimitMaxAttempts * 3
	for i := range presses {
		if _, _, err := f.Validate(ctx, "zone-1", "disarm", "", "mqtt"); !errors.Is(err, engine.ErrInvalidCode) {
			t.Fatalf("press %d: err=%v want engine.ErrInvalidCode (code required, none supplied)", i, err)
		}
	}

	// The correct per-zone PIN from the same source is still accepted —
	// no code-free press ever charged the limiter.
	identity, _, err := f.Validate(ctx, "zone-1", "disarm", "1234", "mqtt")
	if err != nil {
		t.Fatalf("correct PIN after %d code-free disarms: %v, want it accepted", presses, err)
	}
	if identity != "Markus" {
		t.Errorf("identity=%q want Markus", identity)
	}

	// A genuinely wrong code still counts toward the lockout: the real
	// brute-force protection is untouched. (The success above cleared the
	// ledger, so this run starts fresh.)
	for i := range rateLimitMaxAttempts {
		if _, _, err := f.Validate(ctx, "zone-1", "disarm", "0000", "mqtt"); !errors.Is(err, engine.ErrInvalidCode) {
			t.Fatalf("wrong attempt %d: err=%v want engine.ErrInvalidCode", i, err)
		}
	}
	if _, _, err := f.Validate(ctx, "zone-1", "disarm", "1234", "mqtt"); !errors.Is(err, engine.ErrInvalidCode) {
		t.Errorf("correct PIN after %d wrong codes: err=%v want a lockout ErrInvalidCode", rateLimitMaxAttempts, err)
	}
}

// TestFacadeMatchDuressCorrectPINNeverSuppressesRealDuress pins the
// probe-ledger defect: a correct ordinary PIN is not a failed duress
// guess. The aggregate panel republishes the household's correct PIN
// across zones as ordinary no-op disarms; charging each one to the
// probe limiter would exhaust the budget and mute a real duress code
// entered during that window — the exact scenario the branch exists
// for. Only a genuinely unknown code counts (guarded by
// TestFacadeMatchDuressBoundsProbeWorkPerSource).
//
// Bite: with recordFailure charged on the correct ordinary PIN, the
// real duress code after rateLimitMaxAttempts correct-PIN disarms is
// suppressed and the final assertion fails.
func TestFacadeMatchDuressCorrectPINNeverSuppressesRealDuress(t *testing.T) {
	store := &fakeStore{rows: []sqlitestore.AlarmCodeRow{
		pinRow(t, "c1", "Markus", "1234", false, Perms{Disarm: true}, nil),
		pinRow(t, "c2", "Under Duress", "9999", true, Perms{Disarm: true}, nil),
	}}
	f := New(Deps{Store: store, Clock: clock.NewFake(time.Unix(0, 0))})
	ctx := context.Background()

	const disarms = rateLimitMaxAttempts * 3
	for i := range disarms {
		if identity, duress := f.MatchDuress(ctx, "zone-1", "disarm", "1234", "mqtt"); duress || identity != "" {
			t.Fatalf("disarm %d: MatchDuress(correct ordinary PIN)=(%q,%v) want ('',false)", i, identity, duress)
		}
	}

	identity, duress := f.MatchDuress(ctx, "zone-1", "disarm", "9999", "mqtt")
	if !duress || identity != "Under Duress" {
		t.Fatalf("real duress code after %d correct-PIN disarms=(%q,%v) want (%q,true) — the probe ledger must not have muted it",
			disarms, identity, duress, "Under Duress")
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
			ZonesJSON:   `["zone-1"]`,
			BindingJSON: `{"central":"ccu1","device_address":"0001ABCD","slot":1,"arm_mode":"full","zone_id":"zone-1"}`,
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
	if got.Binding.DeviceAddress != "0001ABCD" || got.Binding.Slot != 1 || got.Binding.ZoneID != "zone-1" {
		t.Errorf("Binding=%+v unexpected", got.Binding)
	}
	if len(got.Zones) != 1 || got.Zones[0] != "zone-1" {
		t.Errorf("Zones=%v want [zone-1]", got.Zones)
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
