// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package sigma

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"testing"
)

// testVerifier is a stand-in for the M5/M6 NOC chain validator. It
// returns the public key encoded into the "noc" field directly — for
// test purposes the noc bytes ARE the uncompressed public key. The
// production implementation parses TLV-encoded NOCs and verifies the
// signature chain against the fabric root.
type testVerifier struct{}

func (testVerifier) VerifyAndExtractPubKey(noc, _ []byte) (*ecdsa.PublicKey, error) {
	x, y := elliptic.Unmarshal(elliptic.P256(), noc) //nolint:staticcheck // SA1019: test fixture
	if x == nil {
		return nil, errors.New("test verifier: invalid noc fixture")
	}
	return &ecdsa.PublicKey{Curve: elliptic.P256(), X: x, Y: y}, nil
}

// newTestIdentity returns a fresh Identity whose NOC is just the
// uncompressed P-256 public key — a shortcut for testing the Sigma
// protocol layer without TLV-encoded certificates. Both the
// initiator and responder identities in a single round-trip share
// the same fabric, so the IPK is taken from a per-test fixture
// supplied by the caller.
func newTestIdentity(t *testing.T, nodeID, fabricID uint64, ipk [16]byte) *Identity {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	noc := elliptic.Marshal(elliptic.P256(), priv.X, priv.Y) //nolint:staticcheck // SA1019: test fixture
	id := &Identity{
		NOC:        noc,
		ICAC:       nil,
		PrivateKey: priv,
		NodeID:     nodeID,
		FabricID:   fabricID,
		IPK:        ipk,
	}
	return id
}

// fabricIPK returns a deterministic per-test IPK fixture. Both peers
// in a round-trip must share the same IPK because it prefixes every
// CASE Sigma HKDF salt — diverging IPKs produce diverging session
// keys and Sigma2 fails on the initiator side with ErrUnauthenticated.
func fabricIPK() [16]byte {
	var ipk [16]byte
	if _, err := rand.Read(ipk[:]); err != nil {
		panic("test ipk: " + err.Error())
	}
	return ipk
}

// TestRoundTripDerivesMatchingKeys is the central correctness
// assertion: an honest initiator and responder, each with a valid
// NOC, end the exchange holding the same I2R / R2I /
// AttestationChallenge bytes.
func TestRoundTripDerivesMatchingKeys(t *testing.T) {
	ipk := fabricIPK()
	initID := newTestIdentity(t, 0xAAAA, 1, ipk)
	respID := newTestIdentity(t, 0xBBBB, 1, ipk)
	verifier := testVerifier{}

	initiator := NewInitiator(initID, verifier, 0x1001, [32]byte{0xDE, 0xAD, 0xBE, 0xEF})
	responder := NewResponder(respID, verifier, 0x2001)

	sigma1Bytes, err := initiator.GenerateSigma1()
	if err != nil {
		t.Fatalf("GenerateSigma1: %v", err)
	}

	sigma2, err := responder.ProcessSigma1(sigma1Bytes)
	if err != nil {
		t.Fatalf("ProcessSigma1: %v", err)
	}

	sigma3Bytes, err := initiator.ProcessSigma2(sigma2)
	if err != nil {
		t.Fatalf("ProcessSigma2: %v", err)
	}

	if err := responder.ProcessSigma3(sigma3Bytes); err != nil {
		t.Fatalf("ProcessSigma3: %v", err)
	}

	initKeys, ok := initiator.SessionKeys()
	if !ok {
		t.Fatal("initiator has no session keys after success")
	}
	respKeys, ok := responder.SessionKeys()
	if !ok {
		t.Fatal("responder has no session keys after success")
	}
	if !constantTimeKeysEqual(initKeys, respKeys) {
		t.Fatalf("key mismatch:\n  initiator I2R=% X R2I=% X AC=% X\n  responder I2R=% X R2I=% X AC=% X",
			initKeys.I2RKey, initKeys.R2IKey, initKeys.AttestationChallenge,
			respKeys.I2RKey, respKeys.R2IKey, respKeys.AttestationChallenge)
	}
}

// TestTamperedSigma2EncryptedRejected — flipping a byte inside the
// encrypted2 envelope must surface ErrUnauthenticated on the
// initiator.
func TestTamperedSigma2EncryptedRejected(t *testing.T) {
	ipk := fabricIPK()
	initID := newTestIdentity(t, 0xAAAA, 1, ipk)
	respID := newTestIdentity(t, 0xBBBB, 1, ipk)
	verifier := testVerifier{}
	initiator := NewInitiator(initID, verifier, 1, [32]byte{})
	responder := NewResponder(respID, verifier, 2)
	sigma1Bytes, _ := initiator.GenerateSigma1()
	sigma2, _ := responder.ProcessSigma1(sigma1Bytes)
	sigma2.Encrypted2[0] ^= 0x01
	if _, err := initiator.ProcessSigma2(sigma2); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("err=%v, want ErrUnauthenticated", err)
	}
}

// TestTamperedResponderEphPubKeyRejected — modifying the responder's
// ephemeral pubkey must invalidate the AAD-bound encrypted2 (the
// transcript hash diverges) AND yield a different ECDH shared secret.
func TestTamperedResponderEphPubKeyRejected(t *testing.T) {
	ipk := fabricIPK()
	initID := newTestIdentity(t, 0xAAAA, 1, ipk)
	respID := newTestIdentity(t, 0xBBBB, 1, ipk)
	verifier := testVerifier{}
	initiator := NewInitiator(initID, verifier, 1, [32]byte{})
	responder := NewResponder(respID, verifier, 2)
	sigma1Bytes, _ := initiator.GenerateSigma1()
	sigma2, _ := responder.ProcessSigma1(sigma1Bytes)
	// Flip a byte in the X coordinate (offset 1..32 of the
	// uncompressed encoding).
	sigma2.ResponderEphPubKey[1] ^= 0x01
	if _, err := initiator.ProcessSigma2(sigma2); err == nil {
		t.Fatal("expected error on tampered responderEphPubKey")
	}
}

// TestSigma3WithTamperedEncryptedRejected — the responder must reject
// a Sigma3 whose encrypted3 ciphertext has been corrupted on the wire.
// Flips a byte deep inside the encrypted3 octet-string so the outer
// TLV envelope still decodes — the AES-CCM tag check is what should
// fire, not a TLV parse error.
func TestSigma3WithTamperedEncryptedRejected(t *testing.T) {
	ipk := fabricIPK()
	initID := newTestIdentity(t, 0xAAAA, 1, ipk)
	respID := newTestIdentity(t, 0xBBBB, 1, ipk)
	verifier := testVerifier{}
	initiator := NewInitiator(initID, verifier, 1, [32]byte{})
	responder := NewResponder(respID, verifier, 2)
	sigma1Bytes, _ := initiator.GenerateSigma1()
	sigma2, _ := responder.ProcessSigma1(sigma1Bytes)
	sigma3Bytes, _ := initiator.ProcessSigma2(sigma2)
	// Flip a byte well past the TLV header so the envelope parses but
	// the inner ciphertext fails the AES-CCM tag check.
	tampIdx := len(sigma3Bytes) - 8
	sigma3Bytes[tampIdx] ^= 0x01
	if err := responder.ProcessSigma3(sigma3Bytes); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("err=%v, want ErrUnauthenticated", err)
	}
}

// TestSigma1RejectsBadEphPoint catches malformed ephemeral keys.
// Builds a spec-shaped Sigma1 TLV but with an off-curve eph point so
// the responder reaches the validatePoint check rather than failing
// earlier on length or TLV decoding.
func TestSigma1RejectsBadEphPoint(t *testing.T) {
	respID := newTestIdentity(t, 0xBBBB, 1, fabricIPK())
	verifier := testVerifier{}
	responder := NewResponder(respID, verifier, 2)

	badEph := make([]byte, EphPubKeySize)
	badEph[0] = 0x05 // wrong prefix → off-curve
	bad := Sigma1{
		InitiatorRandom:    [RandomSize]byte{},
		InitiatorSessionID: 1,
		DestinationID:      [32]byte{},
		InitiatorEphPubKey: badEph,
	}.Marshal()
	if _, err := responder.ProcessSigma1(bad); !errors.Is(err, ErrInvalidPoint) {
		t.Fatalf("err=%v, want ErrInvalidPoint", err)
	}
}

// TestUnmarshalSigma1_ResumptionTagsAllOrNothing pins the chip
// CASESession.cpp:2438-2449 strictness rule: tags 6 (resumptionId)
// and 7 (initiatorResumeMic) MUST appear together. A Sigma1 with
// only one of the two is rejected with `CHIP_ERROR_UNEXPECTED_TLV_ELEMENT`
// in chip; OpenCCU-Loom mirrors the rejection so a malformed
// commissioner surfaces the same error instead of silently
// downgrading to a fresh CASE handshake.
func TestUnmarshalSigma1_ResumptionTagsAllOrNothing(t *testing.T) {
	t.Parallel()
	ephPub := make([]byte, EphPubKeySize)
	ephPub[0] = 0x04 // uncompressed marker (decoder only inspects length here)
	base := Sigma1{
		InitiatorRandom:    [RandomSize]byte{1, 2, 3},
		InitiatorSessionID: 0x1234,
		DestinationID:      [32]byte{0xAA},
		InitiatorEphPubKey: ephPub,
	}

	t.Run("resumptionID_only", func(t *testing.T) {
		t.Parallel()
		s := base
		s.ResumptionID = make([]byte, 16)
		if _, err := UnmarshalSigma1(s.Marshal()); err == nil {
			t.Fatal("expected error for resumptionID without initiatorResumeMic, got nil")
		}
	})
	t.Run("resumeMic_only", func(t *testing.T) {
		t.Parallel()
		s := base
		s.InitiatorResumeMIC = make([]byte, 16)
		if _, err := UnmarshalSigma1(s.Marshal()); err == nil {
			t.Fatal("expected error for initiatorResumeMic without resumptionID, got nil")
		}
	})
	t.Run("both_present", func(t *testing.T) {
		t.Parallel()
		s := base
		s.ResumptionID = make([]byte, 16)
		s.InitiatorResumeMIC = make([]byte, 16)
		out, err := UnmarshalSigma1(s.Marshal())
		if err != nil {
			t.Fatalf("both fields present: unexpected error: %v", err)
		}
		if len(out.ResumptionID) != 16 || len(out.InitiatorResumeMIC) != 16 {
			t.Errorf("round-trip mismatch: rid=%d bytes, mic=%d bytes",
				len(out.ResumptionID), len(out.InitiatorResumeMIC))
		}
	})
	t.Run("both_absent", func(t *testing.T) {
		t.Parallel()
		s := base
		out, err := UnmarshalSigma1(s.Marshal())
		if err != nil {
			t.Fatalf("no resumption fields: unexpected error: %v", err)
		}
		if len(out.ResumptionID) != 0 || len(out.InitiatorResumeMIC) != 0 {
			t.Errorf("unexpected resumption residue: rid=%d, mic=%d",
				len(out.ResumptionID), len(out.InitiatorResumeMIC))
		}
	})
}

// TestStateMachineGuards locks the Initiator state machine.
func TestStateMachineGuards(t *testing.T) {
	initID := newTestIdentity(t, 0xAAAA, 1, fabricIPK())
	verifier := testVerifier{}
	initiator := NewInitiator(initID, verifier, 1, [32]byte{})
	if _, err := initiator.GenerateSigma1(); err != nil {
		t.Fatal(err)
	}
	if _, err := initiator.GenerateSigma1(); !errors.Is(err, ErrSessionState) {
		t.Fatalf("err=%v, want ErrSessionState (double GenerateSigma1)", err)
	}
}

// TestProcessSigma2BeforeSigma1Fails — invariant guard.
func TestProcessSigma2BeforeSigma1Fails(t *testing.T) {
	initID := newTestIdentity(t, 0xAAAA, 1, fabricIPK())
	verifier := testVerifier{}
	initiator := NewInitiator(initID, verifier, 1, [32]byte{})
	if _, err := initiator.ProcessSigma2(Sigma2{}); !errors.Is(err, ErrSessionState) {
		t.Fatalf("err=%v, want ErrSessionState", err)
	}
}

// TestProcessSigma3BeforeSigma1Fails — responder-side invariant.
func TestProcessSigma3BeforeSigma1Fails(t *testing.T) {
	respID := newTestIdentity(t, 0xBBBB, 1, fabricIPK())
	verifier := testVerifier{}
	responder := NewResponder(respID, verifier, 2)
	if err := responder.ProcessSigma3(nil); !errors.Is(err, ErrSessionState) {
		t.Fatalf("err=%v, want ErrSessionState", err)
	}
}

// TestSessionKeysHiddenBeforeFinish guards against premature key
// exposure to caller code that forgets to check the second return
// value.
func TestSessionKeysHiddenBeforeFinish(t *testing.T) {
	initID := newTestIdentity(t, 0xAAAA, 1, fabricIPK())
	verifier := testVerifier{}
	initiator := NewInitiator(initID, verifier, 1, [32]byte{})
	if _, ok := initiator.SessionKeys(); ok {
		t.Fatal("SessionKeys ok=true before any call")
	}
	_, _ = initiator.GenerateSigma1()
	if _, ok := initiator.SessionKeys(); ok {
		t.Fatal("SessionKeys ok=true after only Sigma1")
	}
}

// TestSignatureRoundTripsBitExact exercises the ECDSA pack/unpack
// helpers in isolation.
func TestSignatureRoundTripsBitExact(t *testing.T) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	noc := []byte("noc")
	icac := []byte("icac")
	selfEph := []byte("selfEph")
	peerEph := []byte("peerEph")
	sig, err := signTranscript(priv, noc, icac, selfEph, peerEph)
	if err != nil {
		t.Fatal(err)
	}
	if len(sig) != 64 {
		t.Fatalf("sig length=%d, want 64", len(sig))
	}
	if err := verifyTranscript(&priv.PublicKey, sig, noc, icac, selfEph, peerEph); err != nil {
		t.Fatal(err)
	}
	// Tampered signature must fail.
	sig[0] ^= 0x01
	if err := verifyTranscript(&priv.PublicKey, sig, noc, icac, selfEph, peerEph); !errors.Is(err, ErrSignatureInvalid) {
		t.Fatalf("err=%v, want ErrSignatureInvalid", err)
	}
}

// TestVerifyTranscriptRejectsBadLength catches signature-length
// regressions.
func TestVerifyTranscriptRejectsBadLength(t *testing.T) {
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err := verifyTranscript(&priv.PublicKey, make([]byte, 63), nil, nil, nil, nil); !errors.Is(err, ErrSignatureInvalid) {
		t.Fatalf("err=%v, want ErrSignatureInvalid", err)
	}
}

// TestProcessSigma1ConcurrentReplayDeterministic reproduces the
// Apple-iOS multicast-Sigma1 race: the commissioner blasts the same
// Sigma1 bytes onto IPv4 + IPv6-LL + IPv6-Global simultaneously and
// the receiver handler is invoked N times in parallel. The pre-mutex
// implementation generated a fresh ECDH ephemeral on each invocation
// and emitted N divergent Sigma2 replies; the commissioner picked one,
// dropped the rest, and aborted the handshake.
//
// Post-fix the mutex + sigma1Bytes-equality cache guarantee that every
// concurrent invocation returns byte-identical Sigma2 — and that
// downstream Sigma3 still completes against any of the returned
// Sigma2's. Run with -race to catch unsynchronized state access.
//
// Mirrors matter.js's `CaseServer.ts::onSigma1` which serialises every
// per-exchange invocation behind a single async lock (`fabric.locked`).
func TestProcessSigma1ConcurrentReplayDeterministic(t *testing.T) {
	t.Parallel()
	ipk := fabricIPK()
	initID := newTestIdentity(t, 0xAAAA, 1, ipk)
	respID := newTestIdentity(t, 0xBBBB, 1, ipk)
	verifier := testVerifier{}

	initiator := NewInitiator(initID, verifier, 0x1001, [32]byte{0xDE, 0xAD, 0xBE, 0xEF})
	responder := NewResponder(respID, verifier, 0x2001)

	sigma1Bytes, err := initiator.GenerateSigma1()
	if err != nil {
		t.Fatalf("GenerateSigma1: %v", err)
	}

	// Fan out N parallel ProcessSigma1 calls on the SAME Responder
	// with identical sigma1Bytes — simulates Apple's multicast burst.
	const fanout = 5
	type result struct {
		sigma2 Sigma2
		err    error
	}
	results := make(chan result, fanout)
	start := make(chan struct{})
	for i := 0; i < fanout; i++ {
		go func() {
			<-start
			s2, err := responder.ProcessSigma1(sigma1Bytes)
			results <- result{sigma2: s2, err: err}
		}()
	}
	close(start)

	first := <-results
	if first.err != nil {
		t.Fatalf("ProcessSigma1[0]: %v", first.err)
	}
	// Every subsequent invocation must return the SAME bytes — the
	// cached sigma2 from the first call, not a freshly-rerun handshake.
	for i := 1; i < fanout; i++ {
		r := <-results
		if r.err != nil {
			t.Errorf("ProcessSigma1[%d]: unexpected err=%v", i, r.err)
			continue
		}
		if !bytesEqual(r.sigma2.Encrypted2, first.sigma2.Encrypted2) {
			t.Errorf("ProcessSigma1[%d]: Encrypted2 diverged — race regressed", i)
		}
		if !bytesEqual(r.sigma2.ResponderEphPubKey, first.sigma2.ResponderEphPubKey) {
			t.Errorf("ProcessSigma1[%d]: ResponderEphPubKey diverged — race regressed", i)
		}
		if r.sigma2.ResponderRandom != first.sigma2.ResponderRandom {
			t.Errorf("ProcessSigma1[%d]: ResponderRandom diverged — race regressed", i)
		}
	}

	// Downstream Sigma3 still succeeds against the deduped Sigma2.
	sigma3Bytes, err := initiator.ProcessSigma2(first.sigma2)
	if err != nil {
		t.Fatalf("ProcessSigma2 after concurrent ProcessSigma1: %v", err)
	}
	if err := responder.ProcessSigma3(sigma3Bytes); err != nil {
		t.Fatalf("ProcessSigma3 after concurrent ProcessSigma1: %v", err)
	}
}

// bytesEqual is a tiny stand-in for bytes.Equal that keeps this test
// file free of yet another import.
func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestComputeDestinationID_Deterministic asserts the public helper
// returns byte-identical bytes for the same inputs across invocations.
// Underlies the multi-fabric resolver: a single fabric's destinationID
// MUST be a pure function of (opIPK, random, rootPub, fabricID, nodeID),
// otherwise the inbound Sigma1 match would race.
func TestComputeDestinationID_Deterministic(t *testing.T) {
	t.Parallel()
	var ipk [16]byte
	for i := range ipk {
		ipk[i] = byte(0x10 + i)
	}
	var rnd [RandomSize]byte
	for i := range rnd {
		rnd[i] = byte(i)
	}
	rootPub := make([]byte, 65)
	rootPub[0] = 0x04
	for i := 1; i < 65; i++ {
		rootPub[i] = byte(i * 3)
	}
	a := ComputeDestinationID(ipk, rnd, rootPub, 0xAAAAAAAAAAAAAAAA, 0xBBBBBBBBBBBBBBBB)
	b := ComputeDestinationID(ipk, rnd, rootPub, 0xAAAAAAAAAAAAAAAA, 0xBBBBBBBBBBBBBBBB)
	if a != b {
		t.Fatalf("ComputeDestinationID non-deterministic: %x vs %x", a, b)
	}
	// Differs across distinct fabricID.
	c := ComputeDestinationID(ipk, rnd, rootPub, 0xAAAAAAAAAAAAAAAB, 0xBBBBBBBBBBBBBBBB)
	if a == c {
		t.Errorf("ComputeDestinationID collides across distinct fabricIDs")
	}
}

// multiFabricResolver is a test fixture implementing IdentityResolver.
type multiFabricResolver struct {
	identities []*Identity
	verifier   PeerVerifier
	rootPub    [][]byte // parallel to identities
}

func (m *multiFabricResolver) ResolveSigma1Destination(dest [32]byte, rnd [RandomSize]byte) (*Identity, PeerVerifier, bool) {
	for i, id := range m.identities {
		cand := ComputeDestinationID(id.IPK, rnd, m.rootPub[i], id.FabricID, id.NodeID)
		if cand == dest {
			return id, m.verifier, true
		}
	}
	return nil, nil, false
}

// TestResponder_MultiFabric_PicksByDestinationID asserts the resolver
// swaps the responder identity per inbound Sigma1.DestinationID.
// Reproduces Apple Multi-Admin: two fabrics installed, an initiator
// targeting fabric #1 must receive Sigma2 signed under fabric #1's
// NOC even when fabric #2 was registered after #1.
func TestResponder_MultiFabric_PicksByDestinationID(t *testing.T) {
	t.Parallel()
	ipkA := fabricIPK()
	ipkB := fabricIPK()

	idA := newTestIdentity(t, 0x1111, 1, ipkA) // FabricID 1
	idB := newTestIdentity(t, 0x2222, 2, ipkB) // FabricID 2
	verifier := testVerifier{}

	// Synthetic 65-byte root pubs distinguished only by a label byte —
	// real bridges feed the live fabric root, but the resolver only
	// HMACs them so a fixed pattern is enough.
	rootPubA := make([]byte, 65)
	rootPubA[0] = 0x04
	rootPubA[1] = 0xAA
	rootPubB := make([]byte, 65)
	rootPubB[0] = 0x04
	rootPubB[1] = 0xBB

	resolver := &multiFabricResolver{
		identities: []*Identity{idA, idB},
		verifier:   verifier,
		rootPub:    [][]byte{rootPubA, rootPubB},
	}

	// Initiator A targets fabric 1. Its Sigma1 carries
	// DestinationID computed for idA.
	initA := NewInitiator(idA, verifier, 0x1001, [RandomSize]byte{0xDE, 0xAD, 0xBE, 0xEF})
	sigma1A, err := initA.GenerateSigma1()
	if err != nil {
		t.Fatalf("init A: %v", err)
	}
	parsedA, err := UnmarshalSigma1(sigma1A)
	if err != nil {
		t.Fatalf("parse A: %v", err)
	}
	parsedA.DestinationID = ComputeDestinationID(idA.IPK, parsedA.InitiatorRandom, rootPubA, idA.FabricID, idA.NodeID)
	sigma1A = parsedA.Marshal()

	// Responder seeded with idB (the wrong default) but with the
	// resolver attached — should swap to idA on inbound.
	respWrongDefault := NewResponder(idB, verifier, 0x2001)
	respWrongDefault.SetIdentityResolver(resolver)

	if _, err := respWrongDefault.ProcessSigma1(sigma1A); err != nil {
		t.Fatalf("ProcessSigma1: %v", err)
	}
	fIdx, nodeID, ok := respWrongDefault.SessionIdentity()
	if !ok {
		t.Fatal("SessionIdentity ok=false after ProcessSigma1")
	}
	if nodeID != idA.NodeID {
		t.Errorf("resolver picked wrong identity: nodeID=0x%x, want 0x%x (idA)", nodeID, idA.NodeID)
	}
	_ = fIdx // FabricIndex left at 0 in this test fixture; production stamps it on Identity.
}

// TestResponder_MultiFabric_NoMatchFallsBack asserts that a Sigma1
// whose DestinationID matches NO installed fabric falls back to the
// responder's constructor-time identity (single-fabric / test path).
// Production resolvers should treat the miss as "no shared trust
// roots" and reject, but the protocol layer keeps the fallback so
// single-fabric flows aren't broken by an over-eager resolver.
func TestResponder_MultiFabric_NoMatchFallsBack(t *testing.T) {
	t.Parallel()
	ipk := fabricIPK()
	idDefault := newTestIdentity(t, 0xCCCC, 5, ipk)
	verifier := testVerifier{}

	resolver := &multiFabricResolver{
		// Empty fabric set — resolver always returns false.
		identities: nil,
		verifier:   verifier,
		rootPub:    nil,
	}

	resp := NewResponder(idDefault, verifier, 0x3001)
	resp.SetIdentityResolver(resolver)

	// Drive a real Sigma1 from a matching initiator so the message
	// parses cleanly. The DestinationID is whatever the initiator
	// stamps; the resolver returns false, fallback kicks in.
	init := NewInitiator(idDefault, verifier, 0x4001, [RandomSize]byte{1, 2, 3})
	sigma1, err := init.GenerateSigma1()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resp.ProcessSigma1(sigma1); err != nil {
		t.Fatalf("ProcessSigma1: %v", err)
	}
	_, nodeID, ok := resp.SessionIdentity()
	if !ok {
		t.Fatal("SessionIdentity ok=false")
	}
	if nodeID != idDefault.NodeID {
		t.Errorf("fallback identity nodeID=0x%x, want 0x%x", nodeID, idDefault.NodeID)
	}
}
