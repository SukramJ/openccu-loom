// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package sigma

import (
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"

	"github.com/SukramJ/openccu-loom/internal/north/matter/secure/aesccm"
)

// ErrFabricIDMismatch is returned by [Responder.ProcessSigma3] when the
// peer's operational certificate is scoped to a different fabric than
// the one the responder selected for this exchange — its NOC subject
// fabric-id does not equal [Identity.FabricID]. Mirrors matter.js
// packages/protocol/src/session/case/CaseServer.ts:304-306
// (`if (fabric.fabricId !== peerFabricId) throw new UnexpectedDataError`).
var ErrFabricIDMismatch = errors.New("sigma: peer NOC fabric-id does not match responder fabric")

// PeerFabricIDExtractor is an OPTIONAL interface a [PeerVerifier]
// implementation may also satisfy. When it does, [Responder.ProcessSigma3]
// calls it after NOC-chain verification to lift the peer NOC subject's
// fabric-id and reject the handshake when it does not match the
// responder-selected fabric's [Identity.FabricID].
//
// The chain verification in [PeerVerifier.VerifyAndExtractPubKey] only
// proves the peer NOC links back to the fabric root; it does NOT bind
// the NOC subject fabric-id, which Matter §6.5.6.1 carries as a
// distinct subject DN attribute. Two fabrics provisioned from one
// trust root would otherwise accept each other's operational
// certificates. matter.js validates the two separately and rejects a
// fabric-id mismatch before the transcript signature check.
//
// The daemon's production verifier implements this surface; only
// protocol-layer test rigs that hand the responder a certificate-less
// verifier leave it unimplemented, and those skip the check. A
// verifier that DOES implement it but fails to read the fabric-id
// rejects the handshake — matter.js has no "unavailable" path here.
// Mirrors matter.js packages/protocol/src/session/case/CaseServer.ts:299-306.
type PeerFabricIDExtractor interface {
	PeerFabricIDFromNOC(noc []byte) (uint64, error)
}

// sigma2Salt builds the per-spec HKDF salt for the S2K key:
// `IPK || responderRandom || responderEphPubKey || SHA256(sigma1)`
// (Matter Core §4.13.2.5 / §4.14.2.3). Without the IPK prefix the
// responder derives a different S2K than the initiator and Apple
// surfaces SecureChannel/INVALID_PARAMETER on Sigma2.
func sigma2Salt(ipk, responderRandom, responderEphPub, sigma1Bytes []byte) []byte {
	var buf bytes.Buffer
	buf.Grow(len(ipk) + len(responderRandom) + len(responderEphPub) + sha256.Size)
	buf.Write(ipk)
	buf.Write(responderRandom)
	buf.Write(responderEphPub)
	t := sha256.Sum256(sigma1Bytes)
	buf.Write(t[:])
	return buf.Bytes()
}

// sigma3Salt builds the per-spec HKDF salt for the S3K key:
// `IPK || SHA256(sigma1 || sigma2_full)`. Note: spec mandates the
// *complete* Sigma2 wire bytes (including encrypted2), not the
// pre-encryption partial form we used during the early TLV bring-up.
func sigma3Salt(ipk, sigma1Bytes, sigma2Bytes []byte) []byte {
	h := sha256.New()
	h.Write(sigma1Bytes)
	h.Write(sigma2Bytes)
	digest := h.Sum(nil)
	out := make([]byte, 0, len(ipk)+sha256.Size)
	out = append(out, ipk...)
	out = append(out, digest...)
	return out
}

// sessionKeysSalt builds the secure-session salt:
// `IPK || SHA256(sigma1 || sigma2 || sigma3)`.
func sessionKeysSalt(ipk, sigma1Bytes, sigma2Bytes, sigma3Bytes []byte) []byte {
	h := sha256.New()
	h.Write(sigma1Bytes)
	h.Write(sigma2Bytes)
	h.Write(sigma3Bytes)
	digest := h.Sum(nil)
	out := make([]byte, 0, len(ipk)+sha256.Size)
	out = append(out, ipk...)
	out = append(out, digest...)
	return out
}

// nonceForTBE returns the 13-byte AES-CCM nonce used to encrypt
// TBE2 / TBE3. Per Matter Core Spec §4.13.2.3 the nonces are
// hard-coded constants — the transcript binding rides in the AAD,
// not the nonce.
func nonceForTBE(label string) []byte {
	n := make([]byte, aesccm.NonceSize)
	copy(n, label)
	return n
}

var (
	nonceTBE2 = nonceForTBE("NCASE_Sigma2N")
	nonceTBE3 = nonceForTBE("NCASE_Sigma3N")
)

// ResumptionRecord is the per-peer CASE resumption state the protocol
// layer needs to attempt Sigma2_Resume. The store package embeds a
// superset of this; the protocol layer takes only what it uses so the
// sigma package stays free of the store import.
type ResumptionRecord struct {
	// SharedSecret is the 32-byte ECDH shared secret from the prior CASE
	// session. Acts as the HKDF IKM on the resume path.
	SharedSecret []byte
	// ResumptionID is the 16-byte id stored in TBE2 of the original Sigma2
	// and presented back by the initiator in Sigma1 tag 6.
	ResumptionID []byte
	// FabricIndex scopes the record to the fabric the original CASE
	// session was established under. The resumed session is registered
	// under this fabric — matter.js CaseServer.ts:151 destructures
	// `fabric` straight from the record; there is no Sigma3 on the
	// resume path, so the fabric can come from nowhere else.
	FabricIndex uint8
	// PeerNodeID is the peer's operational NodeID from the original
	// session. It feeds the AES-CCM nonce of the resumed session
	// (Matter §4.5.1.4 builds the nonce from the source NodeID);
	// leaving it zero makes every inbound decrypt fail.
	PeerNodeID uint64
	// PeerCATs carries the CASE Authenticated Tags from the peer's NOC
	// captured at the original Sigma3. The resume path re-grants them
	// without re-validating the NOC — matter.js CaseServer.ts:151
	// `caseAuthenticatedTags` from the record.
	PeerCATs []uint32
}

// ResumptionStore is the read side the Responder requires during
// ProcessSigma1 to attempt Sigma2_Resume. GetByID must return a
// ResumptionRecord and nil when the id is known; it must return nil
// record and non-nil error (wrap [ErrResumptionMICInvalid] or any
// sentinel) when the id is unknown so the responder can fall through to
// Full Sigma. Implementations must be safe for concurrent use.
type ResumptionStore interface {
	GetByID(resumptionID []byte) (*ResumptionRecord, error)
}

// Sigma1ProcessResult is the discriminated union returned by
// [Responder.ProcessSigma1WithResume]. Callers check IsResume to
// decide which opcode to send.
type Sigma1ProcessResult struct {
	// Sigma2 is non-nil when Full Sigma was performed.
	Sigma2 *Sigma2
	// Sigma2Resume is non-nil when the fast-path resume was performed.
	Sigma2Resume *Sigma2Resume
	// ResumeKeys holds the derived session keys after a Sigma2_Resume.
	// Populated only when Sigma2Resume != nil.
	ResumeKeys SessionKeys
}

// IsResume reports whether the result requires a Sigma2_Resume reply.
func (r Sigma1ProcessResult) IsResume() bool { return r.Sigma2Resume != nil }

// Initiator drives the commissioner-side of CASE. Construct with
// [NewInitiator], call [Initiator.GenerateSigma1] to produce the
// first message bytes, [Initiator.ProcessSigma2] to advance, and
// [Initiator.SharedSecret] / [Initiator.Sigma3Bytes] to retrieve the
// session keys + the final wire frame.
type Initiator struct {
	identity    *Identity
	verifier    PeerVerifier
	ephPriv     *ecdh.PrivateKey
	ephPubBytes []byte
	sessionID   uint16
	dest        [32]byte
	random      [RandomSize]byte

	sigma1Bytes []byte
	sigma3Bytes []byte
	keys        SessionKeys
	state       initiatorState
}

type initiatorState uint8

const (
	initiatorStateInit initiatorState = iota
	initiatorStateSigma1Sent
	initiatorStateFinished
)

// NewInitiator returns an Initiator ready to emit Sigma1.
func NewInitiator(identity *Identity, verifier PeerVerifier, sessionID uint16, destinationID [32]byte) *Initiator {
	return &Initiator{
		identity:  identity,
		verifier:  verifier,
		sessionID: sessionID,
		dest:      destinationID,
	}
}

// GenerateSigma1 produces the Sigma1 message bytes for transmission.
func (i *Initiator) GenerateSigma1() ([]byte, error) {
	if i.state != initiatorStateInit {
		return nil, fmt.Errorf("%w: GenerateSigma1 already called", ErrSessionState)
	}
	if _, err := rand.Read(i.random[:]); err != nil {
		return nil, fmt.Errorf("sigma: random: %w", err)
	}
	priv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("sigma: ephemeral keygen: %w", err)
	}
	i.ephPriv = priv
	i.ephPubBytes = priv.PublicKey().Bytes()

	msg := Sigma1{
		InitiatorRandom:    i.random,
		InitiatorSessionID: i.sessionID,
		DestinationID:      i.dest,
		InitiatorEphPubKey: i.ephPubBytes,
	}
	i.sigma1Bytes = msg.Marshal()
	i.state = initiatorStateSigma1Sent
	return i.sigma1Bytes, nil
}

// ProcessSigma2 consumes the responder's Sigma2 reply, validates the
// embedded TBE2 payload, and produces the Sigma3 reply bytes.
func (i *Initiator) ProcessSigma2(sigma2 Sigma2) ([]byte, error) {
	if i.state != initiatorStateSigma1Sent {
		return nil, fmt.Errorf("%w: GenerateSigma1 must run first", ErrSessionState)
	}
	if err := validatePoint(sigma2.ResponderEphPubKey); err != nil {
		return nil, err
	}

	respECDH, err := ecdh.P256().NewPublicKey(sigma2.ResponderEphPubKey)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidPoint, err) //nolint:errorlint // intentional double-wrap
	}
	shared, err := ecdhSharedSecret(i.ephPriv, respECDH)
	if err != nil {
		return nil, err
	}

	// S2K derivation per Matter §4.14.2.3:
	// salt = IPK || responderRandom || responderEphPubKey || SHA256(sigma1)
	salt2 := sigma2Salt(i.identity.IPK[:], sigma2.ResponderRandom[:], sigma2.ResponderEphPubKey, i.sigma1Bytes)
	s2k, err := hkdfDerive(shared, salt2, HKDFInfoSigma2, SessionKeySize)
	if err != nil {
		return nil, err
	}

	// Decrypt TBE2. Per spec the AES-CCM AAD is empty for both
	// Sigma2 and Sigma3 — the transcript binding rides in the salt,
	// not in the AEAD AAD. matter.js's `crypto.encrypt(key, data,
	// nonce)` call passes `aad=undefined` for the same reason.
	cipher, err := aesccm.New(s2k)
	if err != nil {
		return nil, err
	}
	plain, err := cipher.Open(nil, nonceTBE2, sigma2.Encrypted2, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnauthenticated, err)
	}
	tbe2, err := unmarshalTBE2(plain)
	if err != nil {
		return nil, err
	}

	// Verify peer signature using the NOC's embedded public key.
	respOpPub, err := i.verifier.VerifyAndExtractPubKey(tbe2.ResponderNOC, tbe2.ResponderICAC)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrSignatureInvalid, err)
	}
	if err := verifyTranscript(respOpPub, tbe2.Signature, tbe2.ResponderNOC, tbe2.ResponderICAC, sigma2.ResponderEphPubKey, i.ephPubBytes); err != nil {
		return nil, err
	}

	// Build TBE3 — initiator's signed-data carries (initNOC, initICAC,
	// initEph, respEph) per Matter §4.14.2.3.
	mySig, err := signTranscript(i.identity.PrivateKey, i.identity.NOC, i.identity.ICAC, i.ephPubBytes, sigma2.ResponderEphPubKey)
	if err != nil {
		return nil, err
	}
	tbe3 := TBE3Plaintext{
		InitiatorNOC:  i.identity.NOC,
		InitiatorICAC: i.identity.ICAC,
		Signature:     mySig,
	}
	tbe3Bytes := marshalTBE3(tbe3)

	// S3K derivation per Matter §4.14.2.3:
	// salt = IPK || SHA256(sigma1 || sigma2_full).
	sigma2Bytes := sigma2.Marshal()
	salt3 := sigma3Salt(i.identity.IPK[:], i.sigma1Bytes, sigma2Bytes)
	s3k, err := hkdfDerive(shared, salt3, HKDFInfoSigma3, SessionKeySize)
	if err != nil {
		return nil, err
	}
	enc, err := aesccm.New(s3k)
	if err != nil {
		return nil, err
	}
	// AES-CCM AAD = empty (spec); transcript binding is in the salt.
	enc3, err := enc.Seal(nil, nonceTBE3, tbe3Bytes, nil)
	if err != nil {
		return nil, err
	}
	sigma3 := Sigma3{Encrypted3: enc3}
	i.sigma3Bytes = sigma3.Marshal()

	// Secure-session salt = IPK || SHA256(sigma1 || sigma2 || sigma3).
	finalSalt := sessionKeysSalt(i.identity.IPK[:], i.sigma1Bytes, sigma2Bytes, i.sigma3Bytes)
	final, err := hkdfDerive(shared, finalSalt, HKDFInfoSessionKeys, FinalKeyMaterialSize)
	if err != nil {
		return nil, err
	}
	copy(i.keys.I2RKey[:], final[0:SessionKeySize])
	copy(i.keys.R2IKey[:], final[SessionKeySize:2*SessionKeySize])
	copy(i.keys.AttestationChallenge[:], final[2*SessionKeySize:3*SessionKeySize])

	i.state = initiatorStateFinished
	return i.sigma3Bytes, nil
}

// SessionKeys returns the derived I2R / R2I / AttestationChallenge.
// Valid only after [Initiator.ProcessSigma2] succeeded.
func (i *Initiator) SessionKeys() (SessionKeys, bool) {
	if i.state != initiatorStateFinished {
		return SessionKeys{}, false
	}
	return i.keys, true
}

// --- Responder ---

// Responder drives the device-side of CASE. Construct with
// [NewResponder], call [Responder.ProcessSigma1] to consume the
// commissioner's Sigma1 and produce Sigma2, then [Responder.ProcessSigma3]
// to finalise. Optionally wire [ResumptionStore] via
// [Responder.SetResumptionStore] to enable the Sigma2_Resume fast path.
type Responder struct {
	// mu serialises every state-mutating method on Responder. Apple
	// iOS sends Sigma1 as a multicast on every interface (IPv4,
	// IPv6-Link-Local, IPv6-Global), so the bridge UDP listener
	// receives identical Sigma1 datagrams in parallel. Without the
	// mutex, all parallel ProcessSigma1 invocations race past the
	// state==responderStateInit check, each generates fresh ECDH key
	// material, and emits a different Sigma2 wire payload. The
	// initiator picks the first received and drops the rest with
	// `Dropping message without piggyback ack` (Bug A).
	//
	// Mirrors matter.js packages/protocol/src/session/case/CaseServer.ts:
	// the per-exchange CaseHandler lives behind a single async-await
	// chain so handlers are sequential by construction.
	mu sync.Mutex

	identity         *Identity
	verifier         PeerVerifier
	sessionID        uint16
	resumptionStore  ResumptionStore
	identityResolver IdentityResolver
	// sessionIDRenewer, when wired, supplies a fresh local session id
	// every time the responder resets for a NEW Sigma1 — see
	// [Responder.SetSessionIDRenewer].
	sessionIDRenewer func(previous uint16) (uint16, bool)
	// sessionParams, when non-nil and non-empty, is emitted as Sigma2
	// context-tag 5 (responderSessionParams).
	sessionParams *SessionParameters

	ephPriv     *ecdh.PrivateKey
	ephPubBytes []byte

	random       [RandomSize]byte
	resumptionID []byte
	// resume records what the Sigma2_Resume fast path did for the
	// session the responder currently holds. Observability only —
	// nothing in the handshake reads it back. See [ResumeInfo].
	resume ResumeInfo

	sigma1Bytes   []byte
	initEphPub    []byte   // initiator ephemeral pub key parsed from Sigma1
	peerSessionID uint16   // initiatorSessionID from Sigma1; the value we stamp into outbound Header.SessionID
	peerNodeID    uint64   // NodeID extracted from the peer's NOC at Sigma3 verification; feeds the AES-CCM nonce
	peerCATs      []uint32 // CASE Authenticated Tags from the peer's NOC subject; feeds the ACL gate's per-subject match
	// peerSessionParams retains the initiator's MRP tuning hints from
	// Sigma1 tag 5 so the operational session can size its
	// retransmission backoff to the peer (matter.js MRP.ts:129).
	peerSessionParams *SessionParameters
	sigma2            Sigma2
	sigma2Bytes       []byte // full Sigma2 wire bytes; reused for S3K + final salts
	shared            []byte

	keys  SessionKeys
	state responderState
}

// PeerSessionID returns the InitiatorSessionID extracted from the
// peer's Sigma1. CASE callers route this into
// [channel.Config.PeerSessionID] so outbound packets stamp the
// SessionID the commissioner expects in its lookup table; without
// it Apple Home / chip-tool drops every reply on the encrypted
// path with `secure session not found`.
func (r *Responder) PeerSessionID() uint16 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.peerSessionID
}

// ResumeInfo describes the CASE resumption fast path
// ([Responder.ProcessSigma1WithResume]) for the session a responder
// currently holds. Resumed is false for a session established through
// Full Sigma and for a responder that has processed nothing yet.
//
// It exists because the resume branch is the one part of CASE whose
// correctness cannot be settled without a live controller. matter.js
// takes a fresh session id for a resumed session
// (packages/protocol/src/session/case/CaseServer.ts:#resume calls
// `getNextAvailableSessionId()` before creating the secure session); we
// keep the id the responder already announced. Renewing it here would
// allocate one id per MRP retransmit of a resume Sigma1, reusing it
// risks conflating the peer's previous message counters with the new
// session — and which failure a certified controller actually provokes
// is an interop question, not a unit-test one. Recording both ids makes
// the current behaviour readable from an operator report instead of
// only from the source. matter.js logs a resumed session for the same
// reason (NodeSession.ts:412 `logNew(logger, "Resumed", …)`).
//
// loom:reachable:reason="returned by Responder.ResumeInfo, which the daemon's CASE session-established callback reads on every resume before it logs the record; a data struct whose fields the logging path copies out, which the analyzer's type heuristic (reachable only via its own methods) cannot see used"
type ResumeInfo struct {
	// Resumed reports whether the session came from Sigma2_Resume
	// rather than Full Sigma.
	Resumed bool
	// PresentedResumptionID is the id the initiator sent in Sigma1 —
	// it names the cached record the controller resumed from.
	PresentedResumptionID []byte
	// IssuedResumptionID is the fresh id shipped in Sigma2_Resume for
	// the initiator's next resume.
	IssuedResumptionID []byte
	// SessionIDBefore and SessionIDAfter are the responder's local
	// session id around the resume. They are equal while the resume
	// path reuses the id; keeping both means a future change to that
	// choice shows up in the record without touching the caller.
	SessionIDBefore uint16
	SessionIDAfter  uint16
}

// ResumeInfo returns a copy of the resumption record for the session the
// responder currently holds. Safe to call from a session-established
// callback: the record is stamped before the fast path returns.
func (r *Responder) ResumeInfo() ResumeInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.resume
	out.PresentedResumptionID = append([]byte(nil), r.resume.PresentedResumptionID...)
	out.IssuedResumptionID = append([]byte(nil), r.resume.IssuedResumptionID...)
	return out
}

// PeerSessionParameters returns the initiator's MRP tuning hints from
// Sigma1 (tag 5) and whether the initiator supplied any. Session-open
// callers copy them onto the operational entry so the retransmission
// backoff honours the peer's advertised idle/active intervals
// (matter.js MRP.ts:129 retransmissionIntervalOf).
func (r *Responder) PeerSessionParameters() (SessionParameters, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.peerSessionParams == nil {
		return SessionParameters{}, false
	}
	return *r.peerSessionParams, true
}

// PeerNodeID returns the operational NodeID of the peer extracted
// from the verified NOC. The Matter AES-CCM nonce per §4.5.1.4 is
// `securityFlags || messageCounter || sourceNodeID` and the peer's
// outbound nonce uses ITS OWN NodeID — so to verify those packets
// we must hand the peer's NodeID into [channel.Config.PeerNodeID].
// Defaulting it to 0 (the previous behaviour) made every operational
// inbound from Apple Home fail AES-CCM tag verification with
// `aesccm: authentication failed`.
func (r *Responder) PeerNodeID() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.peerNodeID
}

// PeerCATs returns a copy of the CASE Authenticated Tags lifted out
// of the peer's NOC subject at Sigma3 verification. Returns nil when
// the verifier did not implement [PeerCATsExtractor] or the peer's
// NOC carried no CATs. CASE callers route this into
// [channel.Config.PeerCATs] so the IM dispatcher's ACL gate can match
// CAT-bearing ACEs (Matter §9.10.5.6).
func (r *Responder) PeerCATs() []uint32 {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.peerCATs) == 0 {
		return nil
	}
	out := make([]uint32, len(r.peerCATs))
	copy(out, r.peerCATs)
	return out
}

// SessionIdentity returns the (FabricIndex, NodeID) the multi-fabric
// destination resolver landed on for the current exchange. ok==false
// before Sigma1 has been processed. Bridge callers feed these values
// into [operational.Manager.OpenFromSigmaWithID] so the secure session
// is registered under the actually-selected fabric — without this, a
// Sigma1 that the resolver routed to fabric #1 would still be tagged
// with the factory-time fabric #2 (the bridge's last-installed default),
// and Apple's reads under fabric #1 would target a session table entry
// scoped to the wrong fabric.
//
// The fabric_index is read from [Identity.FabricIndex]; daemons that
// don't stamp it leave a zero (which is also the pre-resolver
// fallback). Returns the operational FabricIndex (0 = unknown / not
// set by daemon) and NodeID. Callers tolerant of fabric_index==0
// fall back to their factory-time default fabric.
func (r *Responder) SessionIdentity() (fabricIndex uint8, nodeID uint64, ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.identity == nil || r.state == responderStateInit {
		return 0, 0, false
	}
	return r.identity.FabricIndex, r.identity.NodeID, true
}

type responderState uint8

const (
	responderStateInit responderState = iota
	responderStateSigma2Sent
	responderStateFinished
	// responderStateFailed is terminal for the handshake attempt that
	// reached it: a Sigma3 that did not authenticate must not be
	// followed by a second Sigma3 against the same Sigma2. Only a fresh
	// Sigma1 clears it, which is how a controller retries anyway.
	responderStateFailed
)

// NewResponder returns a Responder ready to consume Sigma1.
func NewResponder(identity *Identity, verifier PeerVerifier, sessionID uint16) *Responder {
	return &Responder{identity: identity, verifier: verifier, sessionID: sessionID}
}

// SetResumptionStore wires the optional resumption-record lookup. When
// non-nil and Sigma1 carries tags 6+7, [Responder.ProcessSigma1WithResume]
// attempts Sigma2_Resume before falling through to Full Sigma.
func (r *Responder) SetResumptionStore(s ResumptionStore) {
	r.resumptionStore = s
}

// SessionID returns the local session id the responder announces in
// Sigma2 for the handshake it is currently serving. Callers that open
// the operational session after Sigma3 MUST read it here rather than
// remember the id the responder was constructed with: a second Sigma1
// on the same exchange renews it (see
// [Responder.SetSessionIDRenewer]).
func (r *Responder) SessionID() uint16 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sessionID
}

// SetSessionIDRenewer wires the local session-id source used when a
// NEW Sigma1 arrives on a responder that already served one. It is
// called with the id currently announced and returns the replacement;
// ok==false keeps the current id.
//
// matter.js takes the responder session id inside each Sigma1
// handling (packages/protocol/src/session/case/CaseServer.ts:266
// `getNextAvailableSessionId`), so every handshake gets its own id
// even when several run over one exchange. Our per-exchange
// CaseAdapter instead allocates once at construction, which made the
// second handshake register its session in the slot the first peer's
// session still occupied. Daemons therefore wire this to the
// operational manager's allocator; leaving it nil keeps the
// constructor-time id (single-handshake test rigs).
//
// The callback runs while the responder's own lock is held, so it must
// not call back into the responder.
func (r *Responder) SetSessionIDRenewer(fn func(previous uint16) (uint16, bool)) {
	r.mu.Lock()
	r.sessionIDRenewer = fn
	r.mu.Unlock()
}

// SetSessionParameters wires the responder's preferred MRP tuning
// hints. When set and non-empty, Sigma2 carries the value as
// context-tag 5 (`responderSessionParams`) so the commissioner can
// align its retransmit budget with the bridge's mDNS-advertised
// SII/SAI/SAT.
//
// Mirrors matter.js packages/protocol/src/session/case/CaseServer.ts:258-264.
// Pass nil to detach.
func (r *Responder) SetSessionParameters(p *SessionParameters) {
	r.sessionParams = p
}

// SetIdentityResolver wires a multi-fabric identity selector. When
// set, every fresh Sigma1 arrival runs the resolver against the
// inbound `(destinationID, initiatorRandom)` pair; the matching
// fabric's `(Identity, PeerVerifier)` replaces the baseline identity
// for Sigma2 generation. Pass nil to revert to the constructor-time
// single identity (test / single-fabric path).
//
// Required for Apple Home Multi-Admin pairing: the iPhone reconnects
// under the primary fabric while the iCloud system commissioner uses
// a different fabric, and both target the same bridge — without
// per-Sigma1 fabric selection, our Sigma2 carries the wrong NOC and
// the initiator drops with InvalidParam. Mirrors matter.js
// `FabricManager.findFabricFromDestinationId`.
func (r *Responder) SetIdentityResolver(resolver IdentityResolver) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.identityResolver = resolver
}

// ProcessSigma1WithResume is the resumption-aware entry point.
// It first attempts the Sigma2_Resume fast path (Matter §4.13.2.4)
// when Sigma1 carries resumptionId + initiatorResumeMic AND a
// ResumptionStore is wired; on any miss or MIC failure it falls
// through to Full Sigma automatically.
//
// The result's IsResume() flag tells the caller which opcode to use
// (SCOpcodeSigma2Resume vs SCOpcodeSigma2). After a resume the session
// is fully established — no Sigma3 is expected.
//
// Mirrors matter.js packages/protocol/src/session/case/CaseServer.ts::#resume.
func (r *Responder) ProcessSigma1WithResume(sigma1Bytes []byte) (Sigma1ProcessResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Parse the incoming Sigma1 so we can inspect the optional resume fields.
	sigma1, err := UnmarshalSigma1(sigma1Bytes)
	if err != nil {
		return Sigma1ProcessResult{}, err
	}
	r.peerSessionParams = sigma1.InitiatorSessionParams

	// Attempt resume path only when Sigma1 carries both optional fields
	// AND a store is wired. Mirrors matter.js CaseServer.ts:#handleSigma1:
	// `sigma1.resumptionId !== undefined && sigma1.initiatorResumeMic !== undefined`.
	if r.resumptionStore != nil &&
		len(sigma1.ResumptionID) == ResumptionIDSize &&
		len(sigma1.InitiatorResumeMIC) == ResumptionIDSize {
		result, ok, resumeErr := r.tryResume(sigma1)
		if resumeErr != nil {
			// Unrecoverable error (store I/O failure, crypto error
			// other than bad-MIC). Propagate — do not silently fall
			// through to Full Sigma.
			return Sigma1ProcessResult{}, resumeErr
		}
		if ok {
			return result, nil
		}
		// MIC miss / unknown id → fall through to Full Sigma below.
	}

	// Full Sigma path. Use processSigma1Locked rather than ProcessSigma1
	// because we already hold r.mu (sync.Mutex is not re-entrant).
	sigma2, err := r.processSigma1Locked(sigma1Bytes)
	if err != nil {
		return Sigma1ProcessResult{}, err
	}
	return Sigma1ProcessResult{Sigma2: &sigma2}, nil
}

// tryResume attempts the Sigma2_Resume path for a Sigma1 that carries
// resumptionId (tag 6) + initiatorResumeMic (tag 7). Returns
// (result, true, nil) on success, (_, false, nil) when the MIC misses
// (caller should fall through to Full Sigma), or (_, false, err) on a
// hard failure.
//
// KDF / MIC logic mirrors matter.js CaseServer.ts::#resume lines 139-213.
func (r *Responder) tryResume(sigma1 Sigma1) (Sigma1ProcessResult, bool, error) {
	// Captured before anything can change it so [ResumeInfo] can report
	// what the fast path did with the session id rather than only what
	// it ended up being.
	sessionIDBefore := r.sessionID
	rec, err := r.resumptionStore.GetByID(sigma1.ResumptionID)
	if err != nil || rec == nil {
		// Unknown id → fall through to Full Sigma.
		return Sigma1ProcessResult{}, false, nil //nolint:nilerr // miss is non-fatal; caller retries with full Sigma
	}

	// KDFSR1: peerResumeKey = HKDF(sharedSecret,
	//     initiatorRandom || peerResumptionId, "Sigma1_Resume", 16)
	// matter.js CaseServer.ts line 146-149.
	sr1Salt := make([]byte, 0, RandomSize+ResumptionIDSize)
	sr1Salt = append(sr1Salt, sigma1.InitiatorRandom[:]...)
	sr1Salt = append(sr1Salt, sigma1.ResumptionID...)
	peerResumeKey, err := hkdfDerive(rec.SharedSecret, sr1Salt, HKDFInfoSigma1Resume, SessionKeySize)
	if err != nil {
		return Sigma1ProcessResult{}, false, fmt.Errorf("sigma resume: KDFSR1: %w", err)
	}

	// Verify initiatorResumeMIC = AES-CCM-Encrypt(peerResumeKey, empty,
	//     nonce="NCASE_SigmaS1"). The MIC is over zero-length plaintext;
	// matter.js uses crypto.decrypt which verifies the tag on decrypt.
	// We verify by re-encrypting and comparing — equivalent since AES-CCM
	// with empty plaintext is deterministic under a fixed key+nonce.
	// matter.js CaseServer.ts line 153.
	if !verifyResumeMIC(peerResumeKey, sigma1.InitiatorResumeMIC, nonceResume1MIC) {
		// Bad MIC → fall through to Full Sigma per spec §4.13.2.4.
		return Sigma1ProcessResult{}, false, nil
	}

	// Resolve the responder identity by the record's FabricIndex. The
	// resume path has no Sigma3 (no NOC exchange), so the fabric is
	// authoritative from the record alone — matter.js CaseServer.ts:151
	// `const { sharedSecret, fabric, peerNodeId, caseAuthenticatedTags }
	// = cx.resumptionRecord`. A resolver miss means the fabric was
	// removed after the record was written; treat like an unknown id
	// and fall through to Full Sigma.
	if r.identityResolver != nil {
		if fir, ok := r.identityResolver.(FabricIndexResolver); ok {
			id, ver, found := fir.ResolveFabricIndex(rec.FabricIndex)
			if !found {
				return Sigma1ProcessResult{}, false, nil
			}
			r.identity = id
			r.verifier = ver
		}
	}

	// Generate a fresh 16-byte local resumption id for the response.
	freshResumptionID := make([]byte, ResumptionIDSize)
	if _, err := rand.Read(freshResumptionID); err != nil {
		return Sigma1ProcessResult{}, false, fmt.Errorf("sigma resume: fresh resumptionID: %w", err)
	}

	// KDFSR2: resumeKey = HKDF(sharedSecret,
	//     initiatorRandom || localResumptionId, "Sigma2_Resume", 16)
	// matter.js CaseServer.ts line 182-183.
	sr2Salt := make([]byte, 0, RandomSize+ResumptionIDSize)
	sr2Salt = append(sr2Salt, sigma1.InitiatorRandom[:]...)
	sr2Salt = append(sr2Salt, freshResumptionID...)
	resumeKey, err := hkdfDerive(rec.SharedSecret, sr2Salt, HKDFInfoSigma2Resume, SessionKeySize)
	if err != nil {
		return Sigma1ProcessResult{}, false, fmt.Errorf("sigma resume: KDFSR2: %w", err)
	}

	// Sigma2ResumeMIC = AES-CCM-Encrypt(resumeKey, empty,
	//     nonce="NCASE_SigmaS2"). matter.js CaseServer.ts line 184.
	sigma2ResumeMIC, err := sealResumeMIC(resumeKey, nonceResume2MIC)
	if err != nil {
		return Sigma1ProcessResult{}, false, fmt.Errorf("sigma resume: Sigma2ResumeMIC: %w", err)
	}

	// Derive operational session keys via HKDF(sharedSecret,
	//     initiatorRandom || peerResumptionId, "SessionResumptionKeys", 48).
	// Salt uses the PEER's resumption id (the one they presented in Sigma1),
	// mirroring matter.js CaseServer.ts line 165:
	// `secureSessionSalt = Bytes.concat(peerRandom, peerResumptionId)`.
	keySalt := make([]byte, 0, RandomSize+ResumptionIDSize)
	keySalt = append(keySalt, sigma1.InitiatorRandom[:]...)
	keySalt = append(keySalt, sigma1.ResumptionID...)
	finalMat, err := hkdfDerive(rec.SharedSecret, keySalt, HKDFInfoSessionResumptionKeys, FinalKeyMaterialSize)
	if err != nil {
		return Sigma1ProcessResult{}, false, fmt.Errorf("sigma resume: session keys: %w", err)
	}
	var keys SessionKeys
	copy(keys.I2RKey[:], finalMat[0:SessionKeySize])
	copy(keys.R2IKey[:], finalMat[SessionKeySize:2*SessionKeySize])
	copy(keys.AttestationChallenge[:], finalMat[2*SessionKeySize:3*SessionKeySize])

	// Adopt the resumed session's identity so the post-resume accessors
	// (PeerSessionID / PeerNodeID / PeerCATs / SessionIdentity /
	// ECDHSharedSecret / ResumptionID) describe THIS session, exactly as
	// they would after a full Sigma3. matter.js CaseServer.ts:171-186
	// creates the secure session from `peerNodeId`, `peerSessionId:
	// cx.peerSessionId`, `sharedSecret` and `caseAuthenticatedTags`,
	// then persists the fresh resumption id (`:212`). Without these the
	// resumed session registers with peerNodeID=0 (every inbound
	// AES-CCM verify fails, Matter §4.5.1.4 nonce) and peerSessionID=0
	// (every outbound reply stamps a session id the initiator never
	// allocated) — a dead session until the controller gives up on
	// resumption and re-runs Full Sigma.
	r.peerSessionID = sigma1.InitiatorSessionID
	r.peerNodeID = rec.PeerNodeID
	r.peerCATs = append([]uint32(nil), rec.PeerCATs...)
	r.shared = append([]byte(nil), rec.SharedSecret...)
	r.resumptionID = freshResumptionID
	r.keys = keys
	r.state = responderStateFinished
	// Stamp the record BEFORE returning: the caller fires its
	// session-established callback on the way out of the fast path, and
	// that callback is where the daemon reads it.
	r.resume = ResumeInfo{
		Resumed:               true,
		PresentedResumptionID: append([]byte(nil), sigma1.ResumptionID...),
		IssuedResumptionID:    append([]byte(nil), freshResumptionID...),
		SessionIDBefore:       sessionIDBefore,
		SessionIDAfter:        r.sessionID,
	}

	s2r := Sigma2Resume{
		ResumptionID:       freshResumptionID,
		Sigma2ResumeMIC:    sigma2ResumeMIC,
		ResponderSessionID: r.sessionID,
		// Propagate the responder's MRP tuning hints so the resuming peer
		// can align its retransmit budget. Mirrors matter.js CaseServer.ts:186-191.
		SessionParams: r.sessionParams,
	}
	return Sigma1ProcessResult{
		Sigma2Resume: &s2r,
		ResumeKeys:   keys,
	}, true, nil
}

// verifyResumeMIC checks the initiator's 16-byte AES-CCM MIC: encrypt
// empty plaintext under key+nonce and compare (constant-time) to mic.
// AES-CCM of empty plaintext is deterministic, so seal==verify.
func verifyResumeMIC(key, mic []byte, nonceStr string) bool {
	computed, err := sealResumeMIC(key, nonceStr)
	if err != nil {
		return false
	}
	return constantTimeEqual(computed, mic)
}

// sealResumeMIC AES-CCM-seals empty plaintext under key and nonce
// (zero-padded to NonceSize), returning the 16-byte tag only.
// Corresponds to matter.js crypto.encrypt(key, new Uint8Array(0), nonce).
func sealResumeMIC(key []byte, nonceStr string) ([]byte, error) {
	cipher, err := aesccm.New(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aesccm.NonceSize)
	copy(nonce, nonceStr)
	// Seal: AES-CCM over empty plaintext → 16-byte tag appended to empty ciphertext.
	sealed, err := cipher.Seal(nil, nonce, nil, nil)
	if err != nil {
		return nil, err
	}
	// sealed is the 16-byte tag only (no ciphertext for empty input).
	return sealed, nil
}

// ProcessSigma1 consumes the initiator's Sigma1 bytes and produces
// the Sigma2 reply.
//
// Idempotency: Apple Home retransmits Sigma1 over the post-Commissioning
// CASE channel (same ExchangeID, same Sigma1 payload) when its
// MRP-layer has not yet seen our Sigma2 ACK. Without idempotency,
// allocating fresh ephemeral keys + random + Sigma2 on every
// retransmit yields a NEW Sigma2 each time — Apple sticks with the
// FIRST Sigma2 it received, derives session keys against that
// Sigma2's ephemerals, and ships its Sigma3 with those keys. Our
// last-generated Responder state then fails to AES-CCM-decrypt the
// Sigma3 payload (`encrypted payload authentication failed`) and
// Apple logs `still not subscribed, marking the device as
// unreachable` ~16 s later. Mirrors matter.js
// `packages/protocol/src/session/case/CaseServer.ts:onSigma1` which
// caches the prior Sigma2 and replays it byte-for-byte on a
// duplicate Sigma1 instead of re-deriving.
func (r *Responder) ProcessSigma1(sigma1Bytes []byte) (Sigma2, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.processSigma1Locked(sigma1Bytes)
}

// processSigma1Locked is the lock-already-held variant used by both
// ProcessSigma1 (which acquires the mutex) and ProcessSigma1WithResume
// (which holds it across the resume probe + Full-Sigma fallback so the
// state transition stays atomic against parallel Sigma1 datagrams).
func (r *Responder) processSigma1Locked(sigma1Bytes []byte) (Sigma2, error) { //nolint:funlen // single-purpose SIGMA-1 processing with many crypto/validation branches
	switch {
	case r.state == responderStateInit:
		// fresh responder — fall through to the standard processing.
	case r.state != responderStateFailed && bytes.Equal(r.sigma1Bytes, sigma1Bytes) && r.sigma2Bytes != nil:
		// Idempotent replay: Apple Home retransmits Sigma1 over MRP
		// when its layer hasn't yet observed our Sigma2 ACK. Same
		// Sigma1 bytes → same Sigma2 wire reply (matter.js
		// `CaseServer.ts:onSigma1` caches and replays). Returning a
		// freshly-derived Sigma2 here would hand the initiator new
		// ephemerals; its first-received Sigma2 wins so the Sigma3
		// would AES-CCM-fail against our last-derived keys.
		return r.sigma2, nil
	default:
		// Different Sigma1 (or post-Sigma3 Finished state) on the
		// same Responder slot. Apple Home opens new CASE sessions
		// on the SAME ExchangeID after CommissioningComplete (the
		// iCloud Hub Companion fabric grafts onto the existing
		// exchange). Reset the responder and proceed as a clean
		// init. Mirrors matter.js's per-exchange CaseServer which
		// allocates a fresh handler instance for every new Sigma1.
		r.ephPriv = nil
		r.ephPubBytes = nil
		r.random = [RandomSize]byte{}
		r.resumptionID = nil
		// The resume record describes the session the responder holds
		// now. Apple Home grafts a full handshake onto an exchange that
		// already carried a resumed session; keeping the old record
		// would report that session as resumed too.
		r.resume = ResumeInfo{}
		r.sigma1Bytes = nil
		r.initEphPub = nil
		r.peerSessionID = 0
		r.peerNodeID = 0
		// CASE Authenticated Tags and the MRP hints are peer-supplied
		// state and describe the peer this responder served before.
		// Clearing them here keeps every accessor honest for the whole
		// span of the new handshake, not just from its Sigma3 onwards.
		r.peerCATs = nil
		r.peerSessionParams = nil
		// Take a fresh local session id for the new handshake: the one
		// already announced may be occupied by the session the first
		// handshake established, and registering the second peer under
		// it would displace the first without tearing it down.
		if r.sessionIDRenewer != nil {
			if next, ok := r.sessionIDRenewer(r.sessionID); ok {
				r.sessionID = next
			}
		}
		r.sigma2 = Sigma2{}
		r.sigma2Bytes = nil
		r.shared = nil
		r.keys = SessionKeys{}
		r.state = responderStateInit
	}
	sigma1, err := UnmarshalSigma1(sigma1Bytes)
	if err != nil {
		return Sigma2{}, err
	}
	r.peerSessionParams = sigma1.InitiatorSessionParams
	// Multi-fabric identity resolution. Apple Home Multi-Admin keeps
	// two fabrics live concurrently; Sigma1's DestinationID =
	// HMAC(opIPK, random||rootPub||fabricID||nodeID) tells us which
	// fabric the initiator is addressing. The daemon-side resolver
	// iterates every persisted fabric, computes the candidate
	// destinationID, and returns the matching identity. A miss falls
	// back to the constructor-time identity (single-fabric / test
	// path); production callers should treat the miss as "no shared
	// trust roots" and reject the exchange, but we keep the fallback
	// here so the per-exchange CaseAdapter can still process Sigma1
	// during the brief pre-AddNOC window when only one fabric exists.
	if r.identityResolver != nil {
		if id, ver, ok := r.identityResolver.ResolveSigma1Destination(sigma1.DestinationID, sigma1.InitiatorRandom); ok {
			r.identity = id
			r.verifier = ver
		}
	}
	r.sigma1Bytes = append([]byte(nil), sigma1Bytes...)
	r.initEphPub = append([]byte(nil), sigma1.InitiatorEphPubKey...)
	r.peerSessionID = sigma1.InitiatorSessionID

	initEphBytes := sigma1.InitiatorEphPubKey
	if err := validatePoint(initEphBytes); err != nil {
		return Sigma2{}, err
	}
	initECDH, err := ecdh.P256().NewPublicKey(initEphBytes)
	if err != nil {
		return Sigma2{}, fmt.Errorf("%w: %v", ErrInvalidPoint, err) //nolint:errorlint // intentional double-wrap
	}

	if _, err := rand.Read(r.random[:]); err != nil {
		return Sigma2{}, fmt.Errorf("sigma: random: %w", err)
	}
	priv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return Sigma2{}, fmt.Errorf("sigma: ephemeral keygen: %w", err)
	}
	r.ephPriv = priv
	r.ephPubBytes = priv.PublicKey().Bytes()

	r.shared, err = ecdhSharedSecret(priv, initECDH)
	if err != nil {
		return Sigma2{}, err
	}

	// Per Matter §4.14.2.3 the responder signs over a TLV-encoded
	// `signed_data` carrying (responderNOC, responderICAC?,
	// responderEph, initiatorEph). The initiator's Sigma3 signature
	// later flips the eph order to (initiatorEph, responderEph) —
	// asymmetry intentional, locks each party's signature to its role.
	mySig, err := signTranscript(r.identity.PrivateKey, r.identity.NOC, r.identity.ICAC, r.ephPubBytes, initEphBytes)
	if err != nil {
		return Sigma2{}, err
	}

	// Generate a 16-byte resumptionID for the optional
	// session-resumption surface.
	r.resumptionID = make([]byte, 16)
	if _, err := rand.Read(r.resumptionID); err != nil {
		return Sigma2{}, err
	}

	tbe2 := TBE2Plaintext{
		ResponderNOC:  r.identity.NOC,
		ResponderICAC: r.identity.ICAC,
		Signature:     mySig,
		ResumptionID:  r.resumptionID,
	}
	tbe2Bytes := marshalTBE2(tbe2)

	// S2K derivation per Matter §4.14.2.3:
	// salt = IPK || responderRandom || responderEphPubKey || SHA256(sigma1).
	salt2 := sigma2Salt(r.identity.IPK[:], r.random[:], r.ephPubBytes, r.sigma1Bytes)
	s2k, err := hkdfDerive(r.shared, salt2, HKDFInfoSigma2, SessionKeySize)
	if err != nil {
		return Sigma2{}, err
	}
	cipher, err := aesccm.New(s2k)
	if err != nil {
		return Sigma2{}, err
	}
	// AES-CCM AAD = empty; transcript binding rides in the salt only.
	enc2, err := cipher.Seal(nil, nonceTBE2, tbe2Bytes, nil)
	if err != nil {
		return Sigma2{}, err
	}

	r.sigma2 = Sigma2{
		ResponderRandom:    r.random,
		ResponderSessionID: r.sessionID,
		ResponderEphPubKey: r.ephPubBytes,
		Encrypted2:         enc2,
		SessionParams:      r.sessionParams,
	}
	// Pre-compute the full Sigma2 wire bytes once for both the S3K
	// salt (next step) and the secure-session salt (after Sigma3).
	r.sigma2Bytes = r.sigma2.Marshal()

	r.state = responderStateSigma2Sent
	return r.sigma2, nil
}

// ProcessSigma3 verifies the initiator's Sigma3 envelope, validates
// the operational signature, and finalises the session keys.
//
// Idempotency: Apple Home retransmits Sigma3 over MRP when its layer
// has not yet observed our Sigma3-Success StatusReport ACK. A
// retransmit lands here with `state == Finished`; returning an error
// drops the StatusReport reply and Apple's pair times out 9 s later
// with `HMMTRErrorDomain Code=9`. Treat a retransmit of the same
// Sigma3 bytes as success — the caller (CaseAdapter.ProcessSigma3)
// then re-emits the cached StatusReport(Success). Mirrors matter.js
// `CaseServer.ts:onSigma3` which converges the responder on the
// finished state and re-acks duplicates.
//
// A Sigma3 that does not authenticate is terminal for this handshake:
// the responder accepts no further Sigma3 until a new Sigma1 restarts
// it, and commits nothing out of the rejected message.
func (r *Responder) ProcessSigma3(sigma3Bytes []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state == responderStateFinished {
		// Already verified this exchange — re-ack via cached state.
		// Matched on Finished because Sigma3 transitions the state
		// there on the first success.
		return nil
	}
	if r.state != responderStateSigma2Sent {
		return fmt.Errorf("%w: ProcessSigma1 must run first", ErrSessionState)
	}
	if err := r.verifySigma3Locked(sigma3Bytes); err != nil {
		// A Sigma3 that does not authenticate ends the handshake.
		// Leaving the responder in Sigma2Sent would let a second Sigma3
		// be verified against the same Sigma2 — the shape that turns any
		// per-attempt state into cross-attempt state. matter.js has no
		// such path: CaseServer reads exactly one Sigma3 and the first
		// failure ends the exchange
		// (packages/protocol/src/session/case/CaseServer.ts:275-309).
		// Only a fresh Sigma1 restarts this responder.
		r.state = responderStateFailed
		return err
	}
	r.state = responderStateFinished
	return nil
}

// verifySigma3Locked runs the Sigma3 crypto and, once the transcript
// signature has authenticated the peer, commits the peer identity and
// the session keys. Caller holds r.mu and owns the state transition.
func (r *Responder) verifySigma3Locked(sigma3Bytes []byte) error {
	sigma3, err := UnmarshalSigma3(sigma3Bytes)
	if err != nil {
		return err
	}
	// S3K derivation per Matter §4.14.2.3:
	// salt = IPK || SHA256(sigma1 || sigma2_full).
	salt3 := sigma3Salt(r.identity.IPK[:], r.sigma1Bytes, r.sigma2Bytes)
	s3k, err := hkdfDerive(r.shared, salt3, HKDFInfoSigma3, SessionKeySize)
	if err != nil {
		return err
	}
	cipher, err := aesccm.New(s3k)
	if err != nil {
		return err
	}
	// AES-CCM AAD = empty (transcript binding via salt).
	plain, err := cipher.Open(nil, nonceTBE3, sigma3.Encrypted3, nil)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrUnauthenticated, err)
	}
	tbe3, err := unmarshalTBE3(plain)
	if err != nil {
		return err
	}
	initOpPub, err := r.verifier.VerifyAndExtractPubKey(tbe3.InitiatorNOC, tbe3.InitiatorICAC)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrSignatureInvalid, err)
	}
	// Bind the peer's NOC to the responder-selected fabric: the chain
	// verification above only proves the NOC links to the fabric root,
	// not that its subject fabric-id equals the fabric we signed Sigma2
	// under. matter.js checks the two separately and rejects a mismatch
	// BEFORE the transcript signature check. Verifiers that do not
	// expose the fabric-id (protocol-layer test rigs) skip the check;
	// a verifier that exposes it but cannot read the fabric-id out of
	// a NOC it just accepted describes an anomalous certificate, and
	// matter.js has no path that proceeds without the comparison.
	// Mirrors matter.js packages/protocol/src/session/case/CaseServer.ts:299-306.
	if extractor, ok := r.verifier.(PeerFabricIDExtractor); ok && r.identity != nil {
		peerFabricID, ferr := extractor.PeerFabricIDFromNOC(tbe3.InitiatorNOC)
		if ferr != nil {
			return fmt.Errorf("%w: peer NOC fabric-id unreadable: %w", ErrFabricIDMismatch, ferr)
		}
		if peerFabricID != r.identity.FabricID {
			return fmt.Errorf("%w: NOC fabric-id 0x%016X != responder fabric-id 0x%016X",
				ErrFabricIDMismatch, peerFabricID, r.identity.FabricID)
		}
	}
	// Nothing lifted out of the peer's certificate may reach responder
	// state before the transcript signature proves the sender owns it:
	// every step up to here is reproducible by any node that holds the
	// fabric IPK, so an unauthenticated Sigma3 could otherwise install
	// another member's identity on this exchange. matter.js keeps the
	// decoded subject in locals across verifyEcdsa and only then builds
	// the secure session from peerNodeId + caseAuthenticatedTags
	// (packages/protocol/src/session/case/CaseServer.ts:302-327).
	if err := verifyTranscript(initOpPub, tbe3.Signature, tbe3.InitiatorNOC, tbe3.InitiatorICAC, r.initEphPub, r.ephPubBytes); err != nil {
		return err
	}

	// Lift the peer's NodeID out of the now-authenticated NOC if the
	// verifier supports it (PeerNodeIDExtractor). The value rides
	// into the secure channel's AES-CCM nonce (Matter §4.5.1.4)
	// so we can authenticate the peer's outbound packets.
	if extractor, ok := r.verifier.(PeerNodeIDExtractor); ok {
		if pid, perr := extractor.PeerNodeIDFromNOC(tbe3.InitiatorNOC); perr == nil {
			r.peerNodeID = pid
		}
	}
	// Lift CASE Authenticated Tags out of the same NOC subject for the
	// ACL gate's per-subject match (Matter §9.10.5.6). The tag set is
	// replaced, never merged: a peer whose NOC carries no CATs must end
	// up with none, otherwise it inherits every CAT-scoped ACE written
	// for whoever held this responder before it. An extractor that
	// errors or reports no CATs leaves r.peerCATs nil — the ACL gate
	// then only matches operational-node-id ACEs.
	r.peerCATs = nil
	if extractor, ok := r.verifier.(PeerCATsExtractor); ok {
		if cats, cerr := extractor.PeerCATsFromNOC(tbe3.InitiatorNOC); cerr == nil && len(cats) > 0 {
			r.peerCATs = append([]uint32(nil), cats...)
		}
	}

	// Secure-session salt = IPK || SHA256(sigma1 || sigma2 || sigma3).
	finalSalt := sessionKeysSalt(r.identity.IPK[:], r.sigma1Bytes, r.sigma2Bytes, sigma3Bytes)
	final, err := hkdfDerive(r.shared, finalSalt, HKDFInfoSessionKeys, FinalKeyMaterialSize)
	if err != nil {
		return err
	}
	copy(r.keys.I2RKey[:], final[0:SessionKeySize])
	copy(r.keys.R2IKey[:], final[SessionKeySize:2*SessionKeySize])
	copy(r.keys.AttestationChallenge[:], final[2*SessionKeySize:3*SessionKeySize])
	return nil
}

// SessionKeys returns the derived I2R / R2I / AttestationChallenge.
// Valid only after [Responder.ProcessSigma3] succeeded.
func (r *Responder) SessionKeys() (SessionKeys, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state != responderStateFinished {
		return SessionKeys{}, false
	}
	return r.keys, true
}

// ECDHSharedSecret returns a defensive copy of the ECDH-derived shared
// secret produced during Sigma1/2 (the value used as HKDF IKM for S2K,
// S3K, SessionKeys, and all resumption KDFs). Returns nil before
// [Responder.ProcessSigma1] has been called.
//
// The caller (daemon.go CASE onEstablished) must pass this value to
// [operational.Manager.PersistResumption] so that returning peers can
// resume via Sigma2_Resume. Mirrors matter.js
// packages/protocol/src/session/case/CaseServer.ts:210 —
// `cx.resumptionRecord.sharedSecret`.
func (r *Responder) ECDHSharedSecret() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.shared) == 0 {
		return nil
	}
	return append([]byte(nil), r.shared...)
}

// ResumptionID returns a defensive copy of the 16-byte resumption ID
// the responder placed in TBE2 (Sigma2 tag 4). Returns nil before
// [Responder.ProcessSigma1] has been called.
//
// The caller (daemon.go CASE onEstablished) must pass this value to
// [operational.Manager.PersistResumption] together with
// [Responder.ECDHSharedSecret] so that the resumption record is
// complete and a future Sigma1 carrying this ID can trigger
// Sigma2_Resume. Mirrors matter.js
// packages/protocol/src/session/case/CaseServer.ts:210 —
// `cx.resumptionRecord.resumptionId`.
func (r *Responder) ResumptionID() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.resumptionID) == 0 {
		return nil
	}
	return append([]byte(nil), r.resumptionID...)
}

// constantTimeKeysEqual is a debugging aid the test code uses to
// compare initiator/responder keys in constant time. Only exposed for
// testing — production code should compare via crypto/subtle directly.
func constantTimeKeysEqual(a, b SessionKeys) bool {
	return constantTimeEqual(a.I2RKey[:], b.I2RKey[:]) &&
		constantTimeEqual(a.R2IKey[:], b.R2IKey[:]) &&
		constantTimeEqual(a.AttestationChallenge[:], b.AttestationChallenge[:])
}
