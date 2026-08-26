// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package sigma

import (
	"testing"
)

// catsVerifier wraps the round-trip testVerifier and adds the optional
// [PeerCATsExtractor] surface with a settable tag list, so one
// Responder can serve two consecutive handshakes whose peers carry
// different CASE Authenticated Tags.
type catsVerifier struct {
	testVerifier
	cats []uint32
}

func (c *catsVerifier) PeerCATsFromNOC(_ []byte) ([]uint32, error) { return c.cats, nil }

// runHandshakeAgainst drives one honest Sigma1/2/3 round-trip from a
// freshly-minted initiator against responder and returns the Sigma2 the
// responder produced. Every step failure is fatal.
func runHandshakeAgainst(t *testing.T, responder *Responder, ipk [16]byte, fabricID, initNodeID uint64) Sigma2 {
	t.Helper()
	initID := newTestIdentity(t, initNodeID, fabricID, ipk)
	//nolint:gosec // G115: test-only node id fits the destination fixture byte
	initiator := NewInitiator(initID, testVerifier{}, 0x1001, [32]byte{byte(initNodeID)})
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
	if err := responder.ProcessSigma3(s3); err != nil {
		t.Fatalf("ProcessSigma3: %v", err)
	}
	return s2
}

// TestResponderResetForNewSigma1_ClearsPeerCATs pins that the peer's
// authenticated-subject state does not survive into the next
// handshake: a peer with no CAT in its certificate must not be matched
// by the CAT-scoped ACEs written for the previous peer. The reset is
// what keeps the accessors honest for the whole span of the new
// handshake; ProcessSigma3 replaces the tag set again once the new
// peer authenticates.
func TestResponderResetForNewSigma1_ClearsPeerCATs(t *testing.T) {
	t.Parallel()
	ipk := fabricIPK()
	const fabricID = uint64(0x2A)
	ver := &catsVerifier{cats: []uint32{0xABCD0001}}
	responder := NewResponder(newTestIdentity(t, 0xBBBB, fabricID, ipk), ver, 0x2001)

	runHandshakeAgainst(t, responder, ipk, fabricID, 0xAAAA)
	if got := responder.PeerCATs(); len(got) != 1 || got[0] != 0xABCD0001 {
		t.Fatalf("PeerCATs after first handshake = %v, want [ABCD0001]", got)
	}

	// Second peer on the same responder slot, its NOC carrying no CATs.
	ver.cats = nil
	runHandshakeAgainst(t, responder, ipk, fabricID, 0xCCCC)
	if got := responder.PeerCATs(); got != nil {
		t.Fatalf("PeerCATs after second handshake = %v, want nil — the previous peer's tags leaked", got)
	}
}

// TestResponderResetForNewSigma1_RenewsSessionID pins that a NEW Sigma1
// on an already-served responder takes a fresh local session id from
// the wired renewer. Reusing the id would make the second handshake
// register its session in the slot the first peer's session already
// occupies. Mirrors matter.js
// packages/protocol/src/session/case/CaseServer.ts:266, which calls
// getNextAvailableSessionId inside each Sigma1 handling rather than
// once per exchange.
func TestResponderResetForNewSigma1_RenewsSessionID(t *testing.T) {
	t.Parallel()
	ipk := fabricIPK()
	const fabricID = uint64(0x2A)
	var renewCalls int
	responder := NewResponder(newTestIdentity(t, 0xBBBB, fabricID, ipk), testVerifier{}, 0x2001)
	responder.SetSessionIDRenewer(func(previous uint16) (uint16, bool) {
		renewCalls++
		return previous + 0x10, true
	})

	first := runHandshakeAgainst(t, responder, ipk, fabricID, 0xAAAA)
	if first.ResponderSessionID != 0x2001 {
		t.Fatalf("first Sigma2.ResponderSessionID = %#x, want 0x2001", first.ResponderSessionID)
	}
	if renewCalls != 0 {
		t.Fatalf("renewer called %d times on the first Sigma1, want 0", renewCalls)
	}

	second := runHandshakeAgainst(t, responder, ipk, fabricID, 0xCCCC)
	if second.ResponderSessionID != 0x2011 {
		t.Fatalf("second Sigma2.ResponderSessionID = %#x, want 0x2011 — the id was not renewed", second.ResponderSessionID)
	}
	if got := responder.SessionID(); got != 0x2011 {
		t.Fatalf("SessionID() = %#x, want 0x2011", got)
	}
	if renewCalls != 1 {
		t.Fatalf("renewer called %d times, want 1", renewCalls)
	}
}

// TestResponderSigma1Replay_KeepsSessionID pins the idempotent-replay
// branch: Apple Home retransmits the SAME Sigma1 over MRP, and the
// cached Sigma2 must be replayed byte-for-byte — renewing the session
// id there would hand the initiator a Sigma2 it never accepts.
func TestResponderSigma1Replay_KeepsSessionID(t *testing.T) {
	t.Parallel()
	ipk := fabricIPK()
	const fabricID = uint64(0x2A)
	var renewCalls int
	responder := NewResponder(newTestIdentity(t, 0xBBBB, fabricID, ipk), testVerifier{}, 0x2001)
	responder.SetSessionIDRenewer(func(previous uint16) (uint16, bool) {
		renewCalls++
		return previous + 0x10, true
	})

	initiator := NewInitiator(newTestIdentity(t, 0xAAAA, fabricID, ipk), testVerifier{}, 0x1001, [32]byte{0x01})
	s1, err := initiator.GenerateSigma1()
	if err != nil {
		t.Fatalf("GenerateSigma1: %v", err)
	}
	if _, err := responder.ProcessSigma1(s1); err != nil {
		t.Fatalf("ProcessSigma1: %v", err)
	}
	replay, err := responder.ProcessSigma1(s1)
	if err != nil {
		t.Fatalf("ProcessSigma1 replay: %v", err)
	}
	if replay.ResponderSessionID != 0x2001 {
		t.Fatalf("replayed Sigma2.ResponderSessionID = %#x, want 0x2001", replay.ResponderSessionID)
	}
	if renewCalls != 0 {
		t.Fatalf("renewer called %d times on a Sigma1 replay, want 0", renewCalls)
	}
}
