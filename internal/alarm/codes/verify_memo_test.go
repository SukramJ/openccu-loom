// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package codes

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/internal/clock"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// memoTestNow keeps the fake clock past the validity-window epoch the
// other facade tests use.
var memoTestNow = time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)

// TestRepeatedWrongCodeDoesNotDeriveEveryHashAgain pins the per-attempt
// cost of a wrong code.
//
// Verifying a supplied code derives one argon2id key per applicable
// enabled code — deliberately expensive — and a code that matches nothing
// has to be compared against all of them. A household with several codes
// therefore paid that full sweep for every mistyped PIN, and paid it
// again on the retry, which is the attempt a resident actually makes
// while standing in the doorway with the entry delay running.
//
// The two attempts use different sources so the rate limiter cannot be
// the reason the second one is cheap: a lockout short-circuits before the
// hashing and would make this pass with no memo at all.
func TestRepeatedWrongCodeDoesNotDeriveEveryHashAgain(t *testing.T) {
	t.Parallel()

	store := &fakeStore{rows: []sqlitestore.AlarmCodeRow{
		pinRow(t, "c1", "Anna", "1111", false, Perms{Disarm: true}, nil),
		pinRow(t, "c2", "Ben", "2222", false, Perms{Disarm: true}, nil),
		pinRow(t, "c3", "Cara", "3333", false, Perms{Disarm: true}, nil),
	}}
	f := New(Deps{Store: store, Journal: &fakeJournal{}, Clock: clock.NewFake(memoTestNow)})
	ctx := context.Background()

	first := timeValidate(ctx, t, f, "eg", "9999", "mqtt")
	second := timeValidate(ctx, t, f, "eg", "9999", "rest")

	// Three derivations against one: anything near the first sweep means
	// the hashes were all derived a second time.
	if second > first/3 {
		t.Fatalf("second wrong-code attempt took %s against %s for the first: a code already known to "+
			"match nothing must not re-derive every enabled code's hash", second, first)
	}
}

// TestMemoizedCodeStopsAuthenticatingOnceItIsRevoked is the safety half
// of the memo: it may make a decision cheaper, never wronger.
//
// Every memo entry is scoped by a fingerprint of the candidate set, so
// disabling a code, changing its PIN, or letting its validity window
// close leaves the earlier entry unreachable rather than merely stale. A
// memo that outlived the row it was derived from would keep a revoked
// code working — the one failure this cache must not be capable of.
func TestMemoizedCodeStopsAuthenticatingOnceItIsRevoked(t *testing.T) {
	t.Parallel()

	row := pinRow(t, "c1", "Anna", "1111", false, Perms{Disarm: true}, nil)
	store := &fakeStore{rows: []sqlitestore.AlarmCodeRow{row}}
	f := New(Deps{Store: store, Journal: &fakeJournal{}, Clock: clock.NewFake(memoTestNow)})
	ctx := context.Background()

	// Resolved once, so the memo holds it.
	identity, _, err := f.Validate(ctx, "eg", "disarm", "1111", "mqtt")
	if err != nil {
		t.Fatalf("first validate: %v", err)
	}
	if identity != "Anna" {
		t.Fatalf("identity = %q, want Anna", identity)
	}

	for _, tc := range []struct {
		name  string
		apply func(r *sqlitestore.AlarmCodeRow)
	}{
		{name: "disabled", apply: func(r *sqlitestore.AlarmCodeRow) { r.Enabled = false }},
		{name: "pin changed", apply: func(r *sqlitestore.AlarmCodeRow) {
			hash, herr := HashPIN("4242")
			if herr != nil {
				t.Fatalf("HashPIN: %v", herr)
			}
			r.Hash = hash
		}},
		{name: "validity expired", apply: func(r *sqlitestore.AlarmCodeRow) {
			r.ValidUntilMS = memoTestNow.Add(-time.Hour).UnixMilli()
		}},
	} {
		revoked := row
		tc.apply(&revoked)
		store.mu.Lock()
		store.rows = []sqlitestore.AlarmCodeRow{revoked}
		store.mu.Unlock()

		if _, _, err := f.Validate(ctx, "eg", "disarm", "1111", "mqtt"); !errors.Is(err, engine.ErrInvalidCode) {
			t.Fatalf("%s: validate error = %v, want ErrInvalidCode — a revoked code must not be "+
				"accepted from a remembered verification", tc.name, err)
		}
		// Restore for the next case and re-prime the memo.
		store.mu.Lock()
		store.rows = []sqlitestore.AlarmCodeRow{row}
		store.mu.Unlock()
		if _, _, err := f.Validate(ctx, "eg", "disarm", "1111", "operator:tester"); err != nil {
			t.Fatalf("%s: restoring the row must make the code work again: %v", tc.name, err)
		}
	}
}

// timeValidate runs one Validate against a wrong code and returns how
// long it took, failing when the code is unexpectedly accepted.
func timeValidate(ctx context.Context, t *testing.T, f *Facade, zoneID, code, source string) time.Duration {
	t.Helper()
	start := time.Now()
	_, _, err := f.Validate(ctx, zoneID, "disarm", code, source)
	elapsed := time.Since(start)
	if !errors.Is(err, engine.ErrInvalidCode) {
		t.Fatalf("validate(%q) error = %v, want ErrInvalidCode", code, err)
	}
	return elapsed
}
