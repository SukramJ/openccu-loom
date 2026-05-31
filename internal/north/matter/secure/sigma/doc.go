// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package sigma implements Matter CASE (Certificate-Authenticated
// Session Establishment) per Matter Core Specification §4.13.
//
// CASE is the post-commissioning operational session protocol: two
// nodes that already share a fabric — i.e., both have a Node
// Operational Certificate (NOC) signed by a common root CA — derive
// a fresh session key without involving a passcode. The protocol
// is a three-message handshake known as Sigma1 / Sigma2 / Sigma3:
//
//	Sigma1 (initiator → responder):
//	    initiatorRandom (32B), initiatorSessionId,
//	    destinationId (HMAC over peer's compressed-fabric+nodeID),
//	    initiatorEphPubKey (P-256 uncompressed)
//
//	Sigma2 (responder → initiator):
//	    responderRandom (32B), responderSessionId,
//	    responderEphPubKey,
//	    encrypted2 := AES-CCM(S2K, nonce_R2, plaintext={
//	        responderNOC, responderICAC, signature, resumptionID
//	    }, aad=transcript)
//
//	Sigma3 (initiator → responder):
//	    encrypted3 := AES-CCM(S3K, nonce_I3, plaintext={
//	        initiatorNOC, initiatorICAC, signature
//	    }, aad=transcript)
//
// Key schedule (Matter §4.13.2.3):
//
//	sharedSecret = ECDH(initiatorEph, responderEph)
//	transcriptHash_1   = SHA-256(Sigma1)
//	S2K = HKDF(sharedSecret, salt=transcriptHash_1, info="Sigma2", L=16)
//	transcriptHash_2   = SHA-256(Sigma1 || Sigma2_partial)
//	S3K = HKDF(sharedSecret, salt=transcriptHash_2, info="Sigma3", L=16)
//	transcriptHash_3   = SHA-256(Sigma1 || Sigma2 || Sigma3_partial)
//	(I2RKey || R2IKey || AttestationChallenge) = HKDF(
//	    sharedSecret, salt=transcriptHash_3, info="SessionKeys", L=48)
//
// The signature inside encrypted2 / encrypted3 binds the operational
// identity (NOC) to the freshly-generated ephemeral keys and is
// verified by the peer using the NOC's public key.
//
// This package implements the protocol layer plus key derivation;
// AES-CCM frame encryption is delegated to [..]/secure/aesccm and the
// transcript-binding uses SHA-256 + HKDF from stdlib.
package sigma
