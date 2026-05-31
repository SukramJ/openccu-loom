// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package channel

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sync"

	"github.com/SukramJ/openccu-loom/internal/north/matter/secure/aesccm"
	"github.com/SukramJ/openccu-loom/internal/north/matter/transport/message"
	"github.com/SukramJ/openccu-loom/internal/north/matter/transport/mrp"
)

// Errors.
var (
	// ErrCounterReplayed is returned by [Session.Decrypt] when the
	// received Message Counter has already been consumed (sliding-
	// window duplicate detection per Matter §4.6.6).
	ErrCounterReplayed = errors.New("channel: message counter replayed or out-of-window")

	// ErrUnauthenticated is returned when the AES-CCM authentication
	// tag does not validate. Indicates either a wrong key or
	// tampering — the caller MUST drop the message.
	ErrUnauthenticated = errors.New("channel: authentication failed")

	// ErrSessionInactive surfaces from outbound calls when the
	// session has been [Session.Close]d.
	ErrSessionInactive = errors.New("channel: session inactive")
)

// Session is one half of an encrypted Matter conversation: the local
// node's view of a peer node sharing two AES-CCM-128 keys (one per
// direction) and per-direction message counters.
//
// Concurrency: Session is safe for concurrent Encrypt / Decrypt; the
// underlying counter and window are atomically / mutex-protected.
type Session struct {
	encKey []byte
	decKey []byte

	encCipher *aesccm.CCM
	decCipher *aesccm.CCM

	localNodeID uint64
	peerNodeID  uint64

	// peerCATs is the set of CASE Authenticated Tags lifted out of the
	// peer's NOC subject during CASE establishment. Used by the IM
	// dispatcher's ACL gate to evaluate per-subject ACEs that target
	// administrator groups (Matter §9.10.5.6 + chip
	// src/access/AccessControl.cpp:481). nil / empty for PASE.
	peerCATs []uint32

	// peerSessionID is the SessionID the peer has assigned for
	// inbound traffic on its side — i.e. what we MUST stamp into
	// outbound Header.SessionID so the peer can resolve the session.
	// In PASE this is the InitiatorSessionID from PBKDFParamRequest;
	// in CASE this is Sigma1.InitiatorSessionID. Distinct from the
	// local-side ID which the operational manager assigns for our
	// inbound table. 0 is a valid wire value pre-establishment;
	// callers that need to detect "not configured" must check
	// out-of-band.
	peerSessionID uint16

	out *mrp.Counter
	in  *mrp.Window

	// privacyMu guards lazy derivation of the privacy keys. The keys
	// are derived on first use and reused for every privacy-flagged
	// frame for the lifetime of the session.
	privacyMu      sync.Mutex
	privacyKey     []byte // outbound (encrypt) privacy key — see Matter §4.4.3.1
	peerPrivacyKey []byte // inbound (decrypt) privacy key

	closed bool
}

// Config bundles the session parameters established during PASE /
// CASE.
type Config struct {
	// EncryptKey is the 16-byte AES-CCM key used for outbound traffic.
	EncryptKey []byte
	// DecryptKey is the 16-byte AES-CCM key used for inbound traffic.
	// Equal to EncryptKey for unidirectional sessions; differs for
	// bidirectional PASE / CASE (I2R vs. R2I).
	DecryptKey []byte
	// LocalNodeID is the 64-bit node identifier the local side carries
	// in outbound nonces.
	LocalNodeID uint64
	// PeerNodeID is the 64-bit node identifier of the peer.
	PeerNodeID uint64
	// PeerCATs is the set of CASE Authenticated Tags from the peer's
	// NOC subject. Used by the ACL gate to evaluate CAT-bearing ACE
	// subjects (Matter §9.10.5.6). PASE leaves this nil.
	PeerCATs []uint32
	// PeerSessionID is the SessionID the peer expects on its inbound
	// (i.e. our outbound) — InitiatorSessionID from PBKDFParamRequest
	// for PASE, Sigma1.InitiatorSessionID for CASE. Stamped into
	// Header.SessionID by [Bridge.sendReply] so the peer resolves the
	// session in its own table.
	PeerSessionID uint16
	// InitialCounter primes the outbound counter. Pass 0 to seed from
	// crypto/rand. Tests typically pass a deterministic value.
	InitialCounter uint32
}

// New constructs a Session from cfg. Returns an error when the keys
// are not 16 bytes each.
func New(cfg Config) (*Session, error) {
	enc, err := aesccm.New(cfg.EncryptKey)
	if err != nil {
		return nil, fmt.Errorf("channel: encrypt cipher: %w", err)
	}
	dec, err := aesccm.New(cfg.DecryptKey)
	if err != nil {
		return nil, fmt.Errorf("channel: decrypt cipher: %w", err)
	}
	var counter *mrp.Counter
	if cfg.InitialCounter != 0 {
		counter = mrp.NewCounterFromSeed(cfg.InitialCounter)
	} else {
		counter, err = mrp.NewCounter()
		if err != nil {
			return nil, fmt.Errorf("channel: counter seed: %w", err)
		}
	}
	var peerCATs []uint32
	if len(cfg.PeerCATs) > 0 {
		peerCATs = append(peerCATs, cfg.PeerCATs...)
	}
	return &Session{
		encKey:        append([]byte(nil), cfg.EncryptKey...),
		decKey:        append([]byte(nil), cfg.DecryptKey...),
		encCipher:     enc,
		decCipher:     dec,
		localNodeID:   cfg.LocalNodeID,
		peerNodeID:    cfg.PeerNodeID,
		peerCATs:      peerCATs,
		peerSessionID: cfg.PeerSessionID,
		out:           counter,
		in:            mrp.NewWindow(),
	}, nil
}

// PeerSessionID returns the SessionID the peer expects in our
// outbound Header.SessionID — see [Config.PeerSessionID] for the
// origin.
func (s *Session) PeerSessionID() uint16 { return s.peerSessionID }

// EncryptResult bundles the wire-ready bytes plus the counter the
// session consumed — the caller writes the counter into the outbound
// Message Header and concatenates Header || ciphertext for the wire.
type EncryptResult struct {
	Counter    uint32
	Ciphertext []byte // ciphertext || 16-byte MIC
}

// Encrypt seals plaintext under the next outbound counter. The header
// argument receives the chosen counter and the [Session.LocalNodeID]
// — the caller can serialise the populated header and prepend it to
// the ciphertext for the final wire frame.
//
// secFlags carries the Security Flags byte the caller intends to put
// on the wire — it participates in nonce construction. Pass 0 for
// unencrypted unicast (typical), or set the relevant bits per
// [..]/transport/message.
func (s *Session) Encrypt(header *message.Header, secFlags uint8, plaintext []byte) (*EncryptResult, error) {
	if s.closed {
		return nil, ErrSessionInactive
	}
	counter := s.out.Next()
	header.MessageCounter = counter
	header.HasSourceNodeID = true
	header.SourceNodeID = s.localNodeID

	nonce := buildNonce(secFlags, counter, s.localNodeID)
	aad := header.Marshal()
	sealed, err := s.encCipher.Seal(nil, nonce, plaintext, aad)
	if err != nil {
		return nil, fmt.Errorf("channel: seal: %w", err)
	}
	return &EncryptResult{Counter: counter, Ciphertext: sealed}, nil
}

// Decrypt verifies and unseals a frame the caller has already split
// into header + ciphertext. The header carries the Message Counter
// that drives both nonce construction (with the peer's Source Node
// ID) and replay detection.
//
// secFlags is the Security Flags byte from the received header.
//
// On success returns the plaintext and `duplicate=false`. When the
// counter has already been seen (MRP retransmit) the call still
// decrypts and returns the plaintext with `duplicate=true` —
// Matter §4.12 mandates we ack the duplicate but skip
// re-processing. Returning plain on duplicate lets the caller peek
// the ProtocolHeader (it lives inside the encrypted body) so the
// MRP layer can synthesise a StandaloneAck targeted at the
// duplicate's ExchangeID; without that ack the peer keeps
// retransmitting until the exchange falls over.
func (s *Session) Decrypt(header *message.Header, secFlags uint8, ciphertext []byte) (plain []byte, duplicate bool, err error) {
	if s.closed {
		return nil, false, ErrSessionInactive
	}
	srcNode := header.SourceNodeID
	if !header.HasSourceNodeID {
		// Inbound packets in a unicast session SHOULD carry a source
		// node ID; if absent, fall back to the configured peer.
		srcNode = s.peerNodeID
	}
	nonce := buildNonce(secFlags, header.MessageCounter, srcNode)
	aad := header.Marshal()
	plain, openErr := s.decCipher.Open(nil, nonce, ciphertext, aad)
	if openErr != nil {
		return nil, false, fmt.Errorf("%w: %w", ErrUnauthenticated, openErr)
	}
	if !s.in.Accept(header.MessageCounter) {
		// Authentic but already-seen counter — return plain so the
		// receiver can extract the ExchangeID and ack, but flag it
		// as a duplicate so the IM layer skips re-processing.
		return plain, true, nil
	}
	return plain, false, nil
}

// LocalNodeID returns the local node identifier configured at session
// construction.
func (s *Session) LocalNodeID() uint64 { return s.localNodeID }

// PeerNodeID returns the peer node identifier.
func (s *Session) PeerNodeID() uint64 { return s.peerNodeID }

// PeerCATs returns a copy of the peer's CASE Authenticated Tags
// captured at session establishment. Returns nil for PASE / sessions
// constructed without CAT material. The slice is a copy so callers
// can mutate it freely.
func (s *Session) PeerCATs() []uint32 {
	if len(s.peerCATs) == 0 {
		return nil
	}
	out := make([]uint32, len(s.peerCATs))
	copy(out, s.peerCATs)
	return out
}

// Close zeroises the session keys and prevents further Encrypt /
// Decrypt calls. Idempotent.
func (s *Session) Close() {
	s.closed = true
	for i := range s.encKey {
		s.encKey[i] = 0
	}
	for i := range s.decKey {
		s.decKey[i] = 0
	}
	s.privacyMu.Lock()
	for i := range s.privacyKey {
		s.privacyKey[i] = 0
	}
	for i := range s.peerPrivacyKey {
		s.peerPrivacyKey[i] = 0
	}
	s.privacyKey = nil
	s.peerPrivacyKey = nil
	s.privacyMu.Unlock()
}

// buildNonce assembles the 13-byte Matter Secure Channel nonce per
// Core Spec §4.4.3:
//
//	nonce[0]    = secFlags
//	nonce[1:5]  = counter (LE)
//	nonce[5:13] = sourceNodeID (LE)
func buildNonce(secFlags uint8, counter uint32, sourceNodeID uint64) []byte {
	n := make([]byte, aesccm.NonceSize)
	n[0] = secFlags
	binary.LittleEndian.PutUint32(n[1:5], counter)
	binary.LittleEndian.PutUint64(n[5:13], sourceNodeID)
	return n
}
