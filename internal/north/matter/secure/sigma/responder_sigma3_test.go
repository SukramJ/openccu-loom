// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package sigma

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/secure/aesccm"
)

// nocSubjectVerifier resolves the NodeID and the CASE Authenticated
// Tags per NOC fixture, so one Responder can be driven with two peers
// whose certificates carry different subjects — the shape the
// authentication ordering has to survive.
type nocSubjectVerifier struct {
	testVerifier
	nodeIDs map[string]uint64
	cats    map[string][]uint32
}

func (v nocSubjectVerifier) PeerNodeIDFromNOC(noc []byte) (uint64, error) {
	id, ok := v.nodeIDs[string(noc)]
	if !ok {
		return 0, errors.New("test verifier: no node id for noc fixture")
	}
	return id, nil
}

func (v nocSubjectVerifier) PeerCATsFromNOC(noc []byte) ([]uint32, error) {
	return v.cats[string(noc)], nil
}

// newTestNOC returns a fresh uncompressed P-256 public key usable as a
// NOC fixture for [testVerifier] — chain verification accepts it, so a
// forged Sigma3 built around it reaches the transcript signature check.
func newTestNOC(t *testing.T) []byte {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	return elliptic.Marshal(elliptic.P256(), priv.X, priv.Y) //nolint:staticcheck // SA1019: test fixture
}

// forgeSigma3 seals a TBE3 carrying noc plus an all-zero signature
// under the very S3K the responder derived for this exchange (salt =
// IPK || SHA256(sigma1 || sigma2), Matter §4.14.2.3). Any node that
// holds the fabric IPK and completed Sigma1/Sigma2 can build this, so
// AES-CCM open, NOC-chain verification and the fabric-id check all
// pass and only the transcript signature rejects the message.
func forgeSigma3(t *testing.T, r *Responder, ipk [16]byte, sigma1Bytes []byte, sigma2 Sigma2, noc []byte) []byte {
	t.Helper()
	s3k, err := hkdfDerive(r.ECDHSharedSecret(), sigma3Salt(ipk[:], sigma1Bytes, sigma2.Marshal()), HKDFInfoSigma3, SessionKeySize)
	if err != nil {
		t.Fatalf("hkdfDerive S3K: %v", err)
	}
	cipher, err := aesccm.New(s3k)
	if err != nil {
		t.Fatalf("aesccm.New: %v", err)
	}
	enc3, err := cipher.Seal(nil, nonceTBE3, marshalTBE3(TBE3Plaintext{
		InitiatorNOC: noc,
		Signature:    make([]byte, 64),
	}), nil)
	if err != nil {
		t.Fatalf("seal TBE3: %v", err)
	}
	return Sigma3{Encrypted3: enc3}.Marshal()
}

// TestResponderSigma3SignatureFailureCommitsNoPeerIdentity pins that a
// Sigma3 whose transcript signature does not verify leaves no trace of
// the certificate it carried. Everything up to the signature check is
// reproducible by any node holding the fabric IPK, so committing the
// NOC subject before it authenticates lets one fabric member install
// another member's CASE Authenticated Tags on the session — the ACL
// gate then matches CAT-scoped ACEs the peer's own certificate never
// granted (Matter §9.10.5.6). matter.js keeps the decoded subject in
// locals until after verifyEcdsa
// (packages/protocol/src/session/case/CaseServer.ts:302-327).
func TestResponderSigma3SignatureFailureCommitsNoPeerIdentity(t *testing.T) {
	t.Parallel()
	ipk := fabricIPK()
	const fabricID = uint64(0x2A)

	attackerID := newTestIdentity(t, 0xAAAA, fabricID, ipk)
	victimNOC := newTestNOC(t)
	ver := nocSubjectVerifier{
		nodeIDs: map[string]uint64{
			string(attackerID.NOC): 0xAAAA,
			string(victimNOC):      0x5151,
		},
		cats: map[string][]uint32{
			string(victimNOC): {0xABCD0001},
		},
	}
	responder := NewResponder(newTestIdentity(t, 0xBBBB, fabricID, ipk), ver, 0x2001)
	initiator := NewInitiator(attackerID, testVerifier{}, 0x1001, [32]byte{0x01})

	sigma1Bytes, err := initiator.GenerateSigma1()
	if err != nil {
		t.Fatalf("GenerateSigma1: %v", err)
	}
	sigma2, err := responder.ProcessSigma1(sigma1Bytes)
	if err != nil {
		t.Fatalf("ProcessSigma1: %v", err)
	}

	forged := forgeSigma3(t, responder, ipk, sigma1Bytes, sigma2, victimNOC)
	if err := responder.ProcessSigma3(forged); err == nil {
		t.Fatal("ProcessSigma3 accepted a Sigma3 with an unverifiable transcript signature")
	}

	if got := responder.PeerCATs(); got != nil {
		t.Errorf("PeerCATs() = %v after a failed Sigma3, want nil — the unauthenticated NOC's tags were committed", got)
	}
	if got := responder.PeerNodeID(); got != 0 {
		t.Errorf("PeerNodeID() = %#x after a failed Sigma3, want 0 — the unauthenticated NOC's subject was committed", got)
	}
	if _, ok := responder.SessionKeys(); ok {
		t.Error("SessionKeys() available after a failed Sigma3")
	}
}

// TestResponderSigma3FailureEndsHandshakeUntilNewSigma1 pins the
// teardown half: a Sigma3 that does not authenticate ends the
// handshake, so a second Sigma3 cannot be verified against the same
// Sigma2. matter.js reads exactly one Sigma3 per exchange and lets the
// first failure end it (CaseServer.ts:275-309); only a fresh Sigma1
// restarts the responder.
func TestResponderSigma3FailureEndsHandshakeUntilNewSigma1(t *testing.T) {
	t.Parallel()
	ipk := fabricIPK()
	const fabricID = uint64(0x2A)

	attackerID := newTestIdentity(t, 0xAAAA, fabricID, ipk)
	victimNOC := newTestNOC(t)
	ver := nocSubjectVerifier{
		nodeIDs: map[string]uint64{
			string(attackerID.NOC): 0xAAAA,
			string(victimNOC):      0x5151,
		},
		cats: map[string][]uint32{
			string(victimNOC): {0xABCD0001},
		},
	}
	responder := NewResponder(newTestIdentity(t, 0xBBBB, fabricID, ipk), ver, 0x2001)
	initiator := NewInitiator(attackerID, testVerifier{}, 0x1001, [32]byte{0x01})

	sigma1Bytes, err := initiator.GenerateSigma1()
	if err != nil {
		t.Fatalf("GenerateSigma1: %v", err)
	}
	sigma2, err := responder.ProcessSigma1(sigma1Bytes)
	if err != nil {
		t.Fatalf("ProcessSigma1: %v", err)
	}
	honestSigma3, err := initiator.ProcessSigma2(sigma2)
	if err != nil {
		t.Fatalf("ProcessSigma2: %v", err)
	}

	forged := forgeSigma3(t, responder, ipk, sigma1Bytes, sigma2, victimNOC)
	if err := responder.ProcessSigma3(forged); err == nil {
		t.Fatal("ProcessSigma3 accepted a forged Sigma3")
	}
	if err := responder.ProcessSigma3(honestSigma3); !errors.Is(err, ErrSessionState) {
		t.Fatalf("second Sigma3 after a failed one: err = %v, want ErrSessionState — the failed handshake stayed open", err)
	}
	if got := responder.PeerCATs(); got != nil {
		t.Errorf("PeerCATs() = %v, want nil", got)
	}

	// A fresh Sigma1 restarts the responder: the failure state must not
	// wedge the per-exchange handler a controller reuses after a retry.
	retry := NewInitiator(attackerID, testVerifier{}, 0x1002, [32]byte{0x02})
	retrySigma1, err := retry.GenerateSigma1()
	if err != nil {
		t.Fatalf("retry GenerateSigma1: %v", err)
	}
	retrySigma2, err := responder.ProcessSigma1(retrySigma1)
	if err != nil {
		t.Fatalf("retry ProcessSigma1: %v", err)
	}
	retrySigma3, err := retry.ProcessSigma2(retrySigma2)
	if err != nil {
		t.Fatalf("retry ProcessSigma2: %v", err)
	}
	if err := responder.ProcessSigma3(retrySigma3); err != nil {
		t.Fatalf("retry ProcessSigma3: %v", err)
	}
	if got := responder.PeerNodeID(); got != 0xAAAA {
		t.Errorf("PeerNodeID() = %#x after the retry, want 0xAAAA", got)
	}
	if got := responder.PeerCATs(); got != nil {
		t.Errorf("PeerCATs() = %v after the retry, want nil — the attacker's own NOC carries no tags", got)
	}
}
