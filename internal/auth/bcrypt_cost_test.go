// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package auth

import (
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// TestProductionBcryptCostIsUnchanged pins the work factor the shipped daemon
// hashes at.
//
// [BcryptCost] exists so a test binary can hash at cost 4 instead of 12 — a
// ~330x saving under the race detector, repeated by every test that seeds a
// user. That indirection is also, by construction, the shape a silent
// weakening of production password strength would take: change one constant
// and every hash the daemon writes gets cheaper to crack, with no test
// failing, because no other test asserts a cost. This one does.
//
// 12 is not arbitrary. It is ~0.25 s per hash on server-class hardware, the
// cost an offline attacker pays per candidate. Lowering it is a security
// decision that belongs in a review, not in a performance change.
func TestProductionBcryptCostIsUnchanged(t *testing.T) {
	t.Parallel()

	if ProductionBcryptCost != 12 {
		t.Errorf("ProductionBcryptCost = %d, want 12 — lowering the work factor "+
			"the daemon hashes at is a security change, not a speed-up",
			ProductionBcryptCost)
	}
	if testBcryptCost >= ProductionBcryptCost {
		t.Errorf("testBcryptCost = %d is not below ProductionBcryptCost = %d; "+
			"the whole point of the split is that tests hash cheaply",
			testBcryptCost, ProductionBcryptCost)
	}
}

// TestBcryptCostIsLoweredOnlyInTestBinaries checks the gate itself: this
// process IS a test binary, so BcryptCost must report the test value, and
// there must be no way to reach the test value outside one — which is why the
// gate is testing.Testing() and not a variable anything could assign.
func TestBcryptCostIsLoweredOnlyInTestBinaries(t *testing.T) {
	t.Parallel()

	if got := BcryptCost(); got != testBcryptCost {
		t.Fatalf("BcryptCost() = %d inside a test binary, want %d", got, testBcryptCost)
	}
	if !testing.Testing() {
		t.Fatal("testing.Testing() is false inside a test — the gate BcryptCost " +
			"relies on does not mean what it is documented to mean")
	}
}

// TestHashPasswordUsesTheActiveCost proves the cost actually reaches the hash,
// rather than BcryptCost being computed and discarded, and that the resulting
// hash still round-trips through the real verify path.
func TestHashPasswordUsesTheActiveCost(t *testing.T) {
	t.Parallel()

	h, err := HashPassword("correct-horse-battery-staple")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	cost, err := bcrypt.Cost([]byte(h))
	if err != nil {
		t.Fatalf("bcrypt.Cost: %v", err)
	}
	if cost != BcryptCost() {
		t.Errorf("HashPassword produced a cost-%d hash, want %d", cost, BcryptCost())
	}
	// The cheaper cost must not change what verification proves: the right
	// password is accepted, the wrong one is not, through the same
	// CompareHashAndPassword call production uses.
	if err := bcrypt.CompareHashAndPassword([]byte(h), []byte("correct-horse-battery-staple")); err != nil {
		t.Errorf("the correct password failed to verify: %v", err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(h), []byte("wrong")); err == nil {
		t.Error("a wrong password verified successfully")
	}
}

// TestDummyBcryptHashesCarryTheCostTheyClaim guards the timing-equalisation
// pair. The unknown-user path compares against a dummy hash so its latency
// matches the wrong-password path; that only holds if the dummy's cost equals
// the cost of the records it is standing in for. A typo in either literal
// would silently reintroduce the user-enumeration timing signal, and no
// authentication test would notice, because both paths still return
// ErrUnauthenticated.
func TestDummyBcryptHashesCarryTheCostTheyClaim(t *testing.T) {
	t.Parallel()

	for want, hash := range dummyBcryptHashes {
		got, err := bcrypt.Cost(hash)
		if err != nil {
			t.Errorf("dummy hash for cost %d is not a valid bcrypt hash: %v", want, err)
			continue
		}
		if got != want {
			t.Errorf("dummy hash filed under cost %d is actually cost %d", want, got)
		}
		// It must never be a usable credential.
		if err := bcrypt.CompareHashAndPassword(hash, []byte("")); err == nil {
			t.Errorf("the cost-%d dummy hash matches the empty password", want)
		}
	}
	if got, err := bcrypt.Cost(DummyBcryptHash()); err != nil || got != BcryptCost() {
		t.Errorf("DummyBcryptHash() has cost %d (err=%v), want %d — the dummy "+
			"compare no longer costs what a real verify costs", got, err, BcryptCost())
	}
}

// TestUnknownUserStillAuthenticatesAsUnauthenticated is the behavioural half:
// the store must reject an unknown subject and a wrong password identically,
// at the lowered cost as at the production one.
func TestUnknownUserStillAuthenticatesAsUnauthenticated(t *testing.T) {
	t.Parallel()

	s := NewMemoryUserStore()
	h, err := HashPassword("s3cret")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	s.Put("alice", h, RoleAdmin)

	if _, err := s.AuthenticateBasic(t.Context(), "alice", "s3cret"); err != nil {
		t.Errorf("the correct credential was rejected: %v", err)
	}
	if _, err := s.AuthenticateBasic(t.Context(), "alice", "nope"); err == nil {
		t.Error("a wrong password authenticated")
	}
	if _, err := s.AuthenticateBasic(t.Context(), "nobody", "s3cret"); err == nil {
		t.Error("an unknown subject authenticated")
	}
}
