// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package bridge

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/matter/secure/channel"
	"github.com/SukramJ/openccu-loom/internal/north/matter/secure/sigma"
	"github.com/SukramJ/openccu-loom/internal/north/matter/secure/spake2"
	"github.com/SukramJ/openccu-loom/internal/north/matter/transport/mrp"
)

// Errors specific to the production handler adapters.
var (
	// ErrPBKDFParamsMissing is returned by [PaseAdapter.ProcessPBKDFParamRequest]
	// when the adapter has not been configured with the bridge's PBKDF
	// salt + iterations via [PaseAdapter.SetPBKDFParams]. Without that
	// the adapter cannot answer a commissioner that asks for the
	// in-band copy of the PBKDF parameters (HasPBKDFParameters=false).
	ErrPBKDFParamsMissing = errors.New("bridge: PBKDF params not configured")
)

// PaseAdapter wraps a [spake2.Verifier] in the [PaseHandler] port the
// SecureChannel router consumes. The bridge constructs one adapter
// per pending PASE exchange (Verifier state is not safe for
// concurrent use across distinct exchanges, but the adapter itself
// has no mutable state — concurrency safety is the verifier's
// responsibility).
//
// PASE flow:
//
//  1. Commissioner sends PBKDFParamRequest (opcode 0x20). Bridge
//     responds with PBKDFParamResponse (0x21) carrying the salt +
//     iterations the bridge advertises. v1.1 stub returns
//     [ErrPBKDFNotImplemented]; the daemon must hand pre-negotiated
//     params via a different path until the response encoder lands.
//  2. Commissioner sends Pake1 (0x22) carrying pA. Bridge runs
//     [spake2.Verifier.ProcessPake1] and replies Pake2 (0x23) with
//     Y + cB.
//  3. Commissioner sends Pake3 (0x24) carrying cA. Bridge runs
//     [spake2.Verifier.ProcessPake3]; on success the optional
//     [PaseAdapter.OnSessionEstablished] callback fires with the
//     freshly-derived shared secret so the operational session
//     manager can register the new session in one atomic step.
type PaseAdapter struct {
	// mu serialises all PASE-state transitions (PBKDF capture →
	// Pake1 verifier allocation → Pake3 verifier consumption).
	// PASE is a strictly serial protocol per exchange, but the
	// SC router's PaseHandlerProvider may dispatch concurrent
	// exchanges to the same adapter instance; without this mutex
	// two commissioners pairing in parallel would race the
	// `verifier` pointer and `pbkdfReqBytes`/`pbkdfRespBytes`
	// slices. Holding the mutex across the verifier crypto is
	// fine — Pake1 takes < 50 ms even on Raspberry-Pi-class CPUs.
	mu              sync.Mutex
	verifierFactory func(context []byte) *spake2.Verifier
	verifier        *spake2.Verifier // set on Pake1, cleared on Pake3 / next Pake1
	onEstablished   PaseSessionEstablished

	// PBKDF response config — populated via [PaseAdapter.SetPBKDFParams].
	pbkdfIterations    uint32
	pbkdfSalt          []byte
	responderSessionID uint16
	randomSource       func() [spake2.PBKDFRandomSize]byte // overridable for tests

	// pbkdfReqBytes + pbkdfRespBytes are the wire-bytes of the most
	// recent PBKDFParamRequest / PBKDFParamResponse exchange. Matter
	// §4.13.4 mandates that the SPAKE2+ context fed into the
	// transcript is SHA-256("CHIP PAKE V1 Commissioning" || request
	// || response); both sides hash these into their context to bind
	// the PASE session to the negotiated parameters. Captured by
	// ProcessPBKDFParamRequest, consumed by ProcessPake1.
	pbkdfReqBytes  []byte
	pbkdfRespBytes []byte

	// peerSessionID is the InitiatorSessionID the commissioner
	// supplied in PBKDFParamRequest. Captured here so the
	// post-Pake3 OnSessionEstablished callback can hand it to
	// operational.Manager.OpenFromPase — the bridge needs to stamp
	// this value (NOT its own local session id) into outbound
	// Header.SessionID for any encrypted reply.
	peerSessionID uint16
}

// PaseSessionEstablished fires after a successful Pake3 verification.
// sharedSecret is the raw 16-byte Spake2+ Ke; peerSessionID is the
// commissioner's local session ID (InitiatorSessionID from
// PBKDFParamRequest) — the bridge MUST stamp this into outbound
// Header.SessionID so the commissioner can resolve the session in
// its own table. Implementations typically hand sharedSecret +
// peerSessionID to [operational.Manager.OpenFromPase] which derives
// the I2R/R2I session keys and registers the new [channel.Session].
// A non-nil error from the callback is wrapped and returned from
// ProcessPake3 (the SecureChannel router logs it and the
// commissioner falls back).
type PaseSessionEstablished func(sharedSecret []byte, peerSessionID uint16) error

// NewPaseAdapter wraps v as a single-shot adapter — the verifier is
// reused for every Pake1 the adapter sees. Suitable for tests and
// single-PASE-attempt scenarios. The factory ignores the per-Pake1
// context override and always returns the same verifier; this is OK
// for tests that don't exercise the PBKDFParamRequest/Response
// round, but production should use [NewPaseAdapterWithFactory] so
// the verifier's context can be derived from the actual exchanged
// PBKDFParam wire bytes (Matter §4.13.4).
//
// A nil verifier produces an adapter that returns errors from every
// Pake* call — useful for tests of the missing-verifier defensive
// path.
func NewPaseAdapter(v *spake2.Verifier) *PaseAdapter {
	return &PaseAdapter{
		verifierFactory: func([]byte) *spake2.Verifier { return v },
		verifier:        v,
	}
}

// NewPaseAdapterWithFactory wraps a verifier factory. The adapter
// invokes the factory at every Pake1 with the **per-exchange
// SPAKE2+ context** — SHA-256("CHIP PAKE V1 Commissioning" ||
// PBKDFParamRequest_bytes || PBKDFParamResponse_bytes) per Matter
// §4.13.4 — and the returned verifier MUST initialise its transcript
// with that 32-byte digest, not the literal context string.
//
// The factory MUST return a fresh verifier each call. Returning a
// shared verifier defeats the purpose; tests that need observable
// state across calls should use [NewPaseAdapter] directly.
//
// Concurrent PASE attempts (multiple commissioners pairing in
// parallel) are NOT supported — the adapter still tracks one
// "current" verifier between Pake1 and Pake3. Per-exchange
// concurrent dispatch requires a per-exchange-id PaseHandler-Provider
// on the bridge router; in practice operators open the commissioning
// window for one commissioner at a time so serial-with-retry covers
// the realistic case.
func NewPaseAdapterWithFactory(factory func(context []byte) *spake2.Verifier) *PaseAdapter {
	return &PaseAdapter{verifierFactory: factory}
}

// SetOnSessionEstablished installs a callback that fires after a
// successful Pake3 verification. Pass nil to clear.
func (a *PaseAdapter) SetOnSessionEstablished(cb PaseSessionEstablished) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.onEstablished = cb
}

// SetPBKDFParams configures the PBKDF salt + iteration count the
// adapter advertises in the PBKDFParamResponse, plus the responder
// session ID that goes into the response and into the bridge's
// inbound encrypted Header.SessionID once the PASE round completes.
// The values must match what was passed to
// [spake2.NewVerifierContext] — otherwise the commissioner's (w0,w1)
// derivation diverges from the bridge's and Pake1 verification fails.
func (a *PaseAdapter) SetPBKDFParams(iterations uint32, salt []byte, responderSessionID uint16) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pbkdfIterations = iterations
	a.pbkdfSalt = append([]byte(nil), salt...) // defensive copy
	a.responderSessionID = responderSessionID
}

// SetRandomSource overrides the random-byte source used for
// ResponderRandom in [PaseAdapter.ProcessPBKDFParamRequest]. The
// default reads from crypto/rand; tests substitute a deterministic
// source so the encoded response is byte-stable.
func (a *PaseAdapter) SetRandomSource(fn func() [spake2.PBKDFRandomSize]byte) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.randomSource = fn
}

// ProcessPBKDFParamRequest decodes the commissioner's request, picks
// a fresh ResponderRandom, and assembles the PBKDFParamResponse.
// The response carries the bridge's PBKDF salt + iterations only when
// the request flagged HasPBKDFParameters=false; otherwise the
// commissioner already knows them (e.g. from the QR / manual code).
//
// The wire-bytes of both the inbound request and the outbound
// response are captured on the adapter so the subsequent
// ProcessPake1 can build the SPAKE2+ context per Matter §4.13.4
// (SHA-256("CHIP PAKE V1 Commissioning" || req || resp)).
//
// Returns [ErrPBKDFParamsMissing] when [SetPBKDFParams] hasn't been
// called yet — the SecureChannel router degrades to a debug log so
// the bridge's pre-commissioner boot phase doesn't flood warnings.
func (a *PaseAdapter) ProcessPBKDFParamRequest(payload []byte) (opcode uint8, respPayload []byte, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.pbkdfIterations == 0 || len(a.pbkdfSalt) == 0 {
		return 0, nil, ErrPBKDFParamsMissing
	}
	req, err := spake2.DecodePBKDFParamRequest(payload)
	if err != nil {
		return 0, nil, fmt.Errorf("bridge: PBKDFParamRequest decode: %w", err)
	}
	source := a.randomSource
	if source == nil {
		source = randPBKDFRandom
	}
	respRand := source()
	resp := spake2.PBKDFParamResponse{
		InitiatorRandom:    req.InitiatorRandom,
		ResponderRandom:    respRand[:],
		ResponderSessionID: a.responderSessionID,
	}
	if !req.HasPBKDFParameters {
		resp.Parameters = &spake2.PBKDFParameters{
			Iterations: a.pbkdfIterations,
			Salt:       a.pbkdfSalt,
		}
	}
	respBytes := resp.Marshal()
	// Capture both the inbound + outbound bytes for the upcoming
	// SPAKE2+ context derivation. Defensive copies — the SC router
	// recycles the inbound payload buffer between datagrams.
	a.pbkdfReqBytes = append(a.pbkdfReqBytes[:0], payload...)
	a.pbkdfRespBytes = append(a.pbkdfRespBytes[:0], respBytes...)
	// Capture the commissioner's local session id so the post-Pake3
	// session pickup can hand it to the operational manager.
	a.peerSessionID = req.InitiatorSessionID
	return mrp.SCOpcodePBKDFParamResponse, respBytes, nil
}

// randPBKDFRandom is the production random source for ResponderRandom.
// crypto/rand.Read panics on extreme failure modes (entropy pool
// drained beyond recovery); we propagate via a zero-fill to keep the
// adapter signature error-free, and let the commissioner reject the
// downstream session if the response somehow lands with all-zero
// bytes. In practice crypto/rand.Read on Linux never fails.
func randPBKDFRandom() [spake2.PBKDFRandomSize]byte {
	var out [spake2.PBKDFRandomSize]byte
	_, _ = rand.Read(out[:])
	return out
}

// ProcessPake1 decodes the wire payload, allocates a fresh verifier
// from the factory (so a retry after a failed previous Pake3 starts
// clean), runs it, and encodes the Pake2 reply.
//
// The verifier-context passed to the factory is the SPAKE2+ context
// hash per Matter §4.13.4: SHA-256("CHIP PAKE V1 Commissioning" ||
// PBKDFParamRequest_bytes || PBKDFParamResponse_bytes). Both sides
// derive this hash independently from the wire-bytes that crossed
// the channel during the PBKDF round. Without it the prover and
// verifier compute different transcript hashes and the cB tag
// verification on the prover side fails.
func (a *PaseAdapter) ProcessPake1(payload []byte) (opcode uint8, respPayload []byte, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.verifierFactory == nil {
		return 0, nil, errors.New("bridge: PaseAdapter without verifier factory")
	}
	context, err := a.computePaseContextLocked()
	if err != nil {
		return 0, nil, err
	}
	v := a.verifierFactory(context)
	if v == nil {
		return 0, nil, errors.New("bridge: PaseAdapter factory returned nil verifier")
	}
	a.verifier = v
	pA, err := spake2.DecodePake1(payload)
	if err != nil {
		// Emit StatusReport(FAILURE, InvalidParameter) so the commissioner
		// stops retransmitting Pake1. Mirrors chip PASESession.cpp exit-path.
		body := mrp.EncodeStatusReport(
			mrp.SCStatusGeneralFailure,
			uint32(mrp.SecureChannelProtocolID),
			mrp.SCStatusProtocolInvalidParameter,
			nil,
		)
		return mrp.SCOpcodeStatusReport, body, nil
	}
	out, err := v.ProcessPake1(pA)
	if err != nil {
		body := mrp.EncodeStatusReport(
			mrp.SCStatusGeneralFailure,
			uint32(mrp.SecureChannelProtocolID),
			mrp.SCStatusProtocolInvalidParameter,
			nil,
		)
		return mrp.SCOpcodeStatusReport, body, nil
	}
	return mrp.SCOpcodePake2, spake2.EncodePake2(out), nil
}

// computePaseContextLocked derives the SPAKE2+ context per Matter
// §4.13.4. Caller MUST hold a.mu — the function reads pbkdfReqBytes /
// pbkdfRespBytes which the unlocked write path in
// ProcessPBKDFParamRequest mutates. Returns an error when
// ProcessPBKDFParamRequest hasn't run yet — commissioners are
// required to send PBKDFParamRequest before Pake1 per Spec §4.13, so
// missing capture state is a protocol violation the bridge surfaces
// as a wrapped error.
func (a *PaseAdapter) computePaseContextLocked() ([]byte, error) {
	if len(a.pbkdfReqBytes) == 0 || len(a.pbkdfRespBytes) == 0 {
		return nil, errors.New("bridge: Pake1 before PBKDFParamRequest/Response — context cannot be derived")
	}
	h := sha256.New()
	h.Write([]byte(spake2.MatterContext))
	h.Write(a.pbkdfReqBytes)
	h.Write(a.pbkdfRespBytes)
	return h.Sum(nil), nil
}

// ProcessPake3 decodes the wire payload and runs the verifier
// allocated during the matching ProcessPake1. On success the
// optional [PaseAdapter.SetOnSessionEstablished] callback fires
// with the freshly-derived Spake2+ shared secret and the adapter
// returns a Secure-Channel StatusReport with
// SESSION_ESTABLISHMENT_SUCCESS so the commissioner can complete
// the handshake (Matter §4.13.4 step 11). The verifier is cleared
// after Pake3 so a stray follow-up Pake3 (e.g. retransmit) cannot
// accidentally succeed against stale state.
//
// On a verify failure, the adapter returns a StatusReport with
// GeneralCode=FAILURE so the peer learns the round was rejected
// rather than waiting on MRP retransmits to time out.
func (a *PaseAdapter) ProcessPake3(payload []byte) (opcode uint8, respPayload []byte, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	v := a.verifier
	if v == nil {
		return 0, nil, errors.New("bridge: PaseAdapter Pake3 without preceding Pake1")
	}
	cA, err := spake2.DecodePake3(payload)
	if err != nil {
		// Clear the verifier on decode failure — a retry after any
		// Pake3 rejection MUST start fresh from Pake1. Emit a
		// StatusReport so the commissioner stops retransmitting.
		a.verifier = nil
		body := mrp.EncodeStatusReport(
			mrp.SCStatusGeneralFailure,
			uint32(mrp.SecureChannelProtocolID),
			mrp.SCStatusProtocolInvalidParameter,
			nil,
		)
		return mrp.SCOpcodeStatusReport, body, nil
	}
	if err := v.ProcessPake3(cA); err != nil {
		// Clear the verifier on Pake3 failure so the next Pake1
		// re-starts cleanly. Without this, a retry after Pake3
		// rejection would still see the old (post-Pake1) verifier
		// state on the next Pake3, which is undefined.
		a.verifier = nil
		body := mrp.EncodeStatusReport(
			mrp.SCStatusGeneralFailure,
			uint32(mrp.SecureChannelProtocolID),
			mrp.SCStatusProtocolInvalidParameter,
			nil,
		)
		return mrp.SCOpcodeStatusReport, body, nil
	}
	sharedSecret := v.SharedSecret()
	peerSessionID := a.peerSessionID
	a.verifier = nil
	if a.onEstablished != nil {
		if err := a.onEstablished(sharedSecret, peerSessionID); err != nil {
			return 0, nil, fmt.Errorf("bridge: PASE session pickup: %w", err)
		}
	}
	// Per Matter §4.13.4 step 11 the verifier MUST close the PASE
	// handshake with a StatusReport(SUCCESS, SESSION_ESTABLISHMENT_SUCCESS).
	// chip-tool retransmits Pake3 until it sees this; a bare MRP
	// StandaloneAck is not enough.
	body := mrp.EncodeStatusReport(
		mrp.SCStatusGeneralSuccess,
		uint32(mrp.SecureChannelProtocolID),
		mrp.SCStatusProtocolSessionEstablishmentSuccess,
		nil,
	)
	return mrp.SCOpcodeStatusReport, body, nil
}

// CaseAdapter wraps a [sigma.Responder] in the [CaseHandler] port.
// The responder owns its own state machine — Sigma1 → Sigma2 →
// Sigma3 advance through the responder's internal phase tracking.
//
// Sigma2_Resume (Matter §4.13.2.4) is supported when the Responder
// has been wired with a ResumptionStore via
// [sigma.Responder.SetResumptionStore]: a Sigma1 carrying valid
// resumptionId + initiatorResumeMic triggers a one-round-trip resume
// instead of Full Sigma.
type CaseAdapter struct {
	mu            sync.RWMutex
	responder     *sigma.Responder
	onEstablished CaseSessionEstablished
	// established tracks whether onEstablished has fired for the
	// current responder. Apple Home's MRP layer retransmits Sigma3
	// when our Sigma3-Success StatusReport ACK is in-flight; the
	// retransmit lands in `Responder.ProcessSigma3` which returns nil
	// without re-deriving keys (idempotent path). Without this guard
	// `onEstablished` would also fire on
	// every retransmit — registering the SAME operational session
	// multiple times in `OpenFromSigmaWithID` and confusing Apple's
	// session table. Reset on `SetResponder` so a fresh CASE
	// handshake (post-AddNOC identity swap) gets a clean trigger.
	established bool
}

// CaseSessionEstablished fires after a successful Sigma3 verification.
// keys are the derived I2R/R2I/AttestationChallenge bundle from
// [sigma.Responder.SessionKeys]; peerSessionID is the
// InitiatorSessionID the commissioner sent in Sigma1 — the bridge
// stamps it into outbound Header.SessionID so the peer resolves the
// session in its own table. Implementations typically hand both to
// [operational.Manager.OpenFromSigmaWithID].
type CaseSessionEstablished func(keys sigma.SessionKeys, peerSessionID uint16) error

// NewCaseAdapter wraps r.
func NewCaseAdapter(r *sigma.Responder) *CaseAdapter {
	return &CaseAdapter{responder: r}
}

// SetResponder swaps the underlying sigma responder. Called after
// AddNOC installs a real fabric so subsequent CASE handshakes use the
// persisted NOC + ICAC instead of the empty ephemeral identity the
// daemon boots with.
func (a *CaseAdapter) SetResponder(r *sigma.Responder) {
	a.mu.Lock()
	a.responder = r
	a.established = false
	a.mu.Unlock()
}

func (a *CaseAdapter) snapshotResponder() *sigma.Responder {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.responder
}

// SnapshotResponder is the public read-only accessor for the wrapped
// responder. Callers use it from inside the OnSessionEstablished
// callback to lift state (peer node id, peer session id, …) the
// callback signature does not expose directly.
func (a *CaseAdapter) SnapshotResponder() *sigma.Responder { return a.snapshotResponder() }

// SetOnSessionEstablished installs a callback that fires after a
// successful Sigma3 verification. Pass nil to clear.
func (a *CaseAdapter) SetOnSessionEstablished(cb CaseSessionEstablished) {
	a.onEstablished = cb
}

// ProcessSigma1 hands the wire payload to [sigma.Responder.ProcessSigma1WithResume].
// When the Sigma1 carries a valid resumptionId + initiatorResumeMic pair AND the
// responder has a ResumptionStore wired, the fast Sigma2_Resume path is taken:
// the reply uses opcode SCOpcodeSigma2Resume and the session is established
// immediately — no Sigma3 is expected. Otherwise Full Sigma runs as before.
//
// Mirrors matter.js packages/protocol/src/session/case/CaseServer.ts::#handleSigma1.
func (a *CaseAdapter) ProcessSigma1(payload []byte) (opcode uint8, respPayload []byte, err error) {
	r := a.snapshotResponder()
	if r == nil {
		return 0, nil, errors.New("bridge: CaseAdapter without responder")
	}
	result, err := r.ProcessSigma1WithResume(payload)
	if err != nil {
		// chip CASESession.cpp sends a SecureChannel StatusReport on every
		// Sigma failure so the commissioner learns the round was actively
		// rejected and stops MRP-retransmitting. Without this report Apple
		// retries for ~30 s before logging a timeout.
		body := mrp.EncodeStatusReport(
			mrp.SCStatusGeneralFailure,
			uint32(mrp.SecureChannelProtocolID),
			mrp.SCStatusProtocolInvalidParameter,
			nil,
		)
		return mrp.SCOpcodeStatusReport, body, nil //nolint:nilerr // Sigma1 failure is converted to a StatusReport wire frame; the caller receives nil err by design
	}

	if result.IsResume() {
		// Sigma2_Resume fast path: session is live after this reply.
		// Fire onEstablished now (before sending the reply) so the
		// operational session is registered before the initiator's
		// StatusReport Success arrives. Mirrors matter.js
		// CaseServer.ts line 200-202 (session added before waitForSuccess).
		out := sigma.MarshalSigma2Resume(*result.Sigma2Resume)
		slog.Default().Debug("matter.tx.sigma2resume.wire",
			slog.Int("len", len(out)),
			slog.String("hex_first128", hex.EncodeToString(peekBytes(out, 128))))

		a.mu.Lock()
		firstEstablish := !a.established
		a.established = true
		a.mu.Unlock()
		if firstEstablish && a.onEstablished != nil {
			if err := a.onEstablished(result.ResumeKeys, result.Sigma2Resume.ResponderSessionID); err != nil {
				return 0, nil, fmt.Errorf("bridge: CASE resume session pickup: %w", err)
			}
		}
		return mrp.SCOpcodeSigma2Resume, out, nil
	}

	// Full Sigma path — reset established so the Sigma3 completion
	// fires onEstablished correctly even if a prior resume attempt
	// on this adapter had already set it to true.
	a.mu.Lock()
	a.established = false
	a.mu.Unlock()

	out := result.Sigma2.Marshal()
	slog.Default().Debug("matter.tx.sigma2.wire",
		slog.Int("len", len(out)),
		slog.String("hex_first128", hex.EncodeToString(peekBytes(out, 128))))
	return mrp.SCOpcodeSigma2, out, nil
}

func peekBytes(b []byte, n int) []byte {
	if len(b) <= n {
		return b
	}
	return b[:n]
}

// ProcessSigma3 hands the wire payload to [sigma.Responder.ProcessSigma3].
// On success the optional [CaseAdapter.SetOnSessionEstablished]
// callback fires with the derived [sigma.SessionKeys] and the
// adapter returns a Secure-Channel StatusReport(Success/Success)
// for the caller to ship — Apple Home (and matter.js by extension)
// only starts operational reads on the freshly-established CASE
// session AFTER receiving this success report. Without it Apple
// retransmits Sigma1 every ~30s and the pairing UI hangs at
// "verbinden …".
func (a *CaseAdapter) ProcessSigma3(payload []byte) (opcode uint8, respPayload []byte, err error) {
	r := a.snapshotResponder()
	if r == nil {
		return 0, nil, errors.New("bridge: CaseAdapter without responder")
	}
	if err := r.ProcessSigma3(payload); err != nil {
		// Emit StatusReport(FAILURE, InvalidParameter) so the commissioner
		// terminates the CASE exchange immediately instead of retransmitting
		// Sigma3. Mirrors chip CASESession.cpp error-exit pattern.
		body := mrp.EncodeStatusReport(
			mrp.SCStatusGeneralFailure,
			uint32(mrp.SecureChannelProtocolID),
			mrp.SCStatusProtocolInvalidParameter,
			nil,
		)
		return mrp.SCOpcodeStatusReport, body, nil //nolint:nilerr // Sigma3 failure is converted to a StatusReport wire frame; the caller receives nil err by design
	}
	// onEstablished must fire EXACTLY ONCE per CASE handshake. Apple's
	// MRP-layer retransmits Sigma3 when our StatusReport ACK is
	// in-flight; the Responder treats those as idempotent (success
	// without re-deriving keys), but we must NOT re-register the
	// operational session each time — that double-registers the same
	// session-id in opMgr and confuses Apple's session table.
	a.mu.Lock()
	firstEstablish := !a.established
	a.established = true
	a.mu.Unlock()
	if firstEstablish && a.onEstablished != nil {
		keys, ok := r.SessionKeys()
		if !ok {
			return 0, nil, errors.New("bridge: CASE keys missing after Sigma3 success")
		}
		if err := a.onEstablished(keys, r.PeerSessionID()); err != nil {
			return 0, nil, fmt.Errorf("bridge: CASE session pickup: %w", err)
		}
	}
	// Per Matter Core §4.13.2.3 + matter.js's CaseServer.#generateSigma2:
	// after Sigma3 verifies, the responder ships a Secure-Channel
	// StatusReport(Success/Success) so the commissioner knows the CASE
	// session is live and operational reads may begin. Without this
	// reply Apple Home keeps retransmitting Sigma1 and never sends a
	// single packet on the freshly-allocated session.
	statusReport := mrp.EncodeStatusReport(
		mrp.SCStatusGeneralSuccess,
		uint32(mrp.SecureChannelProtocolID),
		mrp.SCStatusProtocolSessionEstablishmentSuccess,
		nil,
	)
	return mrp.SCOpcodeStatusReport, statusReport, nil
}

// ProcessSigma2Resume handles an inbound Sigma2_Resume (opcode 0x33)
// which would arrive only if the bridge were acting as CASE initiator
// resuming a prior session. The bridge is always a CASE responder, so
// in practice this path is never reached — the bridge SENDS opcode 0x33
// (via ProcessSigma1 on the resume fast path) and does NOT receive it.
// Implemented as a stub that rejects with an error; the commissioner
// (if it ever sends 0x33 in error) falls back to a fresh Sigma1.
func (a *CaseAdapter) ProcessSigma2Resume(_ []byte) (opcode uint8, respPayload []byte, err error) {
	return 0, nil, errors.New("bridge: inbound Sigma2_Resume not supported (bridge is CASE responder)")
}

// MRPAckAdapter wraps an [mrp.AckTracker] in the [AckHandler] port.
// Trivial passthrough — the MRP layer owns the obligation
// bookkeeping; the adapter exists so the bridge can stay decoupled
// from the concrete tracker type (tests substitute a fake).
type MRPAckAdapter struct {
	tracker *mrp.AckTracker
}

// NewMRPAckAdapter wraps t.
func NewMRPAckAdapter(t *mrp.AckTracker) *MRPAckAdapter {
	return &MRPAckAdapter{tracker: t}
}

// Discharge forwards to [mrp.AckTracker.Discharge].
func (a *MRPAckAdapter) Discharge(sessionID, exchangeID uint16) bool {
	if a.tracker == nil {
		return false
	}
	return a.tracker.Discharge(sessionID, exchangeID)
}

// OperationalSessionLookup wraps a session-lookup function from the
// `secure/operational` layer in the [SessionLookup] port the
// receive pipeline depends on. Decouples bridge from the concrete
// `*operational.Manager` type so tests can substitute a fake.
//
// The function shape mirrors [operational.Manager.Get] — passing
// `manager.Get` directly would fail because Get returns
// `(*Entry, error)`. The adapter unwraps to `(*channel.Session, bool)`.
//
// Implements the optional [SessionFabricResolver] when wired with a
// non-nil `fabricFor` closure — Subscribe, ACL checks and any other
// fabric-scoped logic can then resolve `(sessionID → fabricIndex)`.
type OperationalSessionLookup struct {
	get         func(sessionID uint16) (*channel.Session, bool)
	fabricFor   func(sessionID uint16) (uint8, bool)
	subjectFor  func(sessionID uint16) (uint64, []uint32, bool)
	intervalFor func(sessionID uint16, now time.Time) (time.Duration, bool)
}

// SessionFabricResolver is an optional capability a [SessionLookup]
// can implement so the bridge can resolve the operational FabricIndex
// of an active session. Used by Subscribe (for fabric-scoped quotas),
// the ACL access check, and the AdministratorCommissioning admin
// fabric attribute. PASE / pre-fabric sessions yield (0, true).
//
// Returning (_, false) signals "session not known" — callers that
// need a fabric usually fall back to 0 (pre-fabric semantics).
type SessionFabricResolver interface {
	FabricFor(sessionID uint16) (uint8, bool)
}

// SessionRetransmitIntervalResolver is an optional capability a
// [SessionLookup] can implement so the outbound-reliable tracker can
// size its retransmission backoff to the peer's advertised MRP
// session parameters (active/idle interval selected by peer
// activity — matter.js MRP.ts:129 retransmissionIntervalOf). Sessions
// the resolver does not know (session 0 / PASE) return (_, false)
// and the tracker falls back to the spec idle default.
type SessionRetransmitIntervalResolver interface {
	RetransmitBaseInterval(sessionID uint16, now time.Time) (time.Duration, bool)
}

// SessionSubjectResolver is an optional capability a [SessionLookup]
// can implement so the bridge can resolve the requesting peer subject
// (operational NodeID + CASE Authenticated Tags) of an active CASE
// session. Used by the IM dispatcher's ACL gate to evaluate
// per-subject ACEs (Matter §9.10.5.6). Returning (_, _, false) signals
// "session not known" — the ACL gate then matches only ACEs whose
// Subjects list is empty (the fabric-wide wildcard).
type SessionSubjectResolver interface {
	SubjectFor(sessionID uint16) (nodeID uint64, cats []uint32, ok bool)
}

// NewOperationalSessionLookup builds the adapter. The `get` closure
// typically wraps an [operational.Manager.Get] call:
//
//	NewOperationalSessionLookup(func(id uint16) (*channel.Session, bool) {
//	    e, err := mgr.Get(id)
//	    if err != nil {
//	        return nil, false
//	    }
//	    return e.Session, true
//	})
func NewOperationalSessionLookup(get func(sessionID uint16) (*channel.Session, bool)) *OperationalSessionLookup {
	return &OperationalSessionLookup{get: get}
}

// WithFabricResolver wires the optional FabricFor side of the
// adapter. Pass a closure returning `(entry.FabricIndex, true)` for
// known sessions and `(0, false)` for unknown ones. Returns the
// receiver so callers can chain.
func (l *OperationalSessionLookup) WithFabricResolver(fabricFor func(sessionID uint16) (uint8, bool)) *OperationalSessionLookup {
	if l == nil {
		return nil
	}
	l.fabricFor = fabricFor
	return l
}

// Lookup implements [SessionLookup].
func (l *OperationalSessionLookup) Lookup(sessionID uint16) (*channel.Session, bool) {
	if l == nil || l.get == nil {
		return nil, false
	}
	return l.get(sessionID)
}

// FabricFor implements [SessionFabricResolver]. Returns (0, false)
// when the adapter was built without a fabric resolver closure.
func (l *OperationalSessionLookup) FabricFor(sessionID uint16) (uint8, bool) {
	if l == nil || l.fabricFor == nil {
		return 0, false
	}
	return l.fabricFor(sessionID)
}

// WithSubjectResolver wires the optional SubjectFor side of the
// adapter. Pass a closure returning `(peerNodeID, peerCATs, true)`
// for known CASE sessions and `(0, nil, false)` for unknown ones.
// Returns the receiver so callers can chain.
func (l *OperationalSessionLookup) WithSubjectResolver(subjectFor func(sessionID uint16) (uint64, []uint32, bool)) *OperationalSessionLookup {
	if l == nil {
		return nil
	}
	l.subjectFor = subjectFor
	return l
}

// WithRetransmitIntervalResolver wires the optional
// RetransmitBaseInterval side of the adapter. Pass a closure that
// resolves the peer-appropriate MRP base interval (typically
// `operational.Entry.RetransmitBaseInterval`) for known sessions and
// returns `(0, false)` for unknown ones. Returns the receiver so
// callers can chain.
func (l *OperationalSessionLookup) WithRetransmitIntervalResolver(intervalFor func(sessionID uint16, now time.Time) (time.Duration, bool)) *OperationalSessionLookup {
	if l == nil {
		return nil
	}
	l.intervalFor = intervalFor
	return l
}

// RetransmitBaseInterval implements [SessionRetransmitIntervalResolver].
// Returns (0, false) when the adapter was built without an interval
// resolver closure.
func (l *OperationalSessionLookup) RetransmitBaseInterval(sessionID uint16, now time.Time) (time.Duration, bool) {
	if l == nil || l.intervalFor == nil {
		return 0, false
	}
	return l.intervalFor(sessionID, now)
}

// SubjectFor implements [SessionSubjectResolver]. Returns (0, nil,
// false) when the adapter was built without a subject resolver
// closure or the session is unknown.
func (l *OperationalSessionLookup) SubjectFor(sessionID uint16) (nodeID uint64, cats []uint32, ok bool) {
	if l == nil || l.subjectFor == nil {
		return 0, nil, false
	}
	return l.subjectFor(sessionID)
}
