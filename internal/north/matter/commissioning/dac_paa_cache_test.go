// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package commissioning_test

import (
	"crypto/x509"
	"errors"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/matter/commissioning"
)

// TestVerifyChain_EmptyPool_RejectsNotSucceeds guards against a
// PAA-cache-miss that silently returns Success. When the operator has
// not loaded any PAA trust anchors (empty pool), VerifyChain must
// surface an error rather than accepting any device-attestation chain.
//
// A silent Success here is a Conformance violation: Matter Core Spec
// §6.2.5 requires the commissioner to refuse commissioning if the PAA
// is not trusted.  An untrusted-root scenario with an empty pool would
// silently admit arbitrary devices, breaking the device-attestation
// security model.
func TestVerifyChain_EmptyPool_RejectsNotSucceeds(t *testing.T) {
	t.Parallel()

	now := time.Now()
	tc := newTestChain(t, now)

	// Deliberately empty pool — no PAA trust anchors loaded.
	emptyPool := x509.NewCertPool()

	chain := commissioning.DACChain{DAC: tc.DACDer, PAI: tc.PAIDer}
	_, _, _, err := commissioning.VerifyChain(chain, emptyPool, now)

	if err == nil {
		t.Fatal("VerifyChain with empty PAA pool: expected error (PAA not trusted), got nil — " +
			"this is a Conformance violation: an empty trust store must not admit any device")
	}

	// The error must be ErrDACChainBroken (unknown authority / no trust anchor).
	if !errors.Is(err, commissioning.ErrDACChainBroken) {
		t.Errorf("VerifyChain with empty pool: err=%v, want ErrDACChainBroken", err)
	}
}

// TestVerifyChain_WrongPAA_RejectsNotSucceeds pins that a PAA pool
// containing a *different* root (cache-miss scenario: device presents a
// PAA not in the operator's cached set) must return an error.
//
// This is the key PAA-cache-miss case: when a new device's PAA has not
// been added to the trust store yet, VerifyChain must refuse rather than
// silently promote the unknown root to trusted status.
func TestVerifyChain_WrongPAA_RejectsNotSucceeds(t *testing.T) {
	t.Parallel()

	now := time.Now()
	tc := newTestChain(t, now)

	// Pool contains an unrelated PAA — tc's PAA is not present.
	unrelatedChain := newTestChain(t, now)
	wrongPool := unrelatedChain.paaPool(t)

	chain := commissioning.DACChain{DAC: tc.DACDer, PAI: tc.PAIDer}
	_, _, _, err := commissioning.VerifyChain(chain, wrongPool, now)

	if err == nil {
		t.Fatal("VerifyChain with wrong PAA in pool: expected error, got nil — " +
			"PAA-cache-miss must not silently succeed")
	}
	if !errors.Is(err, commissioning.ErrDACChainBroken) {
		t.Errorf("VerifyChain with wrong PAA: err=%v, want ErrDACChainBroken", err)
	}
}

// TestVerifyChain_NilPool_GuardAlreadyPresent documents that the nil-pool
// guard (the explicit nil check at the top of VerifyChain) fires before
// any crypto operation. This is a belt-and-suspenders guard: a nil pool
// means the operator has not configured PAA trust anchors at all, which
// must be treated as "no trust roots — reject all chains."
func TestVerifyChain_NilPool_GuardAlreadyPresent(t *testing.T) {
	t.Parallel()

	now := time.Now()
	tc := newTestChain(t, now)

	chain := commissioning.DACChain{DAC: tc.DACDer, PAI: tc.PAIDer}
	_, _, _, err := commissioning.VerifyChain(chain, nil, now)

	if err == nil {
		t.Fatal("VerifyChain(nil pool): expected error, got nil — nil pool must be rejected immediately")
	}
	if !errors.Is(err, commissioning.ErrDACChainBroken) {
		t.Errorf("VerifyChain(nil pool): err=%v, want ErrDACChainBroken", err)
	}
}
