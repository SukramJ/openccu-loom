// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package sigma

import (
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
)

// Sizes per Matter Core Spec §4.13.
const (
	// RandomSize is the length of the per-message random nonces (32B).
	RandomSize = 32
	// EphPubKeySize is the uncompressed P-256 public key length.
	EphPubKeySize = 65
	// SessionKeySize is the per-direction AES-CCM key length.
	SessionKeySize = 16
	// AttestationChallengeSize is the attestation-binding key length.
	AttestationChallengeSize = 16
	// FinalKeyMaterialSize bundles I2RKey || R2IKey || AttestationChallenge.
	FinalKeyMaterialSize = 3 * SessionKeySize
	// HKDFInfoSigma2 is the info string for the Sigma2 encryption key.
	HKDFInfoSigma2 = "Sigma2"
	// HKDFInfoSigma3 is the info string for the Sigma3 encryption key.
	HKDFInfoSigma3 = "Sigma3"
	// HKDFInfoSessionKeys is the info string for the final session keys.
	HKDFInfoSessionKeys = "SessionKeys"

	// Resumption KDF info strings per Matter §4.13.2.4 /
	// matter.js packages/protocol/src/session/case/CaseMessages.ts.

	// HKDFInfoSigma1Resume is the KDFSR1 info string used to derive the
	// peer-resume key for verifying initiatorResumeMIC in Sigma1.
	HKDFInfoSigma1Resume = "Sigma1_Resume"
	// HKDFInfoSigma2Resume is the KDFSR2 info string used to derive the
	// resume key for generating Sigma2ResumeMIC.
	HKDFInfoSigma2Resume = "Sigma2_Resume"
	// HKDFInfoSessionResumptionKeys is the info string used to derive
	// I2R/R2I/AttestationChallenge after a Sigma2_Resume (replaces
	// HKDFInfoSessionKeys for the resumption path).
	// matter.js packages/protocol/src/session/NodeSession.ts:SESSION_RESUMPTION_KEYS_INFO.
	HKDFInfoSessionResumptionKeys = "SessionResumptionKeys"

	// Resume AES-CCM nonces per Matter §4.13.2.4 /
	// matter.js CaseMessages.ts RESUME1_MIC_NONCE / RESUME2_MIC_NONCE.
	nonceResume1MIC = "NCASE_SigmaS1"
	nonceResume2MIC = "NCASE_SigmaS2"

	// ResumptionIDSize is the canonical 16-byte length of a resumption id.
	ResumptionIDSize = 16
)

// Errors.
var (
	// ErrInvalidPoint surfaces for malformed peer ephemeral public
	// keys (wrong length, off-curve, identity element).
	ErrInvalidPoint = errors.New("sigma: invalid ephemeral public key")
	// ErrSignatureInvalid is returned when the peer's NOC signature
	// over the transcript does not validate. This is the canonical
	// "wrong identity" / "tampered transcript" signal.
	ErrSignatureInvalid = errors.New("sigma: signature verification failed")
	// ErrUnauthenticated wraps an AES-CCM tag-verification failure on
	// encrypted2 or encrypted3. Indicates either a wrong key (peer
	// outside the fabric) or tampering.
	ErrUnauthenticated = errors.New("sigma: encrypted payload authentication failed")
	// ErrSessionState surfaces when methods are invoked out of order.
	ErrSessionState = errors.New("sigma: invalid session state")
	// ErrResumptionMICInvalid is returned when the initiatorResumeMIC in
	// Sigma1 fails AES-CCM verification against the KDFSR1 key derived
	// from the resumption record's shared secret. The responder MUST
	// fall back to Full Sigma per matter.js CaseServer.ts::#resume.
	ErrResumptionMICInvalid = errors.New("sigma: initiator resume MIC verification failed")
)

// SessionKeys is the final key material both sides derive after
// successful Sigma3.
type SessionKeys struct {
	// I2RKey encrypts traffic flowing initiator → responder.
	I2RKey [SessionKeySize]byte
	// R2IKey encrypts traffic flowing responder → initiator.
	R2IKey [SessionKeySize]byte
	// AttestationChallenge binds attestation requests to this session
	// (Matter §11.18.5).
	AttestationChallenge [AttestationChallengeSize]byte
}

// Identity bundles a node's operational certificate plus its private
// key. The NOC is the X.509-style certificate signed by a CA on the
// fabric; for the Sigma protocol layer we work with raw byte slices —
// validation happens against the [Fabric] root key on the peer side.
type Identity struct {
	// NOC is the node operational certificate (raw TLV per Matter
	// §11.18.4 in production; opaque bytes here).
	NOC []byte
	// ICAC is the optional Intermediate CA certificate. Empty means
	// the NOC is signed directly by the fabric root.
	ICAC []byte
	// PrivateKey is the P-256 private key matching the NOC public key.
	PrivateKey *ecdsa.PrivateKey
	// NodeID is the 64-bit operational node identifier.
	NodeID uint64
	// FabricID is the 64-bit fabric identifier.
	FabricID uint64
	// CompressedFabricID is the 8-byte derived fabric identifier
	// (see fabric.New).
	CompressedFabricID [8]byte
	// IPK is the 16-byte Operational Identity Protection Key for this
	// fabric, supplied by the commissioner in AddNOC.IPKValue. Per
	// Matter Core §4.13.2.5 the IPK is the leading prefix of every
	// CASE Sigma HKDF salt — without it the responder derives a
	// different `S2K` than the initiator and Apple Home rejects
	// Sigma2 with SecureChannel/INVALID_PARAMETER.
	IPK [16]byte
	// FabricIndex is the local 1..254 fabric table slot. Optional from
	// the sigma layer's point of view (signatures don't carry it), but
	// the daemon attaches it so [Responder.SessionFabricIndex] can
	// report which fabric the multi-fabric resolver landed on without
	// a second FabricID-to-index lookup.
	FabricIndex uint8
}

// PeerVerifier is the trust-anchor side: given the peer's NOC bytes
// the implementation MUST extract the embedded operational public
// key and verify the certificate chain back to the local fabric root.
//
// The Sigma protocol layer treats certificate parsing as opaque so a
// TLV-aware verifier from the cluster layer can plug in without
// changing the protocol package. For round-trip tests the [TestVerifier]
// type returns a known public key.
type PeerVerifier interface {
	// VerifyAndExtractPubKey validates noc + icac and returns the
	// operational public key embedded in noc. Errors translate into
	// [ErrSignatureInvalid] at the Sigma layer.
	VerifyAndExtractPubKey(noc, icac []byte) (*ecdsa.PublicKey, error)
}

// PeerNodeIDExtractor is an OPTIONAL interface a [PeerVerifier]
// implementation may also satisfy. When it does, the responder calls
// it after Sigma3 verification to lift the peer's NodeID out of the
// NOC subject — that value feeds the AES-CCM nonce on the outbound
// secure channel (Matter §4.5.1.4 nonce = securityFlags || counter
// || sourceNodeID; the peer's encrypts use ITS OWN NodeID, so we
// need it to verify their tags). Verifiers that don't implement
// this surface (e.g. test fixtures) leave [Responder.PeerNodeID]
// at zero — fine for round-trip tests, broken for real CASE.
type PeerNodeIDExtractor interface {
	PeerNodeIDFromNOC(noc []byte) (uint64, error)
}

// PeerCATsExtractor is an OPTIONAL interface a [PeerVerifier]
// implementation may also satisfy. When it does, the responder calls
// it after Sigma3 verification to lift the peer's CASE Authenticated
// Tags out of the NOC subject. The CATs feed the IM dispatcher's
// per-subject ACL match (Matter §9.10.5.6 + chip
// src/access/AccessControl.cpp:481). Verifiers that don't implement
// this surface leave [Responder.PeerCATs] empty — the ACL gate then
// only matches operational-node-id subjects, which is sufficient for
// single-admin fabrics.
type PeerCATsExtractor interface {
	PeerCATsFromNOC(noc []byte) ([]uint32, error)
}

// IdentityResolver picks the per-fabric operational identity that a
// Sigma1 datagram is addressed to. Apple Home's Multi-Admin pairing
// installs two fabrics in quick succession (primary Hub then iCloud
// system commissioner); after pair the iPhone reconnects under
// fabric #1 while the iCloud Hub uses fabric #2, both targeting the
// same bridge over the same UDP listener. Sigma1 carries a
// `DestinationID = HMAC-SHA256(opIPK, random || rootPubKey ||
// fabricID || nodeID)` (Matter Core §4.13.2.4.2) that uniquely
// identifies which fabric the initiator means to address. A
// single-identity Responder that signs Sigma2 with whichever fabric
// happened to be installed last produces a NOC the commissioner
// cannot verify — Apple responds with StatusReport(SecureChannel,
// INVALID_PARAMETER) and the post-pair ongoing CASE never lands,
// surfacing as an unsupported-device error in Home.
//
// Implementations iterate every installed fabric, compute the
// candidate destinationID, and return the matching (Identity,
// PeerVerifier) tuple. Returning (_, _, false) makes the responder
// fall through to its baseline identity (single-fabric / test path).
// Mirrors matter.js packages/protocol/src/fabric/FabricManager.ts:
// `findFabricFromDestinationId`.
type IdentityResolver interface {
	ResolveSigma1Destination(destinationID [32]byte, initiatorRandom [RandomSize]byte) (*Identity, PeerVerifier, bool)
}

// --- Sigma1 ---

// Sigma1 is the first message: plaintext on the wire.
type Sigma1 struct {
	InitiatorRandom    [RandomSize]byte
	InitiatorSessionID uint16
	DestinationID      [32]byte // HMAC-SHA256 truncated/full over fabric+nodeID
	InitiatorEphPubKey []byte   // 65-byte uncompressed P-256
	// ResumptionID is the optional 16-byte resumption id from a prior CASE
	// session (Matter §4.14.2.3 tag 6). Nil if not present.
	ResumptionID []byte
	// InitiatorResumeMIC is the optional 16-byte AES-CCM tag proving the
	// initiator holds the prior shared secret (Matter §4.14.2.3 tag 7).
	// Nil if not present.
	InitiatorResumeMIC []byte
}

// Marshal serialises Sigma1 as a Matter TLV anonymous structure per
// Matter Core Specification §4.14.2.3:
//
//	[0] anonymous structure {
//	    [1] octets-32  initiator random
//	    [2] uint16     initiator session id
//	    [3] octets-32  destination id (HMAC-SHA256)
//	    [4] octets-65  initiator ephemeral pub key (P-256 uncompressed)
//	    [5] struct?    initiator session params (omitted)
//	    [6] octets-16? resumption id (omitted)
//	    [7] octets-16? initiator resume mic (omitted)
//	}
//
// Apple Home, Google Home, and chip-tool all send the TLV form; the
// previous deterministic raw-concat format made the responder reject
// every real-world Sigma1 with `bad sigma1 length=174`.
func (s Sigma1) Marshal() []byte {
	enc := sigmaTLVEncoder()
	enc.startStruct()
	enc.putOctets(1, s.InitiatorRandom[:])
	enc.putUint(2, uint64(s.InitiatorSessionID))
	enc.putOctets(3, s.DestinationID[:])
	enc.putOctets(4, s.InitiatorEphPubKey)
	// Optional resumption fields (tags 6 + 7) — emitted asymmetrically
	// only when set. The decoder enforces the all-or-nothing pairing
	// rule (chip CASESession.cpp:2438-2449); tests rely on this
	// permissive encoder to construct malformed inputs.
	if len(s.ResumptionID) > 0 {
		enc.putOctets(6, s.ResumptionID)
	}
	if len(s.InitiatorResumeMIC) > 0 {
		enc.putOctets(7, s.InitiatorResumeMIC)
	}
	enc.endContainer()
	return enc.bytes()
}

// UnmarshalSigma1 parses a Matter TLV-encoded Sigma1. Mandatory fields
// 1-4 are decoded; optional fields 5 (sessionParams container), 6
// (resumptionId) and 7 (resumeMic) are decoded into the matching struct
// fields — the responder uses them to attempt the Sigma2_Resume fast
// path (Matter §4.14.2.3 / §4.13.2.4).
// Mirrors matter.js packages/protocol/src/session/case/CaseMessages.ts::TlvCaseSigma1.
func UnmarshalSigma1(b []byte) (Sigma1, error) {
	var s Sigma1
	dec := sigmaTLVDecoder(b)
	if err := dec.openStruct(); err != nil {
		return s, fmt.Errorf("%w: %w / first16=%x", ErrSessionState, err, peek(b, 16))
	}
	for {
		tag, val, end, err := dec.next()
		if err != nil {
			return s, fmt.Errorf("%w: %w", ErrSessionState, err)
		}
		if end {
			break
		}
		switch tag {
		case 1:
			if len(val.octets) != RandomSize {
				return s, fmt.Errorf("%w: random length=%d (type=%d) / first32=%x", ErrSessionState, len(val.octets), val.elemType, peek(b, 32))
			}
			copy(s.InitiatorRandom[:], val.octets)
		case 2:
			//nolint:gosec // SessionID is uint16 by spec.
			s.InitiatorSessionID = uint16(val.u)
		case 3:
			if len(val.octets) != 32 {
				return s, fmt.Errorf("%w: destinationID length=%d", ErrSessionState, len(val.octets))
			}
			copy(s.DestinationID[:], val.octets)
		case 4:
			if len(val.octets) != EphPubKeySize {
				return s, fmt.Errorf("%w: ephPub length=%d", ErrSessionState, len(val.octets))
			}
			s.InitiatorEphPubKey = append([]byte(nil), val.octets...)
		case 5:
			// initiatorSessionParams container — drain without surfacing
			// (session-timing negotiation is handled separately).
			if val.container {
				if err := dec.skipContainer(); err != nil {
					return s, fmt.Errorf("%w: skip sessionParams (tag=5): %w", ErrSessionState, err)
				}
			}
		case 6:
			// resumptionId — 16 bytes; present only on a resume attempt.
			if len(val.octets) != 16 {
				return s, fmt.Errorf("%w: resumptionId length=%d (want 16)", ErrSessionState, len(val.octets))
			}
			s.ResumptionID = append([]byte(nil), val.octets...)
		case 7:
			// initiatorResumeMic — 16 bytes; present only on a resume attempt.
			if len(val.octets) != 16 {
				return s, fmt.Errorf("%w: initiatorResumeMic length=%d (want 16)", ErrSessionState, len(val.octets))
			}
			s.InitiatorResumeMIC = append([]byte(nil), val.octets...)
		default:
			// Unknown future optional field; drain containers to keep cursor aligned.
			if val.container {
				if err := dec.skipContainer(); err != nil {
					return s, fmt.Errorf("%w: skip optional field tag=%d: %w", ErrSessionState, tag, err)
				}
			}
		}
	}
	if s.InitiatorEphPubKey == nil {
		return s, fmt.Errorf("%w: sigma1 missing ephPub", ErrSessionState)
	}
	// Resumption-tag all-or-nothing rule (chip CASESession.cpp:2438-2449
	// `CHIP_ERROR_UNEXPECTED_TLV_ELEMENT`). The Matter spec §4.13.2.2
	// pairs tag 6 (resumptionId) and tag 7 (initiatorResumeMic) — a
	// commissioner that sends one without the other is non-spec and
	// chip's strict parser rejects the entire Sigma1. Mirror the
	// rejection so a malformed commissioner surfaces the same error
	// as chip rather than silently downgrading to a fresh CASE.
	if (len(s.ResumptionID) > 0) != (len(s.InitiatorResumeMIC) > 0) {
		return s, fmt.Errorf("%w: sigma1 resumption-tags must be all-or-nothing (resumptionId=%d bytes, initiatorResumeMic=%d bytes)",
			ErrSessionState, len(s.ResumptionID), len(s.InitiatorResumeMIC))
	}
	return s, nil
}

func peek(b []byte, n int) []byte {
	if len(b) <= n {
		return b
	}
	return b[:n]
}

// Per-element Sigma1 TLV iteration debug logging was removed here.
// The calls (sigma1.start, sigma1.iter.end, sigma1.iter) were
// flagged for removal once the parser was stable; they produced
// verbose output at every CASE handshake even at the default INFO
// log level. The slog import and slogDefaultDebug indirection have
// been dropped along with the helper.

// --- Sigma2 ---

// SessionParameters carries the responder's MRP tuning hints emitted
// inside Sigma2 (and Sigma2_Resume) as context-tag 5. Mirrors matter.js
// `packages/protocol/src/session/SessionParameters.ts` and chip
// `src/protocols/secure_channel/Constants.h` `SessionParameters`. The
// initiator merges these into its post-CASE retransmit budget.
//
// All three numeric fields are *optional* per matter.js
// `TlvSessionParameters`. When a zero value is passed, the field is
// omitted (the peer falls back to the spec default for that field —
// 500 ms / 300 ms / 4000 ms).
//
// Width is fixed by the spec — matter.js's `TlvSessionParameters`
// (packages/protocol/src/session/pase/PaseMessages.ts:23-50) and
// chip's CASESession.cpp::EncodeSigma2 SessionParameters fields:
//
//	[1] uint32  SessionIdleInterval
//	[2] uint32  SessionActiveInterval
//	[3] uint16  SessionActiveThreshold
//	[4] uint16  DataModelRevision
//	[5] uint16  InteractionModelRevision
//	[6] uint32  SpecificationVersion
//	[7] uint16  MaxPathsPerInvoke
//
// All fields are optional on the wire — a zero value omits the
// corresponding tag so the peer falls back to its own defaults.
type SessionParameters struct {
	SessionIdleInterval      uint32 // [1] milliseconds; 0 = omit
	SessionActiveInterval    uint32 // [2] milliseconds; 0 = omit
	SessionActiveThreshold   uint16 // [3] milliseconds; 0 = omit
	DataModelRevision        uint16 // [4] Matter Data Model revision; 0 = omit
	InteractionModelRevision uint16 // [5] Matter Interaction Model revision; 0 = omit
	SpecificationVersion     uint32 // [6] Matter specification version (e.g. 0x01040000); 0 = omit
	MaxPathsPerInvoke        uint16 // [7] maximum paths per InvokeRequests list; 0 = omit
}

// isEmpty reports whether the SessionParameters carries no hint at
// all. An all-zero SessionParameters is omitted on the wire so the
// responder behaves as if no struct was supplied.
func (sp SessionParameters) isEmpty() bool {
	return sp.SessionIdleInterval == 0 &&
		sp.SessionActiveInterval == 0 &&
		sp.SessionActiveThreshold == 0 &&
		sp.DataModelRevision == 0 &&
		sp.InteractionModelRevision == 0 &&
		sp.SpecificationVersion == 0 &&
		sp.MaxPathsPerInvoke == 0
}

// encode writes the SessionParameters fields into `enc` as a
// context-tag-`outerTag` sub-struct. The outer tag is the parent's
// chosen number (Sigma2 uses 5; Sigma2_Resume uses 4 — see Matter
// §4.14.2.3 / §4.14.2.4).
func (sp SessionParameters) encode(enc *sigmaEncoder, outerTag uint8) {
	enc.startStructTag(outerTag)
	if sp.SessionIdleInterval != 0 {
		enc.putUint32(1, sp.SessionIdleInterval)
	}
	if sp.SessionActiveInterval != 0 {
		enc.putUint32(2, sp.SessionActiveInterval)
	}
	if sp.SessionActiveThreshold != 0 {
		enc.putUint16(3, sp.SessionActiveThreshold)
	}
	if sp.DataModelRevision != 0 {
		enc.putUint16(4, sp.DataModelRevision)
	}
	if sp.InteractionModelRevision != 0 {
		enc.putUint16(5, sp.InteractionModelRevision)
	}
	if sp.SpecificationVersion != 0 {
		enc.putUint32(6, sp.SpecificationVersion)
	}
	if sp.MaxPathsPerInvoke != 0 {
		enc.putUint16(7, sp.MaxPathsPerInvoke)
	}
	enc.endContainer()
}

// Sigma2 is the second message. ResponderRandom + ResponderSessionId
// + ResponderEphPubKey ride in the clear; the rest is wrapped in
// Encrypted2 (AES-CCM under S2K).
type Sigma2 struct {
	ResponderRandom    [RandomSize]byte
	ResponderSessionID uint16
	ResponderEphPubKey []byte // 65-byte uncompressed P-256
	Encrypted2         []byte // AES-CCM ciphertext + 16-byte tag
	// SessionParams, when non-nil and non-empty, emits Sigma2's
	// optional context-tag-5 responderSessionParams sub-struct. The
	// initiator learns the bridge's preferred MRP intervals and tunes
	// its post-CASE retransmit budget; without the hint a commissioner
	// defaults to spec values (500/300/4000 ms) which can be wrong for
	// a bridge that wants longer idle intervals to coalesce upstream
	// poll bursts.
	SessionParams *SessionParameters
}

// Marshal serialises Sigma2 as a Matter TLV anonymous structure per
// Matter Core Spec §4.14.2.3:
//
//	[0] anonymous structure {
//	    [1] octets-32  responder random
//	    [2] uint16     responder session id
//	    [3] octets-65  responder ephemeral pub key
//	    [4] octets     encrypted2 (AES-CCM ciphertext || 16-byte tag)
//	    [5] struct?    responder session params (OPTIONAL)
//	}
//
// Sigma2 includes `responderSessionParams` whenever the responder wants
// the initiator to tune its MRP retransmit budget. Mirrors matter.js
// CaseServer.ts:258-264 (`sendSigma2({ ..., responderSessionParams:
// this.#sessions.sessionParameters })`) and chip
// CASESession.cpp:1282,1326 (kResponderSessionParams = 5). The empty
// case (SessionParams==nil OR SessionParams.isEmpty()) still omits
// the tag entirely so the wire is byte-identical to the pre-fix
// shape — only callers that supply non-empty hints emit the new field.
func (s Sigma2) Marshal() []byte {
	enc := sigmaTLVEncoder()
	enc.startStruct()
	enc.putOctets(1, s.ResponderRandom[:])
	enc.putUint16(2, s.ResponderSessionID)
	enc.putOctets(3, s.ResponderEphPubKey)
	enc.putOctets(4, s.Encrypted2)
	if s.SessionParams != nil && !s.SessionParams.isEmpty() {
		s.SessionParams.encode(enc, 5)
	}
	enc.endContainer()
	return enc.bytes()
}

// TBE2Plaintext is the cleartext form of the Sigma2 encrypted payload.
type TBE2Plaintext struct {
	ResponderNOC  []byte
	ResponderICAC []byte
	Signature     []byte // ECDSA-P256-SHA256 over (initiatorEphPubKey || responderEphPubKey)
	ResumptionID  []byte
}

// marshalTBE2 produces the cleartext bytes the AES-CCM-Sigma2 layer
// encrypts. Format mirrors matter.js TlvEncryptedDataSigma2 (Matter
// §4.14.2.3): anonymous TLV structure with the responder NOC, optional
// ICAC, the operational signature, and a 16-byte resumption id.
func marshalTBE2(p TBE2Plaintext) []byte {
	enc := sigmaTLVEncoder()
	enc.startStruct()
	enc.putOctets(1, p.ResponderNOC)
	if len(p.ResponderICAC) > 0 {
		enc.putOctets(2, p.ResponderICAC)
	}
	enc.putOctets(3, p.Signature)
	enc.putOctets(4, p.ResumptionID)
	enc.endContainer()
	return enc.bytes()
}

// unmarshalTBE2 is the inverse of [marshalTBE2].
func unmarshalTBE2(b []byte) (TBE2Plaintext, error) {
	var p TBE2Plaintext
	dec := sigmaTLVDecoder(b)
	if err := dec.openStruct(); err != nil {
		return p, fmt.Errorf("tbe2 open struct: %w", err)
	}
	for {
		tag, val, end, err := dec.next()
		if err != nil {
			return p, fmt.Errorf("tbe2 next: %w", err)
		}
		if end {
			break
		}
		switch tag {
		case 1:
			p.ResponderNOC = append([]byte(nil), val.octets...)
		case 2:
			p.ResponderICAC = append([]byte(nil), val.octets...)
		case 3:
			p.Signature = append([]byte(nil), val.octets...)
		case 4:
			p.ResumptionID = append([]byte(nil), val.octets...)
		default:
			if val.container {
				if err := dec.skipContainer(); err != nil {
					return p, fmt.Errorf("tbe2 skip tag=%d: %w", tag, err)
				}
			}
		}
	}
	return p, nil
}

// --- Sigma3 ---

// Sigma3 is the third message: a single AES-CCM-wrapped TBE3 payload
// wrapped as a Matter TLV anonymous structure per Matter §4.14.2.3:
//
//	[0] anonymous structure {
//	    [1] octets  encrypted3 (AES-CCM ciphertext || 16-byte tag)
//	}
type Sigma3 struct {
	Encrypted3 []byte
}

// Marshal returns Sigma3 wire bytes (TLV-encoded).
func (s Sigma3) Marshal() []byte {
	enc := sigmaTLVEncoder()
	enc.startStruct()
	enc.putOctets(1, s.Encrypted3)
	enc.endContainer()
	return enc.bytes()
}

// UnmarshalSigma3 extracts the encrypted3 octets from Apple's
// TLV-wrapped Sigma3 payload.
func UnmarshalSigma3(b []byte) (Sigma3, error) {
	var s Sigma3
	dec := sigmaTLVDecoder(b)
	if err := dec.openStruct(); err != nil {
		return s, fmt.Errorf("%w: sigma3 open struct: %w", ErrSessionState, err)
	}
	for {
		tag, val, end, err := dec.next()
		if err != nil {
			return s, fmt.Errorf("%w: sigma3 next: %w", ErrSessionState, err)
		}
		if end {
			break
		}
		switch tag {
		case 1:
			s.Encrypted3 = append([]byte(nil), val.octets...)
		default:
			if val.container {
				if err := dec.skipContainer(); err != nil {
					return s, fmt.Errorf("%w: sigma3 skip tag=%d: %w", ErrSessionState, tag, err)
				}
			}
		}
	}
	if len(s.Encrypted3) == 0 {
		return s, fmt.Errorf("%w: sigma3 missing encrypted3", ErrSessionState)
	}
	return s, nil
}

// TBE3Plaintext is the cleartext form of the Sigma3 encrypted payload.
type TBE3Plaintext struct {
	InitiatorNOC  []byte
	InitiatorICAC []byte
	Signature     []byte // ECDSA-P256-SHA256 over (initiatorEphPubKey || responderEphPubKey)
}

// marshalTBE3 produces the cleartext bytes the AES-CCM-Sigma3 layer
// encrypts. Format mirrors matter.js TlvEncryptedDataSigma3 (Matter
// §4.14.2.3): anonymous TLV with the initiator NOC, optional ICAC,
// and the operational signature.
func marshalTBE3(p TBE3Plaintext) []byte {
	enc := sigmaTLVEncoder()
	enc.startStruct()
	enc.putOctets(1, p.InitiatorNOC)
	if len(p.InitiatorICAC) > 0 {
		enc.putOctets(2, p.InitiatorICAC)
	}
	enc.putOctets(3, p.Signature)
	enc.endContainer()
	return enc.bytes()
}

func unmarshalTBE3(b []byte) (TBE3Plaintext, error) {
	var p TBE3Plaintext
	dec := sigmaTLVDecoder(b)
	if err := dec.openStruct(); err != nil {
		return p, fmt.Errorf("tbe3 open struct: %w", err)
	}
	for {
		tag, val, end, err := dec.next()
		if err != nil {
			return p, fmt.Errorf("tbe3 next: %w", err)
		}
		if end {
			break
		}
		switch tag {
		case 1:
			p.InitiatorNOC = append([]byte(nil), val.octets...)
		case 2:
			p.InitiatorICAC = append([]byte(nil), val.octets...)
		case 3:
			p.Signature = append([]byte(nil), val.octets...)
		default:
			if val.container {
				if err := dec.skipContainer(); err != nil {
					return p, fmt.Errorf("tbe3 skip tag=%d: %w", tag, err)
				}
			}
		}
	}
	return p, nil
}

// --- Sigma2Resume ---

// Sigma2Resume is the server's one-round-trip response when Sigma1
// carries a valid resumptionId + initiatorResumeMic pair. Sending this
// message establishes the session immediately — no Sigma3 is expected.
//
// Mirrors matter.js packages/protocol/src/session/case/CaseMessages.ts
// ::TlvCaseSigma2Resume (Matter §4.14.2.3).
type Sigma2Resume struct {
	// ResumptionID is the bridge's fresh 16-byte resumption id for the
	// next resumption. Replaces the prior id stored in the peer's record.
	ResumptionID []byte
	// Sigma2ResumeMIC is the 16-byte AES-CCM tag proving the bridge
	// holds the same shared secret as the initiator (KDFSR2 path).
	Sigma2ResumeMIC []byte
	// ResponderSessionID is the bridge's newly-allocated session id.
	ResponderSessionID uint16
	// SessionParams, when non-nil and non-empty, emits the optional
	// context-tag-4 responderSessionParams sub-struct so a resuming
	// peer learns the bridge's preferred MRP intervals.
	//
	// Mirrors matter.js packages/protocol/src/session/case/CaseServer.ts:186-191
	// (`sendSigma2Resume({ ..., responderSessionParams: this.#sessions.sessionParameters })`
	// Sigma2ResumeTags tag 4 = kResponderSessionParams) and chip
	// CASESession.cpp:1113,1144 (`outSigma2ResData.responderMrpConfig`).
	SessionParams *SessionParameters
}

// MarshalSigma2Resume encodes the Sigma2Resume reply as a Matter TLV
// anonymous structure per Matter §4.14.2.3:
//
//	[0] anonymous structure {
//	    [1] octets-16  resumptionId
//	    [2] octets-16  sigma2ResumeMic
//	    [3] uint16     responderSessionId
//	    [4] struct?    responderSessionParams (OPTIONAL, emitted when non-nil)
//	}
//
// Sigma2Resume includes `responderSessionParams` (tag 4) whenever the
// responder wants the resuming peer to tune its MRP retransmit budget.
// Mirrors matter.js packages/protocol/src/session/case/CaseServer.ts:186-191
// (`sendSigma2Resume({ ..., responderSessionParams: this.#sessions.sessionParameters })`)
// and chip CASESession.cpp:1113,1144. The empty case (SessionParams==nil
// OR SessionParams.isEmpty()) still omits the tag entirely so the wire
// is byte-identical to the pre-fix shape.
func MarshalSigma2Resume(s Sigma2Resume) []byte {
	enc := sigmaTLVEncoder()
	enc.startStruct()
	enc.putOctets(1, s.ResumptionID)
	enc.putOctets(2, s.Sigma2ResumeMIC)
	enc.putUint16(3, s.ResponderSessionID)
	if s.SessionParams != nil && !s.SessionParams.isEmpty() {
		s.SessionParams.encode(enc, 4)
	}
	enc.endContainer()
	return enc.bytes()
}

// --- Helpers ---

// ComputeDestinationID derives the 32-byte Sigma1.DestinationID a
// commissioner stamps into Sigma1 when addressing a specific operational
// fabric on a multi-fabric node. Matter Core §4.13.2.4.2:
//
//	destinationMessage = random || rootPublicKey || fabricID || nodeID
//	                       (8-byte LE)   (8-byte LE)
//	destinationID      = HMAC-SHA256(opIPK, destinationMessage)
//
// `opIPK` is the per-fabric operational IPK (HKDF of the raw IPK with
// salt=compressedFabricID, info="GroupKey v1.0" — already derived by
// the daemon and stored as `Identity.IPK`). `rootPublicKey` is the
// 65-byte uncompressed P-256 fabric root key (`0x04 || X || Y`).
//
// Used by [IdentityResolver] implementations to match an inbound
// Sigma1.DestinationID against every installed fabric. Mirrors
// matter.js `Fabric.ts::destinationIdsFor` / `#generateSalt`.
func ComputeDestinationID(opIPK [16]byte, initiatorRandom [RandomSize]byte, rootPublicKey []byte, fabricID, nodeID uint64) [32]byte {
	var (
		fabricBytes [8]byte
		nodeBytes   [8]byte
	)
	binary.LittleEndian.PutUint64(fabricBytes[:], fabricID)
	binary.LittleEndian.PutUint64(nodeBytes[:], nodeID)

	mac := hmac.New(sha256.New, opIPK[:])
	mac.Write(initiatorRandom[:])
	mac.Write(rootPublicKey)
	mac.Write(fabricBytes[:])
	mac.Write(nodeBytes[:])

	var out [32]byte
	copy(out[:], mac.Sum(nil))
	return out
}

// signedDataBytes builds the TLV-encoded `signed-data` payload that
// every CASE Sigma signature is computed over (Matter §4.14.2.3
// `signed_data`). Layout:
//
//	[0] anonymous structure {
//	    [1] octets  signer NOC
//	    [2] octets? signer ICAC (if present)
//	    [3] octets  signer ephemeral pub key
//	    [4] octets  peer ephemeral pub key
//	}
//
// `selfEph` / `peerEph` carry the ephemeral keys in the order each
// peer sees them: for Sigma2 the responder signs (respEph, initEph);
// for Sigma3 the initiator signs (initEph, respEph).
func signedDataBytes(noc, icac, selfEph, peerEph []byte) []byte {
	enc := sigmaTLVEncoder()
	enc.startStruct()
	enc.putOctets(1, noc)
	if len(icac) > 0 {
		enc.putOctets(2, icac)
	}
	enc.putOctets(3, selfEph)
	enc.putOctets(4, peerEph)
	enc.endContainer()
	return enc.bytes()
}

// signTranscript signs the TLV `signed-data` derived from the
// signer's NOC + ICAC + ephemeral keys. ECDSA-P256-SHA256; output is
// the 64-byte concatenation of 32-byte BE r || 32-byte BE s expected
// by chip-tool / matter.js for downstream parsing.
func signTranscript(priv *ecdsa.PrivateKey, noc, icac, selfEph, peerEph []byte) ([]byte, error) {
	digest := sha256.Sum256(signedDataBytes(noc, icac, selfEph, peerEph))
	r, s, err := ecdsa.Sign(rand.Reader, priv, digest[:])
	if err != nil {
		return nil, fmt.Errorf("sigma: ecdsa.Sign: %w", err)
	}
	return packECDSASig(r, s), nil
}

// verifyTranscript verifies a signature produced by [signTranscript].
// The verifier reconstructs the `signed-data` layout from the peer's
// NOC + ICAC + ephemerals, hashes, and validates against the supplied
// public key.
func verifyTranscript(pub *ecdsa.PublicKey, sig, noc, icac, selfEph, peerEph []byte) error {
	r, s, err := unpackECDSASig(sig)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(signedDataBytes(noc, icac, selfEph, peerEph))
	if !ecdsa.Verify(pub, digest[:], r, s) {
		return ErrSignatureInvalid
	}
	return nil
}

// packECDSASig serialises (r, s) as 32-byte BE r || 32-byte BE s.
func packECDSASig(r, s *big.Int) []byte {
	out := make([]byte, 64)
	rb := r.Bytes()
	sb := s.Bytes()
	copy(out[32-len(rb):32], rb)
	copy(out[64-len(sb):64], sb)
	return out
}

// unpackECDSASig is the inverse of [packECDSASig].
func unpackECDSASig(sig []byte) (r, s *big.Int, err error) {
	if len(sig) != 64 {
		return nil, nil, fmt.Errorf("%w: signature length=%d, want 64", ErrSignatureInvalid, len(sig))
	}
	return new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:]), nil
}

// validatePoint decodes b as an uncompressed P-256 point and rejects
// the identity / off-curve cases. Returns only the error path —
// callers that need the decoded point use [crypto/ecdh.P256.NewPublicKey]
// directly because the validation here mirrors what NewPublicKey does
// internally; the standalone check exists so we can fail fast with
// our typed [ErrInvalidPoint] before falling into the ecdh layer.
func validatePoint(b []byte) error {
	if len(b) != EphPubKeySize || b[0] != 0x04 {
		return fmt.Errorf("%w: bad encoding", ErrInvalidPoint)
	}
	x, y := elliptic.Unmarshal(elliptic.P256(), b) //nolint:staticcheck // SA1019: required for raw point decode
	if x == nil || y == nil {
		return fmt.Errorf("%w: not on curve", ErrInvalidPoint)
	}
	if x.Sign() == 0 && y.Sign() == 0 {
		return fmt.Errorf("%w: identity element", ErrInvalidPoint)
	}
	return nil
}

// ecdhSharedSecret computes the ECDH shared secret between a local
// private key and a peer public key. Returns the 32-byte X coordinate
// per Matter §4.13.2.3.
func ecdhSharedSecret(priv *ecdh.PrivateKey, peerPub *ecdh.PublicKey) ([]byte, error) {
	secret, err := priv.ECDH(peerPub)
	if err != nil {
		return nil, fmt.Errorf("sigma: ecdh: %w", err)
	}
	return secret, nil
}

// hkdfDerive runs HKDF-Extract-then-Expand and returns L bytes.
func hkdfDerive(ikm, salt []byte, info string, l int) ([]byte, error) {
	out, err := hkdf.Key(sha256.New, ikm, salt, info, l)
	if err != nil {
		return nil, fmt.Errorf("sigma: hkdf: %w", err)
	}
	return out, nil
}

// constantTimeEqual is a small wrapper for clarity at call sites.
func constantTimeEqual(a, b []byte) bool {
	return subtle.ConstantTimeCompare(a, b) == 1
}
