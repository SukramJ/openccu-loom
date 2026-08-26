// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package commissioning

import (
	"crypto/x509"
	"errors"
	"fmt"
	"time"
)

// Errors specific to DAC chain validation.
var (
	// ErrDACMalformed is returned when DAC / PAI / PAA bytes do not
	// parse as valid X.509 DER.
	ErrDACMalformed = errors.New("commissioning: DAC certificate malformed")
	// ErrDACChainBroken is returned when the chain's signatures do
	// not link up.
	ErrDACChainBroken = errors.New("commissioning: DAC chain broken")
	// ErrDACExpired is returned when any cert in the chain is outside
	// its NotBefore..NotAfter window.
	ErrDACExpired = errors.New("commissioning: DAC certificate expired or not yet valid")
)

// DACChain bundles the device-side attestation chain. The PAI is
// optional in some Matter profiles; openccu-loom ships a full chain
// and the validator expects all three.
type DACChain struct {
	// DAC is the Device Attestation Certificate (Matter §6.2). DER.
	DAC []byte
	// PAI is the Product Attestation Intermediate (Matter §6.3). DER.
	PAI []byte
}

// VerifyChain validates DAC ← PAI ← PAA. paaPool carries the trust
// anchors the operator pre-loaded; for chip-tool / Apple Home those
// are the Matter-CSA test PAA roots, for production those are the
// commissioner-supplied PAA list.
//
// Returns the parsed *x509.Certificate trio (dac, pai, paa) plus the
// PAA that was actually used as anchor. Any chain or expiry failure
// surfaces as one of the package-level errors.
func VerifyChain(chain DACChain, paaPool *x509.CertPool, now time.Time) (dac, pai, paa *x509.Certificate, err error) {
	if paaPool == nil {
		return nil, nil, nil, fmt.Errorf("%w: PAA pool is nil", ErrDACChainBroken)
	}
	dac, err = x509.ParseCertificate(chain.DAC)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("%w: DAC: %w", ErrDACMalformed, err)
	}
	pai, err = x509.ParseCertificate(chain.PAI)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("%w: PAI: %w", ErrDACMalformed, err)
	}

	intermediates := x509.NewCertPool()
	intermediates.AddCert(pai)

	opts := x509.VerifyOptions{
		Roots:         paaPool,
		Intermediates: intermediates,
		CurrentTime:   now,
		// Matter device-attestation chain — leaf usage is opaque to
		// us; the standard EKU check rejects it. Skip EKU validation;
		// the Matter spec uses its own X.509 extensions instead.
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	}
	chains, verifyErr := dac.Verify(opts)
	if verifyErr != nil {
		switch {
		case errors.As(verifyErr, new(x509.CertificateInvalidError)):
			cierr := x509.CertificateInvalidError{}
			if errors.As(verifyErr, &cierr) && cierr.Reason == x509.Expired {
				return nil, nil, nil, fmt.Errorf("%w: %w", ErrDACExpired, verifyErr)
			}
		case errors.As(verifyErr, new(x509.UnknownAuthorityError)):
			return nil, nil, nil, fmt.Errorf("%w: PAA not trusted", ErrDACChainBroken)
		}
		return nil, nil, nil, fmt.Errorf("%w: %w", ErrDACChainBroken, verifyErr)
	}
	if len(chains) == 0 || len(chains[0]) < 3 {
		return nil, nil, nil, fmt.Errorf("%w: chain length=%d", ErrDACChainBroken, len(chains))
	}

	// chains[0][0] = leaf (DAC), chains[0][1] = PAI, chains[0][2] = PAA.
	return chains[0][0], chains[0][1], chains[0][2], nil
}

// LoadPAAPoolFromPEM is a convenience for tests / small deployments.
// Production code reads the PAA list from a configured directory and
// passes the resulting *x509.CertPool in.
func LoadPAAPoolFromPEM(pemBytes []byte) (*x509.CertPool, error) {
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pemBytes) {
		return nil, fmt.Errorf("%w: AppendCertsFromPEM rejected input", ErrDACMalformed)
	}
	return pool, nil
}
