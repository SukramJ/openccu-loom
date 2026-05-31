// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package commissioning

import (
	"crypto/hkdf"
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/north/matter/secure/channel"
	"github.com/SukramJ/openccu-loom/internal/north/matter/secure/spake2"
)

// PASEConfig drives a PASE-responder run.
type PASEConfig struct {
	// Passcode is the 27-bit setup code (Matter §5.1.6.4) the
	// operator entered into the commissioner. Always between
	// 00000001 and 99999998 inclusive (per spec); 00000000 and
	// every-bit-on values are reserved.
	Passcode uint32
	// Salt is the PBKDF2 salt persisted alongside the passcode.
	// 16..32 bytes per Matter §3.10.
	Salt []byte
	// Iterations is the PBKDF2 iteration count. Matter requires
	// 1000..100000 (§3.10).
	Iterations int
	// LocalNodeID is the bridge's transient PASE node identifier.
	// Per Matter §4.13.2 the responder uses a random "ephemeral"
	// node id during PASE.
	LocalNodeID uint64
	// PeerNodeID is the commissioner's transient PASE node
	// identifier.
	PeerNodeID uint64
	// IDA / IDB are the PASE identifiers (commonly "CHIP_PAKE_ID_A"
	// and "CHIP_PAKE_ID_B").
	IDA []byte
	IDB []byte
}

// Errors.
var (
	// ErrPASEStateMismatch is returned when methods are invoked out
	// of order (e.g. Pake3 before Pake1).
	ErrPASEStateMismatch = errors.New("commissioning: PASE state mismatch")
	// ErrPASEInvalidPasscode is returned when [PASEConfig.Passcode]
	// is outside the allowed setup-code range.
	ErrPASEInvalidPasscode = errors.New("commissioning: invalid passcode")
	// ErrPASEInvalidIterations is returned when [PASEConfig.Iterations]
	// is outside Matter's 1000..100000 range.
	ErrPASEInvalidIterations = errors.New("commissioning: invalid PBKDF2 iterations")
)

// PASEResponder runs the bridge-side of a Matter PASE handshake. The
// commissioner sends Pake1; the responder produces Pake2; the
// commissioner sends Pake3; the responder validates the confirmation
// and emits the session keys.
type PASEResponder struct {
	cfg       PASEConfig
	verifier  *spake2.Verifier
	pake1Done bool
	finished  bool
}

// NewPASEResponder constructs a responder with cfg validated. The
// PBKDF / SPAKE2+ initialisation runs eagerly so Pake1 processing is
// fast.
func NewPASEResponder(cfg PASEConfig) (*PASEResponder, error) {
	if cfg.Passcode < 1 || cfg.Passcode > 99999998 {
		return nil, fmt.Errorf("%w: %d", ErrPASEInvalidPasscode, cfg.Passcode)
	}
	if cfg.Iterations < 1000 || cfg.Iterations > 100000 {
		return nil, fmt.Errorf("%w: %d", ErrPASEInvalidIterations, cfg.Iterations)
	}
	if len(cfg.Salt) < 16 || len(cfg.Salt) > 32 {
		return nil, fmt.Errorf("commissioning: salt length=%d (want 16..32)", len(cfg.Salt))
	}

	vc, err := spake2.NewVerifierContext(cfg.Passcode, cfg.Salt, cfg.Iterations)
	if err != nil {
		return nil, fmt.Errorf("commissioning: PASE verifier context: %w", err)
	}
	return &PASEResponder{
		cfg:      cfg,
		verifier: spake2.NewVerifier(vc, cfg.IDA, cfg.IDB, nil),
	}, nil
}

// HandlePake1 consumes the commissioner's Pake1 message and returns
// the bridge's Pake2 (which the caller wires onto the wire). Must be
// called before HandlePake3.
func (r *PASEResponder) HandlePake1(pake1 []byte) (*spake2.Pake2Output, error) {
	if r.pake1Done || r.finished {
		return nil, ErrPASEStateMismatch
	}
	out, err := r.verifier.ProcessPake1(pake1)
	if err != nil {
		return nil, fmt.Errorf("commissioning: PASE Pake1: %w", err)
	}
	r.pake1Done = true
	return out, nil
}

// HandlePake3 consumes the commissioner's Pake3 message. Must be
// called after HandlePake1 returned successfully. Returns nil on
// success; on failure the responder is poisoned and must be
// discarded.
func (r *PASEResponder) HandlePake3(pake3 []byte) error {
	if !r.pake1Done || r.finished {
		return ErrPASEStateMismatch
	}
	if err := r.verifier.ProcessPake3(pake3); err != nil {
		return fmt.Errorf("commissioning: PASE Pake3: %w", err)
	}
	r.finished = true
	return nil
}

// Session derives the AES-CCM session keys and constructs a
// [channel.Session]. Must be called only after HandlePake3 returned
// nil. The returned session has separate I→R and R→I keys derived
// from the PASE shared secret per Matter §4.13.2.5.
func (r *PASEResponder) Session() (*channel.Session, error) {
	if !r.finished {
		return nil, ErrPASEStateMismatch
	}
	shared := r.verifier.SharedSecret()
	if len(shared) == 0 {
		return nil, fmt.Errorf("commissioning: PASE shared secret unavailable")
	}
	encKey, decKey, err := derivePASESessionKeys(shared)
	if err != nil {
		return nil, err
	}
	return channel.New(channel.Config{
		EncryptKey:  encKey,
		DecryptKey:  decKey,
		LocalNodeID: r.cfg.LocalNodeID,
		PeerNodeID:  r.cfg.PeerNodeID,
	})
}

// AttestationChallenge returns the 16-byte attestation challenge for
// downstream Attestation / CSR signing.
func (r *PASEResponder) AttestationChallenge() ([]byte, error) {
	if !r.finished {
		return nil, ErrPASEStateMismatch
	}
	shared := r.verifier.SharedSecret()
	out, err := hkdf.Key(sha256.New, shared, nil, "AttestationChallenge", 16)
	if err != nil {
		return nil, fmt.Errorf("commissioning: attestation challenge derive: %w", err)
	}
	return out, nil
}

// derivePASESessionKeys derives I2RKey || R2IKey via HKDF-Expand on
// the SPAKE2+ Ke. Matter §4.13.2.5 specifies a 32-byte output split
// into two 16-byte AES-CCM keys.
func derivePASESessionKeys(shared []byte) (i2r, r2i []byte, err error) {
	out, err := hkdf.Key(sha256.New, shared, nil, "SessionKeys", 32)
	if err != nil {
		return nil, nil, fmt.Errorf("commissioning: PASE key derive: %w", err)
	}
	return out[:16], out[16:], nil
}
