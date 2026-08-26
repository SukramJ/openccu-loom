// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package sigma

import (
	"crypto/ecdh"
	"crypto/rand"
	"testing"
)

// buildSigma1ForFabric constructs a Sigma1 whose DestinationID correctly
// addresses the given fabric identity, and returns both the wire bytes and the
// ephemeral private key required to later process the Sigma2 reply.
//
// NewInitiator cannot be used directly here because it generates the random
// nonce inside GenerateSigma1 — the DestinationID depends on that nonce, so
// it can only be computed after the nonce is known.  This helper generates the
// nonce first, derives the DestinationID from it, then builds the Sigma1
// message manually so the stored sigma1Bytes in the Initiator are consistent
// with the wire bytes delivered to the Responder.
func buildSigma1ForFabric(t *testing.T, fabricID *Identity, rootPub []byte, sessionID uint16) (sigma1Bytes []byte, ephPriv *ecdh.PrivateKey, random [RandomSize]byte) {
	t.Helper()

	if _, err := rand.Read(random[:]); err != nil {
		t.Fatalf("rand: %v", err)
	}

	priv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("ecdh keygen: %v", err)
	}

	destID := ComputeDestinationID(fabricID.IPK, random, rootPub, fabricID.FabricID, fabricID.NodeID)

	msg := Sigma1{
		InitiatorRandom:    random,
		InitiatorSessionID: sessionID,
		DestinationID:      destID,
		InitiatorEphPubKey: priv.PublicKey().Bytes(),
	}
	return msg.Marshal(), priv, random
}

// TestResponder_MultiFabric_SequentialHandshakeIsolation verifies that a
// single Responder instance, when driven through three successive full Sigma
// handshakes targeting two distinct fabrics in the order A → B → A, selects
// the correct per-fabric identity for each exchange and derives matching
// session keys every time.
//
// The test guards against a class of bugs where the responder retains mutable
// identity state from a prior exchange instead of re-evaluating the resolver
// on each new Sigma1.  A responder with that defect would use the wrong IPK
// when deriving S2K for the second and third exchanges: the AES-CCM tag check
// in ProcessSigma2 (on the initiator side) or ProcessSigma3 (on the responder
// side) would fail with ErrUnauthenticated.
//
// Three independent initiators are used so each exchange carries distinct
// ephemeral keys, session IDs, and random nonces — preventing any accidental
// state sharing that would mask the regression.
func TestResponder_MultiFabric_SequentialHandshakeIsolation(t *testing.T) {
	t.Parallel()

	// Two distinct fabrics: independent IPKs, fabricIDs, root public keys.
	ipkA := fabricIPK()
	ipkB := fabricIPK()

	// Responder identities — one per fabric, same NodeID scheme as
	// other multi-fabric tests in this package.
	respIdentA := newTestIdentity(t, 0xAAAA_0001, 1, ipkA)
	respIdentB := newTestIdentity(t, 0xBBBB_0001, 2, ipkB)

	// Synthetic 65-byte root public keys distinguished by a label byte.
	rootPubA := make([]byte, 65)
	rootPubA[0] = 0x04
	rootPubA[1] = 0xA1

	rootPubB := make([]byte, 65)
	rootPubB[0] = 0x04
	rootPubB[1] = 0xB2

	verifier := testVerifier{}

	resolver := &multiFabricResolver{
		identities: []*Identity{respIdentA, respIdentB},
		verifier:   verifier,
		rootPub:    [][]byte{rootPubA, rootPubB},
	}

	// Responder seeded with respIdentA as its default identity; the
	// resolver overrides on each inbound Sigma1 so the default is
	// never the deciding factor in this test.
	responder := NewResponder(respIdentA, verifier, 0x1000)
	responder.SetIdentityResolver(resolver)

	// runHandshake drives one complete Sigma1 → Sigma2 → Sigma3 exchange
	// against the shared Responder, targeting the given fabric identity
	// and root public key.  It verifies that both parties hold identical
	// session keys at the end.
	runHandshake := func(t *testing.T, label string, fabricIdent *Identity, rootPub []byte) {
		t.Helper()

		// Build a Sigma1 whose DestinationID correctly addresses the fabric.
		// The nonce is generated inside buildSigma1ForFabric so the
		// DestinationID and the sigma1Bytes are always consistent.
		sigma1Bytes, initEphPriv, _ := buildSigma1ForFabric(t, fabricIdent, rootPub, 0x9000)

		sigma2, err := responder.ProcessSigma1(sigma1Bytes)
		if err != nil {
			t.Fatalf("%s: ProcessSigma1: %v", label, err)
		}

		// Replay the initiator side: parse our own Sigma1 to recover the
		// ephemeral pub key, then hand-drive ProcessSigma2 logic inline so
		// we stay consistent with the sigma1Bytes stored in Responder.
		//
		// Use a fresh Initiator with destID=[32]byte{} for Sigma2/Sigma3
		// processing only — we supply the already-generated sigma1Bytes
		// through a fresh NewInitiator + GenerateSigma1 pathway that
		// bakes in the correct DestinationID.

		destID := ComputeDestinationID(
			fabricIdent.IPK,
			// parse InitiatorRandom back out of sigma1Bytes so destID matches
			func() [RandomSize]byte {
				s, err := UnmarshalSigma1(sigma1Bytes)
				if err != nil {
					t.Fatalf("%s: re-parse sigma1: %v", label, err)
				}
				return s.InitiatorRandom
			}(),
			rootPub,
			fabricIdent.FabricID,
			fabricIdent.NodeID,
		)

		// Build an Initiator with the DestinationID already known.
		// We use a *thin* initiator constructed to hold the same private
		// ephemeral key and sigma1Bytes that were already sent to the
		// Responder — achieved by constructing the Initiator manually
		// using unexported fields via the internal test package.
		//
		// Because sigma package tests live in the same package (package sigma),
		// we can set unexported fields directly.
		initEphPub := initEphPriv.PublicKey().Bytes()
		s1, _ := UnmarshalSigma1(sigma1Bytes)
		init := &Initiator{
			identity:    fabricIdent,
			verifier:    verifier,
			sessionID:   0x9000,
			dest:        destID,
			ephPriv:     initEphPriv,
			ephPubBytes: initEphPub,
			sigma1Bytes: sigma1Bytes,
			random:      s1.InitiatorRandom,
			state:       initiatorStateSigma1Sent,
		}

		sigma3Bytes, err := init.ProcessSigma2(sigma2)
		if err != nil {
			t.Fatalf("%s: ProcessSigma2: %v", label, err)
		}

		if err := responder.ProcessSigma3(sigma3Bytes); err != nil {
			t.Fatalf("%s: ProcessSigma3: %v", label, err)
		}

		initKeys, ok := init.SessionKeys()
		if !ok {
			t.Fatalf("%s: initiator has no session keys after exchange", label)
		}
		respKeys, ok := responder.SessionKeys()
		if !ok {
			t.Fatalf("%s: responder has no session keys after exchange", label)
		}
		if !constantTimeKeysEqual(initKeys, respKeys) {
			t.Errorf("%s: session key mismatch — responder may have used wrong fabric identity", label)
		}
	}

	// Drive the sequence A → B → A.  If the responder leaks identity state
	// between exchanges, the wrong IPK enters S2K derivation and the
	// AES-CCM tag checks in ProcessSigma2 or ProcessSigma3 fail with
	// ErrUnauthenticated on the second or third handshake.
	runHandshake(t, "fabricA-first", respIdentA, rootPubA)
	runHandshake(t, "fabricB", respIdentB, rootPubB)
	runHandshake(t, "fabricA-again", respIdentA, rootPubA)
}
