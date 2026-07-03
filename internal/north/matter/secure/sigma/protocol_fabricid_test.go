// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package sigma

import (
	"errors"
	"testing"
)

// fabricIDVerifier wraps the round-trip testVerifier and adds the
// optional [PeerFabricIDExtractor] surface, returning a configured
// fabric-id (and optional error) so the responder's Sigma3 fabric-id
// binding can be exercised in isolation.
type fabricIDVerifier struct {
	testVerifier
	fabricID uint64
	err      error
}

func (f fabricIDVerifier) PeerFabricIDFromNOC(_ []byte) (uint64, error) {
	return f.fabricID, f.err
}

// runSigma3WithResponderVerifier drives an honest CASE round-trip where
// the responder uses respVerifier (which may implement
// [PeerFabricIDExtractor]) and returns the responder's ProcessSigma3
// error. The responder identity is scoped to respFabricID.
func runSigma3WithResponderVerifier(t *testing.T, respFabricID uint64, respVerifier PeerVerifier) error {
	t.Helper()
	ipk := fabricIPK()
	initID := newTestIdentity(t, 0xAAAA, respFabricID, ipk)
	respID := newTestIdentity(t, 0xBBBB, respFabricID, ipk)

	initiator := NewInitiator(initID, testVerifier{}, 0x1001, [32]byte{0x01})
	responder := NewResponder(respID, respVerifier, 0x2001)

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
	return responder.ProcessSigma3(sigma3Bytes)
}

// TestProcessSigma3_FabricIDMatch_Succeeds asserts that an honest
// round-trip completes when the peer NOC subject fabric-id matches the
// responder-selected fabric. Mirrors matter.js CaseServer.ts:304 where
// a matching fabricId passes through to session creation.
func TestProcessSigma3_FabricIDMatch_Succeeds(t *testing.T) {
	err := runSigma3WithResponderVerifier(t, 0x2A, fabricIDVerifier{fabricID: 0x2A})
	if err != nil {
		t.Fatalf("ProcessSigma3 with matching fabric-id: %v, want nil", err)
	}
}

// TestProcessSigma3_FabricIDMismatch_Rejected asserts that the
// responder rejects a peer whose NOC subject fabric-id differs from the
// responder-selected fabric, even though the NOC chain verifies. This
// is the defense-in-depth check matter.js applies at
// CaseServer.ts:304-306 (`fabric.fabricId !== peerFabricId`).
func TestProcessSigma3_FabricIDMismatch_Rejected(t *testing.T) {
	err := runSigma3WithResponderVerifier(t, 0x2A, fabricIDVerifier{fabricID: 0x2B})
	if !errors.Is(err, ErrFabricIDMismatch) {
		t.Fatalf("ProcessSigma3 with mismatched fabric-id: err = %v, want ErrFabricIDMismatch", err)
	}
}

// TestProcessSigma3_FabricIDExtractorAbsent_SkipsCheck confirms that a
// verifier without the optional [PeerFabricIDExtractor] surface leaves
// the handshake unaffected — the check is opt-in, matching the
// PeerNodeID / PeerCATs optional-surface pattern.
func TestProcessSigma3_FabricIDExtractorAbsent_SkipsCheck(t *testing.T) {
	err := runSigma3WithResponderVerifier(t, 0x2A, testVerifier{})
	if err != nil {
		t.Fatalf("ProcessSigma3 without fabric-id extractor: %v, want nil", err)
	}
}

// TestProcessSigma3_FabricIDExtractorError_SkipsCheck confirms that an
// extractor which errors (peer NOC carried no decodable fabric-id) is
// treated as "unavailable" and does not fail the handshake, mirroring
// the PeerNodeID / PeerCATs error handling.
func TestProcessSigma3_FabricIDExtractorError_SkipsCheck(t *testing.T) {
	err := runSigma3WithResponderVerifier(t, 0x2A, fabricIDVerifier{fabricID: 0x99, err: errors.New("no fabric-id")})
	if err != nil {
		t.Fatalf("ProcessSigma3 with erroring fabric-id extractor: %v, want nil", err)
	}
}
