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

// TestLoadPAAPoolFromPEM_Valid accepts a self-signed PEM CA certificate.
func TestLoadPAAPoolFromPEM_Valid(t *testing.T) {
	t.Parallel()
	now := time.Now()
	tc := newTestChain(t, now)

	pool, err := commissioning.LoadPAAPoolFromPEM(tc.paaPEM())
	if err != nil {
		t.Fatalf("LoadPAAPoolFromPEM: %v", err)
	}
	if pool == nil {
		t.Fatal("pool is nil")
	}
}

// TestLoadPAAPoolFromPEM_Garbage rejects non-PEM / non-cert input.
func TestLoadPAAPoolFromPEM_Garbage(t *testing.T) {
	t.Parallel()
	_, err := commissioning.LoadPAAPoolFromPEM([]byte("not a certificate"))
	if !errors.Is(err, commissioning.ErrDACMalformed) {
		t.Fatalf("err=%v, want ErrDACMalformed", err)
	}
}

// TestVerifyChain_NilPool returns ErrDACChainBroken immediately.
func TestVerifyChain_NilPool(t *testing.T) {
	t.Parallel()
	now := time.Now()
	tc := newTestChain(t, now)

	chain := commissioning.DACChain{DAC: tc.DACDer, PAI: tc.PAIDer}
	_, _, _, err := commissioning.VerifyChain(chain, nil, now)
	if !errors.Is(err, commissioning.ErrDACChainBroken) {
		t.Fatalf("err=%v, want ErrDACChainBroken", err)
	}
}

// TestVerifyChain_MalformedDAC returns ErrDACMalformed for bogus DAC bytes.
func TestVerifyChain_MalformedDAC(t *testing.T) {
	t.Parallel()
	now := time.Now()
	tc := newTestChain(t, now)
	pool := tc.paaPool(t)

	chain := commissioning.DACChain{DAC: []byte("garbage"), PAI: tc.PAIDer}
	_, _, _, err := commissioning.VerifyChain(chain, pool, now)
	if !errors.Is(err, commissioning.ErrDACMalformed) {
		t.Fatalf("err=%v, want ErrDACMalformed", err)
	}
}

// TestVerifyChain_MalformedPAI returns ErrDACMalformed for bogus PAI bytes.
func TestVerifyChain_MalformedPAI(t *testing.T) {
	t.Parallel()
	now := time.Now()
	tc := newTestChain(t, now)
	pool := tc.paaPool(t)

	chain := commissioning.DACChain{DAC: tc.DACDer, PAI: []byte("garbage")}
	_, _, _, err := commissioning.VerifyChain(chain, pool, now)
	if !errors.Is(err, commissioning.ErrDACMalformed) {
		t.Fatalf("err=%v, want ErrDACMalformed", err)
	}
}

// TestVerifyChain_Valid accepts a well-formed three-level chain.
func TestVerifyChain_Valid(t *testing.T) {
	t.Parallel()
	now := time.Now()
	tc := newTestChain(t, now)
	pool := tc.paaPool(t)

	chain := commissioning.DACChain{DAC: tc.DACDer, PAI: tc.PAIDer}
	dac, pai, paa, err := commissioning.VerifyChain(chain, pool, now)
	if err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	if dac == nil || pai == nil || paa == nil {
		t.Fatal("expected non-nil dac/pai/paa certificates")
	}
}

// TestVerifyChain_UntrustedPAA returns ErrDACChainBroken when the PAA is not in paaPool.
func TestVerifyChain_UntrustedPAA(t *testing.T) {
	t.Parallel()
	now := time.Now()
	tc := newTestChain(t, now)

	// Build a second chain; its PAA is NOT in the pool.
	other := newTestChain(t, now)
	pool := other.paaPool(t) // different PAA

	chain := commissioning.DACChain{DAC: tc.DACDer, PAI: tc.PAIDer}
	_, _, _, err := commissioning.VerifyChain(chain, pool, now)
	if !errors.Is(err, commissioning.ErrDACChainBroken) {
		t.Fatalf("err=%v, want ErrDACChainBroken", err)
	}
}

// TestVerifyChain_DACNotYetValid tests a DAC whose NotBefore is in the future.
// The spec-mandated errors are ErrDACExpired or ErrDACChainBroken.
func TestVerifyChain_DACNotYetValid(t *testing.T) {
	t.Parallel()

	// Build a chain whose DAC is valid only in the future.
	future := time.Now().Add(10 * 365 * 24 * time.Hour)
	tc := newTestChain(t, future)

	// Build a separate pool with the future PAA but verify at "now"
	// so the DAC NotBefore has not yet arrived.
	now := time.Now()
	pool := tc.paaPool(t)

	chain := commissioning.DACChain{DAC: tc.DACDer, PAI: tc.PAIDer}
	_, _, _, err := commissioning.VerifyChain(chain, pool, now)
	if err == nil {
		t.Fatal("expected error for future-NotBefore DAC, got nil")
	}
	if !errors.Is(err, commissioning.ErrDACExpired) && !errors.Is(err, commissioning.ErrDACChainBroken) {
		t.Fatalf("err=%v, want ErrDACExpired or ErrDACChainBroken", err)
	}
}

// TestVerifyChain_DACExpired returns ErrDACExpired for a DAC whose NotAfter is in the past.
func TestVerifyChain_DACExpired(t *testing.T) {
	t.Parallel()

	// Build a chain centred in the past.
	past := time.Now().Add(-10 * 365 * 24 * time.Hour)
	tc := newTestChain(t, past)
	pool := tc.paaPool(t)

	// Verify at "now" → DAC is expired.
	now := time.Now()
	chain := commissioning.DACChain{DAC: tc.DACDer, PAI: tc.PAIDer}
	_, _, _, err := commissioning.VerifyChain(chain, pool, now)
	if !errors.Is(err, commissioning.ErrDACExpired) {
		t.Fatalf("err=%v, want ErrDACExpired", err)
	}
}

// TestVerifyChain_ReturnedCertsMatchInput asserts that the returned dac/pai/paa
// subjects match the certificates we created.
func TestVerifyChain_ReturnedCertsMatchInput(t *testing.T) {
	t.Parallel()
	now := time.Now()
	tc := newTestChain(t, now)
	pool := tc.paaPool(t)

	chain := commissioning.DACChain{DAC: tc.DACDer, PAI: tc.PAIDer}
	dac, pai, paa, err := commissioning.VerifyChain(chain, pool, now)
	if err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}

	if dac.Subject.CommonName != "Test DAC" {
		t.Errorf("dac.CN=%q, want %q", dac.Subject.CommonName, "Test DAC")
	}
	if pai.Subject.CommonName != "Test PAI" {
		t.Errorf("pai.CN=%q, want %q", pai.Subject.CommonName, "Test PAI")
	}
	// The PAA that Go's verify builds may be the pool copy; check it is CA.
	if !paa.IsCA {
		t.Errorf("paa.IsCA=false, want true")
	}
	_ = x509.NewCertPool() // import used for type assertion guard above
}
