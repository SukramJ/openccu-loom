// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mattercert_test

// Crosses the seam between the production NOC verifier and the CASE
// responder: the chain check in VerifyAndExtractPubKey only proves a
// peer NOC links back to the fabric ROOT, so two fabrics provisioned
// from the same root would otherwise accept each other's certificates.
// The binding that stops that lives in the responder but can only run
// when the production verifier exposes the fabric-id, which is what
// these tests drive end-to-end.

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/secure/mattercert"
	"github.com/SukramJ/openccu-loom/internal/north/matter/secure/sigma"
)

// The production verifier must keep exposing the optional fabric-id
// surface — without it the responder's binding check silently turns
// into dead code in every shipped daemon.
var _ sigma.PeerFabricIDExtractor = (*mattercert.Verifier)(nil)

// buildNOCForFabric mints a NOC for (nodeID, fabricID) signed directly
// by rootPriv, together with its operational private key.
func buildNOCForFabric(t *testing.T, rootPriv *ecdsa.PrivateKey, nodeID, fabricID uint64) ([]byte, *ecdsa.PrivateKey) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	now := nowEpoch()
	raw := buildSignedCert(t, verifyTestCertOpts{
		notBefore:          now - 100,
		notAfter:           0,
		issuerRCACID:       0x0001,
		subjectHasNodeID:   true,
		subjectNodeID:      nodeID,
		subjectHasFabricID: true,
		subjectFabricID:    fabricID,
		pubKey:             marshalPub(priv),
	}, rootPriv)
	return raw, priv
}

// runCaseWithRealCerts drives a full Sigma1/2/3 handshake in which both
// ends carry real Matter NOCs issued by one root and the responder uses
// the production [mattercert.Verifier]. It returns the responder's
// ProcessSigma3 error.
func runCaseWithRealCerts(t *testing.T, responderFabricID, peerFabricID uint64) error {
	t.Helper()
	rootPriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("root GenerateKey: %v", err)
	}
	verifier, err := mattercert.NewVerifier(marshalPub(rootPriv), mattercert.SystemTime{})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	respNOC, respPriv := buildNOCForFabric(t, rootPriv, 0xB21D6E, responderFabricID)
	peerNOC, peerPriv := buildNOCForFabric(t, rootPriv, 0xC0FFEE, peerFabricID)

	// Both fabrics share the IPK: this is the insider threat model —
	// without a shared IPK the handshake never reaches Sigma3 at all.
	var ipk [16]byte
	if _, err := rand.Read(ipk[:]); err != nil {
		t.Fatalf("ipk: %v", err)
	}
	respID := &sigma.Identity{NOC: respNOC, PrivateKey: respPriv, NodeID: 0xB21D6E, FabricID: responderFabricID, IPK: ipk}
	peerID := &sigma.Identity{NOC: peerNOC, PrivateKey: peerPriv, NodeID: 0xC0FFEE, FabricID: peerFabricID, IPK: ipk}

	initiator := sigma.NewInitiator(peerID, verifier, 0x1001, [32]byte{0x07})
	responder := sigma.NewResponder(respID, verifier, 0x2001)

	s1, err := initiator.GenerateSigma1()
	if err != nil {
		t.Fatalf("GenerateSigma1: %v", err)
	}
	s2, err := responder.ProcessSigma1(s1)
	if err != nil {
		t.Fatalf("ProcessSigma1: %v", err)
	}
	s3, err := initiator.ProcessSigma2(s2)
	if err != nil {
		t.Fatalf("ProcessSigma2: %v", err)
	}
	return responder.ProcessSigma3(s3)
}

// TestVerifier_CaseHandshake_SameFabricAccepted is the control case: a
// peer whose NOC subject fabric-id matches the fabric the responder
// signed Sigma2 under completes the handshake.
func TestVerifier_CaseHandshake_SameFabricAccepted(t *testing.T) {
	t.Parallel()
	if err := runCaseWithRealCerts(t, 0xFAB1, 0xFAB1); err != nil {
		t.Fatalf("ProcessSigma3 for a same-fabric peer: %v, want nil", err)
	}
}

// TestVerifier_CaseHandshake_ForeignFabricRejected pins the binding
// itself: a NOC that chains to the same root but is scoped to a
// different fabric must not open a session on this fabric. Mirrors
// matter.js packages/protocol/src/session/case/CaseServer.ts
// (`if (fabric.fabricId !== peerFabricId) throw new UnexpectedDataError`).
func TestVerifier_CaseHandshake_ForeignFabricRejected(t *testing.T) {
	t.Parallel()
	err := runCaseWithRealCerts(t, 0xFAB1, 0xFAB2)
	if !errors.Is(err, sigma.ErrFabricIDMismatch) {
		t.Fatalf("ProcessSigma3 for a foreign-fabric peer: err = %v, want ErrFabricIDMismatch", err)
	}
}

// TestVerifier_PeerFabricIDFromNOC_ReadsSubjectFabricID pins the
// extractor in isolation so a decode-shape change surfaces here rather
// than as a skipped security check.
func TestVerifier_PeerFabricIDFromNOC_ReadsSubjectFabricID(t *testing.T) {
	t.Parallel()
	rootPriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("root GenerateKey: %v", err)
	}
	v, err := mattercert.NewVerifier(marshalPub(rootPriv), mattercert.SystemTime{})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	noc, _ := buildNOCForFabric(t, rootPriv, 0xC0FFEE, 0xFAB7)

	got, err := v.PeerFabricIDFromNOC(noc)
	if err != nil {
		t.Fatalf("PeerFabricIDFromNOC: %v", err)
	}
	if got != 0xFAB7 {
		t.Fatalf("PeerFabricIDFromNOC = %#x, want 0xFAB7", got)
	}
	if _, err := v.PeerFabricIDFromNOC([]byte{0x00}); err == nil {
		t.Error("PeerFabricIDFromNOC on undecodable bytes returned nil error")
	}
}
