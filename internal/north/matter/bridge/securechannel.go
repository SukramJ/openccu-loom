// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package bridge

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"log/slog"
	"net"
	"strconv"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/matter/diagevent"
	"github.com/SukramJ/openccu-loom/internal/north/matter/secure/channel"
	"github.com/SukramJ/openccu-loom/internal/north/matter/secure/sigma"
	"github.com/SukramJ/openccu-loom/internal/north/matter/secure/spake2"
	"github.com/SukramJ/openccu-loom/internal/north/matter/transport/message"
	"github.com/SukramJ/openccu-loom/internal/north/matter/transport/mrp"
)

// Errors surfaced by the Secure-Channel routing layer. As with the
// IM routing errors, these are logged inside the bridge and dropped
// — the commissioner retries per MRP.
var (
	// ErrPaseHandlerMissing is returned when a PASE opcode arrives
	// but no [PaseHandler] is wired. The bridge still drops the
	// datagram with a debug log so PASE traffic during pre-commissioner
	// boot does not flood the warn channel.
	ErrPaseHandlerMissing = errors.New("securechannel: PASE handler not wired")
	// ErrCaseHandlerMissing is returned when a CASE (Sigma) opcode
	// arrives but no [CaseHandler] is wired.
	ErrCaseHandlerMissing = errors.New("securechannel: CASE handler not wired")
)

// PaseHandler is the inbound PASE port — the Spake2+ verifier the
// `secure/spake2.Verifier` typically backs. Each method takes the
// inbound TLV payload (already stripped of message + protocol
// headers) and returns either a response opcode + payload pair (the
// router ships it back via [Bridge.sendReply]) or an error. A nil
// response with nil error indicates "no reply needed" (e.g. Pake3
// completes the exchange and the bridge's session manager picks up
// the new session in a separate hook).
//
// Method naming mirrors the spec opcode names. The bridge's router
// fans out by opcode → method, so adding a new PASE opcode is a
// one-line addition both here and in the router's switch.
type PaseHandler interface {
	// ProcessPBKDFParamRequest handles the commissioner's PBKDF param
	// request and produces a PBKDFParamResponse (opcode 0x21).
	ProcessPBKDFParamRequest(payload []byte) (uint8, []byte, error)
	// ProcessPake1 handles Pake1 and produces Pake2 (opcode 0x23).
	ProcessPake1(payload []byte) (uint8, []byte, error)
	// ProcessPake3 handles Pake3 and produces no reply (the bridge's
	// operational manager picks up the freshly-derived session keys
	// out-of-band). A non-nil response is allowed — the router ships
	// it if returned, useful for status acks.
	ProcessPake3(payload []byte) (uint8, []byte, error)
}

// CaseHandler is the inbound CASE (Sigma) port — typically backed
// by `secure/sigma.Responder`.
type CaseHandler interface {
	// ProcessSigma1 handles the commissioner's Sigma1 and produces
	// Sigma2 (opcode 0x31).
	ProcessSigma1(payload []byte) (uint8, []byte, error)
	// ProcessSigma3 handles Sigma3 and produces no reply on success
	// (the operational session is registered out-of-band). May
	// return a status ack.
	ProcessSigma3(payload []byte) (uint8, []byte, error)
	// ProcessSigma2Resume handles the resumption variant. Returns
	// either a Sigma2_Resume reply or an error rejecting the
	// resumption (commissioner falls back to full Sigma1).
	ProcessSigma2Resume(payload []byte) (uint8, []byte, error)
}

// AckHandler is the inbound MRP-ack port — the [mrp.AckTracker]
// typically backs it. The router calls Discharge for any datagram
// that carries HasAck=true regardless of opcode, so the IM and
// SecureChannel paths share this hook.
type AckHandler interface {
	// Discharge marks the obligation for the (session, exchange, role)
	// triple as fulfilled (the peer ACKed our outbound message).
	// Exchange IDs are only unique per session AND per side, so both
	// extra dimensions are needed to keep concurrent controllers — and a
	// bridge-opened exchange colliding with a peer-opened one — from
	// discharging each other's obligations. initiator is the LOCAL
	// side's role. Returns whether an obligation existed
	// (informational; the router does not branch on the result).
	Discharge(sessionID, exchangeID uint16, initiator bool) bool
}

// noopPaseHandler is the default when no PASE handler is wired.
type noopPaseHandler struct{}

func (noopPaseHandler) ProcessPBKDFParamRequest([]byte) (opcode uint8, payload []byte, err error) {
	return 0, nil, ErrPaseHandlerMissing
}

func (noopPaseHandler) ProcessPake1([]byte) (opcode uint8, payload []byte, err error) {
	return 0, nil, ErrPaseHandlerMissing
}

func (noopPaseHandler) ProcessPake3([]byte) (opcode uint8, payload []byte, err error) {
	return 0, nil, ErrPaseHandlerMissing
}

// noopCaseHandler is the default when no CASE handler is wired.
type noopCaseHandler struct{}

func (noopCaseHandler) ProcessSigma1([]byte) (opcode uint8, payload []byte, err error) {
	return 0, nil, ErrCaseHandlerMissing
}

func (noopCaseHandler) ProcessSigma3([]byte) (opcode uint8, payload []byte, err error) {
	return 0, nil, ErrCaseHandlerMissing
}

func (noopCaseHandler) ProcessSigma2Resume([]byte) (opcode uint8, payload []byte, err error) {
	return 0, nil, ErrCaseHandlerMissing
}

// noopAckHandler is the default when no ack handler is wired —
// every Discharge returns false (obligation never existed).
type noopAckHandler struct{}

func (noopAckHandler) Discharge(uint16, uint16, bool) bool { return false }

// AttachPaseHandler wires the PASE port. Pass nil to revert to noop.
// Calling this twice replaces the previous handler.
//
// The handler is shared across every PASE exchange — appropriate for
// the v1.1 single-Commissioner-window flow. For concurrent PASE
// dispatch (multiple commissioners pairing in parallel) use
// [Bridge.AttachPaseHandlerProvider] instead; when a provider is
// wired the singleton handler is ignored.
func (b *Bridge) AttachPaseHandler(h PaseHandler) {
	// A fresh acceptor marks a commissioning-window boundary; give the new
	// window its own PASE brute-force budget and a free
	// single-active-PASE slot.
	b.resetPaseFailures()
	b.mu.Lock()
	defer b.mu.Unlock()
	b.paseInFlightExchange = 0
	b.paseInFlightSince = time.Time{}
	if h == nil {
		b.paseHandler = noopPaseHandler{}
		return
	}
	b.paseHandler = h
}

// PaseHandlerProvider returns a [PaseHandler] scoped to a single
// PASE exchange. The bridge's SecureChannel router calls the
// provider once per inbound PASE opcode (PBKDFParamRequest, Pake1,
// Pake3) and dispatches to the returned handler. Returning the same
// handler instance for the same exchangeID across the three opcode
// arrivals lets the underlying [PaseAdapter] track verifier state
// across Pake1 → Pake3.
//
// Returning nil drops the datagram with a debug log — the
// commissioner retries.
type PaseHandlerProvider func(exchangeID uint16) PaseHandler

// AttachPaseHandlerProvider wires a per-exchange PASE provider. When
// set, the provider takes precedence over [Bridge.AttachPaseHandler]
// for every inbound PASE opcode. Pass nil to clear and fall back to
// the singleton handler.
func (b *Bridge) AttachPaseHandlerProvider(p PaseHandlerProvider) {
	// A fresh acceptor marks a commissioning-window boundary; give the new
	// window its own PASE brute-force budget and a free
	// single-active-PASE slot.
	b.resetPaseFailures()
	b.mu.Lock()
	defer b.mu.Unlock()
	b.paseInFlightExchange = 0
	b.paseInFlightSince = time.Time{}
	b.paseProvider = p
}

// resolvePaseHandler picks the right PaseHandler for an inbound
// exchange: the provider wins when set; otherwise the singleton.
// Returns nil only when both the provider returned nil AND no
// singleton was wired — the caller drops the datagram with a debug
// log.
func (b *Bridge) resolvePaseHandler(exchangeID uint16) PaseHandler {
	b.mu.RLock()
	provider := b.paseProvider
	singleton := b.paseHandler
	b.mu.RUnlock()
	if provider != nil {
		if h := provider(exchangeID); h != nil {
			return h
		}
	}
	return singleton
}

// CaseHandlerProvider returns a [CaseHandler] scoped to a single
// CASE exchange. The bridge's SecureChannel router calls the
// provider once per inbound CASE opcode (Sigma1, Sigma3,
// Sigma2Resume) and dispatches to the returned handler. Returning a
// fresh handler per exchange-id is required because Apple Home
// opens parallel CASE sessions from different IPs (iPhone over
// IPv6, HomePod over IPv4) — a singleton CaseAdapter gets stuck in
// `Finished` after the first successful Sigma3 and rejects every
// subsequent Sigma1 with `ProcessSigma1 already called`.
//
// Returning nil drops the datagram with a debug log — the
// commissioner retries.
type CaseHandlerProvider func(exchangeID uint16) CaseHandler

// AttachCaseHandlerProvider wires a per-exchange CASE provider. When
// set, the provider takes precedence over [Bridge.AttachCaseHandler]
// for every inbound CASE opcode. Pass nil to clear and fall back to
// the singleton handler.
func (b *Bridge) AttachCaseHandlerProvider(p CaseHandlerProvider) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.caseProvider = p
}

// resolveCaseHandler picks the right CaseHandler for an inbound
// exchange: the provider wins when set; otherwise the singleton.
// Returns the noop handler when both the provider returned nil AND
// no singleton was wired — the caller surfaces a debug log.
func (b *Bridge) resolveCaseHandler(exchangeID uint16) CaseHandler {
	b.mu.RLock()
	provider := b.caseProvider
	singleton := b.caseHandler
	b.mu.RUnlock()
	if provider != nil {
		if h := provider(exchangeID); h != nil {
			return h
		}
	}
	return singleton
}

// AttachCaseHandler wires the CASE port. Pass nil to revert to noop.
func (b *Bridge) AttachCaseHandler(h CaseHandler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if h == nil {
		b.caseHandler = noopCaseHandler{}
		return
	}
	b.caseHandler = h
}

// AttachAckHandler wires the MRP-ack port. Pass nil to revert to noop.
func (b *Bridge) AttachAckHandler(h AckHandler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if h == nil {
		b.ackHandler = noopAckHandler{}
		return
	}
	b.ackHandler = h
}

// dispatchSecureChannel is the inbound router for ProtocolID==0x0000
// datagrams. Replaces the v1.1.0 logging stub with a full opcode
// fan-out:
//
//   - 0x10 StandaloneAck         → AckHandler.Discharge (no reply)
//   - 0x20 PBKDFParamRequest     → PaseHandler.ProcessPBKDFParamRequest → 0x21 PBKDFParamResponse
//   - 0x22 Pake1                 → PaseHandler.ProcessPake1 → 0x23 Pake2
//   - 0x24 Pake3                 → PaseHandler.ProcessPake3 (no reply or status ack)
//   - 0x30 Sigma1                → CaseHandler.ProcessSigma1 → 0x31 Sigma2
//   - 0x32 Sigma3                → CaseHandler.ProcessSigma3 (no reply or status ack)
//   - 0x33 Sigma2Resume          → CaseHandler.ProcessSigma2Resume → 0x33 reply
//   - 0x40 StatusReport          → CloseSession closes the session;
//     anything else logs (no reply)
//   - everything else (incl. 0x21/0x23/0x25/0x31 = bridge-initiated
//     responses we should never receive on the listener) → drop with
//     debug log.
//
// The router is logged at debug for happy paths and warn for handler
// errors so operators can tail the bridge during commissioner
// pairing without grepping noise.
func (b *Bridge) dispatchSecureChannel(src *net.UDPAddr, requestHdr *message.Header, proto message.ProtocolHeader, payload []byte) error {
	// Per-exchange owe-tracker discharge: when an inbound datagram
	// piggybacks an ACK we use the exchange ID to clear any pending
	// owe — the legacy AckHandler is keyed on exchange, distinct
	// from the outbound-reliable counter tracker handled in
	// receive.go.
	if proto.HasAck {
		b.mu.RLock()
		ack := b.ackHandler
		b.mu.RUnlock()
		if ack != nil {
			ack.Discharge(requestHdr.SessionID, proto.ExchangeID, !proto.Initiator)
		}
	}

	// Remember the authenticated peer address per secure session
	// (Secure-Channel datagrams reach this router only after the
	// decrypt in receive.go succeeds). The graceful CloseSession
	// StatusReport sender routes on it — matter.js keeps the same
	// association on the session itself via its MessageChannel
	// (packages/protocol/src/session/Session.ts, `get channel()`).
	if requestHdr.SessionID != 0 && src != nil {
		b.sessionPeerAddrs.Store(requestHdr.SessionID, src)
	}

	switch proto.Opcode {
	case mrp.StandaloneAckOpcode:
		// Pure StandaloneAck has no payload after the protocol header.
		// The HasAck path above already discharged the obligation;
		// nothing more to do.
		b.logger.Debug("matter.rx.sc.ack",
			slog.String("src", srcString(src)),
			slog.Int("ack_counter", int(proto.AckCounter)))
		return nil

	case mrp.SCOpcodePBKDFParamRequest, mrp.SCOpcodePake1, mrp.SCOpcodePake3:
		if b.paseLockedOut() {
			// Brute-force cap reached for the currently installed
			// acceptor: stop processing PASE entirely until the lockout
			// cooldown expires or a new commissioning window installs a
			// fresh acceptor. matter.js aborts commissioning at
			// PASE_COMMISSIONING_MAX_ERRORS (PaseServer.ts:95-110) and its
			// PaseServer dies with the window; ours can outlive every
			// window, so the refusal has to live on the receive path — and
			// has to expire, or the refusal itself becomes the denial of
			// service.
			b.logger.Warn("matter.rx.sc.pase_locked_out",
				slog.String("src", srcString(src)),
				slog.Int("opcode", int(proto.Opcode)),
				slog.String("hint", "too many failed pairing attempts; PASE re-enables after the lockout period, or immediately when a new pairing window is opened"))
			return nil
		}
		return b.dispatchPase(src, requestHdr, proto, payload)

	case mrp.SCOpcodeSigma1:
		// Apple iOS multicasts the same Sigma1 onto IPv4 + IPv6-LL +
		// IPv6-Global, so we observe 5 identical inbound Sigma1
		// datagrams on the same exchange in <1 ms. Bug A's responder-
		// mutex + equality cache makes every parallel ProcessSigma1
		// return byte-identical Sigma2 bytes, but `handleCase` still
		// calls `sendReply` 5 times — Apple processes the first, sends
		// Sigma3, and rejects our late copies with `CASESession.cpp:
		// 2507: CHIP Error 0x0000002A: Invalid message type` (state 3
		// no longer accepts Sigma2). Dedupe at the router by hashing
		// the Sigma1 payload and short-circuiting replays on the same
		// (exchangeID, sigma1Hash) tuple. Mirrors matter.js's
		// `CaseServer.ts::onSigma1` which discards duplicate Sigma1
		// arrivals on the same exchange via `fabric.locked`.
		h := sha256.Sum256(payload)
		if b.markSigma1Replied(proto.ExchangeID, h) {
			b.logger.Debug("matter.rx.sc.sigma1.replay_dropped",
				slog.String("src", srcString(src)),
				slog.Int("exchange_id", int(proto.ExchangeID)))
			return nil
		}
		ch := b.resolveCaseHandler(proto.ExchangeID)
		return b.handleCase(src, requestHdr, proto, payload, "sigma1",
			ch.ProcessSigma1)
	case mrp.SCOpcodeSigma3:
		ch := b.resolveCaseHandler(proto.ExchangeID)
		if err := b.handleCase(src, requestHdr, proto, payload, "sigma3",
			ch.ProcessSigma3); err != nil {
			return err
		}
		// Sigma3 closes the CASE handshake on the wire — forget the
		// per-exchange Sigma1 dedupe entry so a later exchange-id
		// rollover starts clean.
		b.forgetSigma1Replied(proto.ExchangeID)
		// A completed Sigma3 is the moment a secure session exists. An
		// operator reading the trace after a controller went quiet needs
		// the open as much as the close: "it never came back after 14:02"
		// is only readable when both ends of the session are recorded.
		b.diagRing().Record(diagevent.Event{
			Kind:     diagevent.KindSession,
			Severity: diagevent.SeverityInfo,
			Message:  "A controller completed a secure-session handshake with the bridge.",
			Detail:   map[string]string{"peer": srcString(src)},
		})
		// Sigma3 receive implicitly acks every prior Sigma2 on the same
		// exchange — the commissioner has progressed past Sigma2, so any
		// still-pending Sigma2 retransmits in our outbound-reliable
		// tracker are wasted bandwidth at best and confuse Apple iOS's
		// MRP layer at worst (it drops the late Sigma2 retransmits with
		// `Dropping message without piggyback ack when we are waiting
		// for an ack`, then aborts the CASE handshake). Mirrors
		// matter.js's `MessageExchange.ts::close` which discards every
		// retx-pending message of the exchange when the receiver
		// advances state.
		b.mu.RLock()
		tracker := b.outboundReliable
		b.mu.RUnlock()
		if tracker != nil {
			if cleared := tracker.AbandonExchange(proto.ExchangeID); cleared > 0 {
				b.logger.Debug("matter.sc.sigma3.abandon_prior",
					slog.Int("exchange_id", int(proto.ExchangeID)),
					slog.Int("cleared", cleared))
			}
		}
		return nil
	case mrp.SCOpcodeSigma2Resume:
		ch := b.resolveCaseHandler(proto.ExchangeID)
		return b.handleCase(src, requestHdr, proto, payload, "sigma2_resume",
			ch.ProcessSigma2Resume)

	case mrp.SCOpcodeStatusReport:
		return b.handleSecureChannelStatusReport(src, requestHdr, payload)

	default:
		b.logger.Debug("matter.rx.sc.unhandled",
			slog.String("src", srcString(src)),
			slog.Int("opcode", int(proto.Opcode)),
			slog.Int("payload_bytes", len(payload)))
		return nil
	}
}

// dispatchPase routes one inbound PASE opcode to the handler resolved
// for its exchange. Split out of the Secure-Channel opcode switch so the
// single-active-PASE claim, the handler resolution and the post-Pake3
// release stay together while the switch keeps one entry per family.
func (b *Bridge) dispatchPase(
	src *net.UDPAddr,
	requestHdr *message.Header,
	proto message.ProtocolHeader,
	payload []byte,
) error {
	switch proto.Opcode {
	case mrp.SCOpcodePBKDFParamRequest:
		if !b.claimPaseInFlight(proto.ExchangeID) {
			// Single-active-PASE (Matter §4.13.1): a second
			// commissioner's PBKDFParamRequest while a handshake is in
			// progress is IGNORED — matter.js PaseServer.ts:80-86
			// onNewExchange logs "Pairing already in progress" and
			// drops the exchange. The MRP layer has already acked the
			// datagram; the rejected commissioner times out and
			// retries after the active handshake finished or expired.
			b.logger.Info("matter.rx.sc.pase_busy",
				slog.String("src", srcString(src)),
				slog.Int("exchange_id", int(proto.ExchangeID)))
			b.diagRing().Record(diagevent.Event{
				Kind:     diagevent.KindPairing,
				Severity: diagevent.SeverityWarning,
				Message: "A commissioner tried to pair while another pairing was already " +
					"in progress; its attempt was dropped and it will retry.",
				Detail: map[string]string{"peer": srcString(src)},
			})
			return nil
		}
		h := b.resolvePaseHandler(proto.ExchangeID)
		return b.handlePase(src, requestHdr, proto, payload, "pbkdf_param_req",
			h.ProcessPBKDFParamRequest)
	case mrp.SCOpcodePake1:
		h := b.resolvePaseHandler(proto.ExchangeID)
		return b.handlePase(src, requestHdr, proto, payload, "pake1",
			h.ProcessPake1)
	case mrp.SCOpcodePake3:
		h := b.resolvePaseHandler(proto.ExchangeID)
		err := b.handlePase(src, requestHdr, proto, payload, "pake3",
			h.ProcessPake3)
		// Pake3 terminates the handshake either way (success installs
		// the session, failure aborts the attempt) — release the
		// single-active-PASE claim so the next commissioner can start.
		// matter.js clears #pairingMessenger in the finally block of
		// onNewExchange (PaseServer.ts:112-118).
		b.releasePaseInFlight(proto.ExchangeID)
		return err
	}
	return nil
}

// paseMaxErrors is the number of PASE pairing failures within a
// commissioning window that aborts the window. Mirrors matter.js
// PaseServer.ts PASE_COMMISSIONING_MAX_ERRORS (= 20). Without this cap an
// attacker on the LAN could hammer passcode guesses for the whole window
// — up to 900 s commissioned, or 48 h for an uncommissioned bridge.
const paseMaxErrors = 20

// maxUnsecuredWindows caps the number of per-SourceNodeID duplicate-detection
// windows tracked for unsecured PASE traffic within one commissioning window.
// SourceNodeID is attacker-controlled and unauthenticated at this stage, so
// without a cap a spoofed-source flood grows unsecuredWindows without bound.
// A real commissioning window sees only a handful of commissioners; 256 is far
// above that. Past the cap a new source is treated as fresh — the PASE
// handshake handler's own state-replay guard still rejects a genuine replay.
const maxUnsecuredWindows = 256

// pasePairingTimeout bounds how long a single PASE handshake may hold
// the single-active-PASE claim. Mirrors matter.js PaseServer.ts:33
// PASE_PAIRING_TIMEOUT (60 s) — an abandoned handshake (commissioner
// crashed between PBKDFParamRequest and Pake3) self-expires so pairing
// is not locked out for the rest of the window.
const pasePairingTimeout = 60 * time.Second

// paseLockoutCooldown is how long PASE stays refused after the first time
// [paseMaxErrors] is reached, and [paseLockoutMaxCooldown] is the ceiling
// the doubling backoff runs into.
//
// matter.js and chip both end the refusal with the commissioning window:
// matter.js throws MaximumPasePairingErrorsReachedError and lets the
// PaseServer die with the window (PaseServer.ts:106-110), chip calls
// CommissioningWindowManager::Cleanup once mFailedCommissioningAttempts
// reaches kMaxFailedCommissioningAttempts
// (CommissioningWindowManager.cpp:160-192). Neither can lock pairing out
// beyond the window, because neither keeps a passcode acceptor armed once
// the window closes. This bridge does: an uncommissioned daemon answers
// PASE from its configured passcode with no window open, so the refusal
// needs its own expiry or 20 malformed Pake1s from any LAN host would
// disable pairing until the operator restarts the daemon.
//
// 20 guesses per 15 min (80/h) leaves the ~10^8 valid-passcode space out
// of reach while keeping an operator who mistyped the code waiting
// minutes, not hours — and opening a pairing window clears the lockout
// immediately.
const (
	paseLockoutCooldown    = 15 * time.Minute
	paseLockoutMaxCooldown = 4 * time.Hour
)

// now reads the wall clock through [Bridge.nowFn] so the PASE lockout
// cooldown is testable without sleeping. Production leaves nowFn nil.
func (b *Bridge) now() time.Time {
	if b.nowFn != nil {
		return b.nowFn()
	}
	return time.Now()
}

// claimPaseInFlight attempts to claim the single-active-PASE slot for
// exchangeID. Returns true when the claim succeeds: the slot was idle,
// expired, or already owned by the SAME exchange (PBKDFParamRequest
// retransmit). Mirrors matter.js PaseServer.ts:80-86 onNewExchange.
func (b *Bridge) claimPaseInFlight(exchangeID uint16) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	idle := b.paseInFlightSince.IsZero() || now.Sub(b.paseInFlightSince) > pasePairingTimeout
	if !idle && b.paseInFlightExchange != exchangeID {
		return false
	}
	b.paseInFlightExchange = exchangeID
	b.paseInFlightSince = now
	return true
}

// releasePaseInFlight clears the single-active-PASE claim held by
// exchangeID. A claim held by a DIFFERENT exchange stays untouched.
func (b *Bridge) releasePaseInFlight(exchangeID uint16) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.paseInFlightExchange == exchangeID {
		b.paseInFlightExchange = 0
		b.paseInFlightSince = time.Time{}
	}
}

// recordPaseFailure increments the PASE failure counter and, when it
// reaches [paseMaxErrors], revokes the open commissioning window and
// engages a timed PASE lockout. Mirrors matter.js PaseServer.ts:95-110
// (count → MaximumPasePairingErrorsReachedError) + DeviceCommissioner.ts:70-72
// (tooManyPaseErrors → endCommissioning). Called from the handlePase
// error path for genuine pairing failures only (not missing-handler or
// state-replay retransmits).
func (b *Bridge) recordPaseFailure() {
	if b.paseFailures.Add(1) != paseMaxErrors {
		// Below the cap, or a straggler arriving between the cap and the
		// lockout taking effect. Only the transition to exactly
		// paseMaxErrors engages the lockout, so the revoke + diagnostic
		// fire once per batch.
		return
	}
	cooldown := b.engagePaseLockout()
	win := b.CommissioningWindow()
	b.logger.Warn("matter.rx.sc.pase_bruteforce",
		slog.Int("max_errors", paseMaxErrors),
		slog.Duration("lockout", cooldown),
		slog.String("hint", "too many PASE pairing failures; revoking commissioning window and refusing PASE for the lockout period"))
	b.diagRing().Record(diagevent.Event{
		Kind:     diagevent.KindPairing,
		Severity: diagevent.SeverityError,
		Message: "Pairing was locked after too many failed attempts. It unlocks " +
			"again after the lockout period; opening a new pairing window " +
			"unlocks it immediately.",
		Detail: map[string]string{
			"max_errors":       strconv.Itoa(paseMaxErrors),
			"lockout_duration": cooldown.String(),
		},
	})
	if win != nil {
		_ = win.RevokeWindow(context.Background())
	}
}

// engagePaseLockout refuses PASE for a bounded cooldown and returns the
// cooldown that was applied. Consecutive lockouts without an operator
// intervention double it, up to [paseLockoutMaxCooldown], so a host that
// keeps guessing pays an ever-growing price while a genuine operator
// waits the base cooldown at worst.
//
// The failure counter starts over: from here on the cooldown — not the
// counter — is what keeps PASE closed, and the next budget must be a
// full [paseMaxErrors] once it expires.
func (b *Bridge) engagePaseLockout() time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.paseLockoutStreak++
	cooldown := paseLockoutCooldown
	for range b.paseLockoutStreak - 1 {
		if cooldown >= paseLockoutMaxCooldown {
			break
		}
		cooldown *= 2
	}
	cooldown = min(cooldown, paseLockoutMaxCooldown)
	b.paseLockoutUntil = b.now().Add(cooldown)
	b.paseFailures.Store(0)
	return cooldown
}

// paseLockedOut reports whether PASE is currently refused because the
// brute-force cap fired. Revoking the commissioning window is not enough
// on its own: an uncommissioned bridge keeps a long-lived
// configured-passcode acceptor armed with no window open, so without this
// refusal the guessing continues after the cap.
//
// The refusal expires by itself after the cooldown
// ([Bridge.engagePaseLockout]) — a permanent latch would hand any LAN
// host a pairing kill switch for the daemon's lifetime. Installing a
// fresh acceptor (opening a pairing window) clears it immediately, which
// is the operator-visible way back.
func (b *Bridge) paseLockedOut() bool {
	b.mu.RLock()
	until := b.paseLockoutUntil
	b.mu.RUnlock()
	return !until.IsZero() && b.now().Before(until)
}

// resetPaseFailures clears the per-window PASE state — the failure counter,
// any active lockout and its backoff streak, and the unsecured
// duplicate-detection windows. Called when a fresh PASE acceptor is
// installed ([Bridge.AttachPaseHandler] /
// [Bridge.AttachPaseHandlerProvider]) — a commissioning-window boundary —
// so each window gets its own [paseMaxErrors] budget, an unlocked PASE
// path and a clean set of per-source dedup windows.
func (b *Bridge) resetPaseFailures() {
	b.paseFailures.Store(0)
	b.mu.Lock()
	b.paseLockoutUntil = time.Time{}
	b.paseLockoutStreak = 0
	b.mu.Unlock()
	b.unsecuredWindows.Clear()
	b.unsecuredWindowCount.Store(0)
}

// handlePase + handleCase share the handler-invocation + reply-send
// pattern. Pulled into helpers so the opcode switch stays a flat
// table and the reply-path bookkeeping (errors, log scoping) lives
// in one place per family.
func (b *Bridge) handlePase(
	src *net.UDPAddr,
	requestHdr *message.Header,
	proto message.ProtocolHeader,
	payload []byte,
	stage string,
	process func([]byte) (uint8, []byte, error),
) error {
	// Entry marker BEFORE the handler runs: a stage log with no
	// follow-up (no reply, no pase_err) pins a stall inside the
	// handler itself rather than in the dispatch or send path.
	b.logger.Debug("matter.rx.sc.pase",
		slog.String("stage", stage),
		slog.String("src", srcString(src)),
		slog.Int("exchange_id", int(proto.ExchangeID)))
	respOpcode, respPayload, err := process(payload)
	if err != nil {
		// Missing-handler is a routine pre-PASE-wiring condition
		// (commissioner pings before the daemon's PASE port is up);
		// log at debug so the warn channel stays clean. Real handler
		// errors (decode failures, crypto rejection) come through at
		// warn.
		level := slog.LevelWarn
		isMissing := errors.Is(err, ErrPaseHandlerMissing)
		isStateReplay := errors.Is(err, spake2.ErrSessionState)
		if isMissing {
			level = slog.LevelDebug
		}
		b.logger.Log(context.Background(), level, "matter.rx.sc.pase_err",
			slog.String("stage", stage),
			slog.String("src", srcString(src)),
			slog.String("err", err.Error()))

		// Send a StatusReport on genuine PASE failures so the
		// commissioner learns the exchange is rejected rather than
		// waiting through full MRP retransmission timeouts. Skip for
		// the missing-handler condition (nothing wired yet) and for
		// state-machine replays (the original message was already
		// handled and replied to). Mirrors chip PASESession.cpp
		// error-path StatusReport emission.
		if !isMissing && !isStateReplay {
			// matter.js answers EVERY PASE pairing failure — a
			// wrong-passcode key-confirmation mismatch included — with
			// SecureChannelStatusCode.InvalidParam. NoSharedTrustRoots is
			// a CASE-only code (no fabric in common) and never rides a
			// PASE failure. Mirrors matter.js
			// packages/protocol/src/session/pase/PaseServer.ts:207-212
			// (cancelPairing → sendError(InvalidParam)).
			body := mrp.EncodeStatusReport(
				mrp.SCStatusGeneralFailure,
				uint32(mrp.SecureChannelProtocolID),
				mrp.SCStatusProtocolInvalidParameter,
				nil,
			)
			if sendErr := b.sendReply(src, requestHdr, proto, mrp.SCOpcodeStatusReport, body); sendErr != nil {
				b.logger.Debug("matter.rx.sc.pase_err.status_report_send_failed",
					slog.String("stage", stage),
					slog.String("err", sendErr.Error()))
			}
			// Brute-force protection: a genuine pairing failure (bad Pake1
			// decode, confirmation mismatch, …). Count it and lock PASE
			// once too many accumulate.
			b.recordPaseFailure()
		}
		// The handshake is over: it died before Pake3, so the Pake3
		// branch's release never runs and the single-active-PASE slot
		// would stay held for the full pasePairingTimeout — an operator
		// retrying immediately after a failed attempt would be refused
		// with pase_busy for up to a minute. A state-machine replay is
		// the exception: the original handshake still owns the slot and
		// is still progressing. Mirrors matter.js PaseServer.ts:112-118,
		// where the finally block clears #pairingMessenger on every
		// termination, not only on success.
		if !isStateReplay {
			b.releasePaseInFlight(proto.ExchangeID)
		}
		return err
	}
	if respPayload == nil {
		// Handler completed without producing a wire reply (Pake3
		// happy-path, for instance). Nothing more to do.
		return nil
	}
	// Reliable: PASE continuation messages (PBKDFParamResponse, Pake2)
	// are part of the reliable Secure-Channel exchange. A dropped Pake2
	// aborts commissioning — the commissioner waits for a reply that the
	// bridge, absent MRP tracking, would never rebroadcast. matter.js
	// makes every non-standalone-ack reply reliable (MessageExchange.ts:602);
	// the Sigma3/Pake3 piggyback ack (or a standalone ack) stops the
	// retransmit via the universal inbound Ack path (receive.go).
	if err := b.sendReplyReliable(src, requestHdr, proto, respOpcode, respPayload); err != nil {
		debugReplyError(b.logger, "send_"+stage, src, err)
		return err
	}
	b.dischargeOwedAck(requestHdr.SessionID, proto.ExchangeID, !proto.Initiator)
	return nil
}

func (b *Bridge) handleCase(
	src *net.UDPAddr,
	requestHdr *message.Header,
	proto message.ProtocolHeader,
	payload []byte,
	stage string,
	process func([]byte) (uint8, []byte, error),
) error {
	respOpcode, respPayload, err := process(payload)
	if err != nil {
		// Most "case_err"s on a healthy pair are MRP retransmissions of
		// an already-processed sigma message: the per-exchange responder
		// rejects them with `ErrSessionState` ("ProcessSigma1 already
		// called" / "ProcessSigma1 must run first"), which is by-design
		// — the original sigma was processed, this duplicate is just
		// redundant reliability traffic from the commissioner. Logging
		// those at WARN drowns out genuine failures (handler unwired,
		// payload decode error, signature verify mismatch). Keep WARN
		// for unexpected errors; downgrade the expected-state-rejects
		// and the missing-handler case to DEBUG.
		level := slog.LevelWarn
		isStateReject := errors.Is(err, sigma.ErrSessionState)
		switch {
		case errors.Is(err, ErrCaseHandlerMissing),
			isStateReject:
			level = slog.LevelDebug
		}
		b.logger.Log(context.Background(), level, "matter.rx.sc.case_err",
			slog.String("stage", stage),
			slog.String("src", srcString(src)),
			slog.String("err", err.Error()))

		// Send a StatusReport so the peer learns the exchange is rejected
		// rather than waiting through MRP retransmission timeouts. Skip
		// for state-machine replays (the original Sigma was processed and
		// replied to) and for the missing-handler case (nothing is wired
		// yet). Mirrors chip CASESession.cpp error-path StatusReport
		// emission on Sigma-reject paths.
		if !isStateReject && !errors.Is(err, ErrCaseHandlerMissing) {
			// Map the error to the most appropriate Secure-Channel
			// protocol code. Malformed payloads (decode errors, bad
			// ephemeral key) use InvalidParameter; everything else
			// (unknown destination, signature verify) uses
			// NoSharedTrustRoots which chip sends for any unresolvable
			// fabric identity.
			protocolCode := mrp.SCStatusProtocolNoSharedTrustRoots
			if errors.Is(err, sigma.ErrInvalidPoint) {
				protocolCode = mrp.SCStatusProtocolInvalidParameter
			}
			body := mrp.EncodeStatusReport(
				mrp.SCStatusGeneralFailure,
				uint32(mrp.SecureChannelProtocolID),
				protocolCode,
				nil,
			)
			if sendErr := b.sendReply(src, requestHdr, proto, mrp.SCOpcodeStatusReport, body); sendErr != nil {
				b.logger.Debug("matter.rx.sc.case_err.status_report_send_failed",
					slog.String("stage", stage),
					slog.String("err", sendErr.Error()))
			}
		}
		return err
	}
	if respPayload == nil {
		return nil
	}
	// Reliable: Sigma2 is a CASE continuation message on the reliable
	// Secure-Channel exchange. A dropped Sigma2 aborts CASE — the
	// commissioner waits for a reply the bridge would otherwise never
	// rebroadcast. matter.js makes every non-standalone-ack reply
	// reliable (MessageExchange.ts:602); the Sigma3 piggyback ack stops
	// the retransmit via the universal inbound Ack path (receive.go).
	if err := b.sendReplyReliable(src, requestHdr, proto, respOpcode, respPayload); err != nil {
		debugReplyError(b.logger, "send_"+stage, src, err)
		return err
	}
	b.dischargeOwedAck(requestHdr.SessionID, proto.ExchangeID, !proto.Initiator)
	return nil
}

// markSigma1Replied records that we have produced a Sigma2 reply for
// `(exchangeID, hash)`. Returns true when an earlier Sigma1 arrival on
// the same exchange with the SAME hash was already replied to —
// caller drops the duplicate. Returns false when the entry is fresh or
// the previous hash for the exchange differs (new Sigma1 round).
//
// Hash comparison ensures a Sigma1 retry after a state reset (e.g.
// Apple cancelled the first attempt and started a new one with a
// fresh ephemeral) is not mistaken for a replay.
func (b *Bridge) markSigma1Replied(exchangeID uint16, hash [32]byte) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if existing, ok := b.sigma1Replied[exchangeID]; ok && existing == hash {
		return true
	}
	b.sigma1Replied[exchangeID] = hash
	return false
}

// forgetSigma1Replied removes the dedupe entry for an exchange. Called
// from the Sigma3 receive path: the CASE handshake on this exchange is
// done, and a later exchange-id rollover (uint16 wraps in long-lived
// daemons) must not see a stale hash.
func (b *Bridge) forgetSigma1Replied(exchangeID uint16) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.sigma1Replied, exchangeID)
}

// ForgetSigma1Replied is the exported entry point the daemon wires into
// [PerExchangeCaseProvider.SetOnEvict]. The Sigma3 success path already
// calls [forgetSigma1Replied]; an ABORTED handshake (Sigma1 received,
// Sigma3 never) leaves its dedupe entry orphaned forever because the
// successful-path cleanup never runs. Routing the TTL reaper's eviction
// through here lets a per-exchange provider clean up aborted handshakes
// so the sigma1Replied map cannot grow without bound on a daemon that
// sees many half-completed CASE attempts.
func (b *Bridge) ForgetSigma1Replied(exchangeID uint16) {
	b.forgetSigma1Replied(exchangeID)
}

// ---------------------------------------------------------------------------
// Graceful secure-session teardown (Secure-Channel CloseSession).
// ---------------------------------------------------------------------------

// scStatusProtocolCloseSession is the Secure-Channel StatusReport
// protocol code announcing that the sender will close the current
// session. Verbatim from matter.js packages/types/src/protocol/
// definitions/secure-channel.ts:76 (`CloseSession = 0x0003`).
const scStatusProtocolCloseSession uint16 = 0x0003

// stopSessionCloseBudget caps the total time [Bridge.Stop] spends on
// graceful secure-session teardown. Each per-session notification is a
// single best-effort UDP datagram, so the budget is generous — it
// exists so a wedged wire path can never stall daemon shutdown.
const stopSessionCloseBudget = 2500 * time.Millisecond

// SessionRegistry is the bridge-side seam to the operational session
// manager for session lifecycle actions the Secure-Channel wire path
// initiates: closing a single session on an inbound CloseSession
// StatusReport, and draining every session gracefully at shutdown.
// Backed by `secure/operational.Manager` in the production daemon via
// [Bridge.AttachSessionRegistry]; tests substitute a fake.
//
// loom:reachable:reason="parameter contract of AttachSessionRegistry, which the daemon calls with the operational manager; the call site satisfies the interface implicitly, so the type name itself never appears in production references"
type SessionRegistry interface {
	// Close removes the session and runs the manager's close hooks
	// (subscription cascade). Returns an error when the id is unknown.
	Close(sessionID uint16) error
	// CloseAllGraceful notifies every peer (best-effort CloseSession
	// StatusReport via the registry's graceful-close notifier) and
	// closes all sessions. Notifications stop once deadline passes;
	// local teardown always completes. Returns the sessions closed.
	CloseAllGraceful(deadline time.Time) int
}

// gracefulCloseNotifierSetter is the optional capability a
// [SessionRegistry] implements to receive the bridge's outbound
// CloseSession sender. The operational manager fires it once per
// session before zeroising the keys on graceful teardown — the Go
// translation of matter.js ExchangeManager.ts:635 observing
// session.gracefulClose and shipping the report via #sendCloseSession.
type gracefulCloseNotifierSetter interface {
	SetGracefulCloseNotifier(fn func(sessionID uint16, sess *channel.Session))
}

// reannounceTriggerSetter is the optional capability a
// [SessionRegistry] implements to receive the bridge's mDNS
// broadcast-resume trigger, fired after a reap / eviction leaves a
// peer with zero live sessions (matter.js DeviceAdvertiser.ts:132-149).
type reannounceTriggerSetter interface {
	SetReannounceTrigger(fn func())
}

// AttachSessionRegistry wires the operational session manager into the
// bridge's Secure-Channel path. Beyond storing the registry for the
// inbound-CloseSession and shutdown paths, it self-wires the reverse
// hooks when the registry supports them: the graceful-close notifier
// (outbound CloseSession StatusReport before key zeroise) and the
// mDNS reannounce trigger. Pass nil to detach.
func (b *Bridge) AttachSessionRegistry(reg SessionRegistry) {
	b.mu.Lock()
	b.sessionRegistry = reg
	b.mu.Unlock()
	if reg == nil {
		return
	}
	if setter, ok := reg.(gracefulCloseNotifierSetter); ok {
		setter.SetGracefulCloseNotifier(b.sendCloseSessionReport)
	}
	if setter, ok := reg.(reannounceTriggerSetter); ok {
		setter.SetReannounceTrigger(b.triggerSessionReannounce)
	}
}

// decodeStatusReport splits a Secure-Channel StatusReport body into
// its fixed fields per Matter §4.10.1.1: LE uint16 generalCode ||
// uint32 protocolID || uint16 protocolCode (|| optional protocolData,
// ignored here). Mirrors matter.js SecureChannelStatusMessageSchema
// decode (packages/protocol/src/securechannel/
// SecureChannelStatusMessageSchema.ts). ok=false on a truncated body.
func decodeStatusReport(payload []byte) (generalCode uint16, protocolID uint32, protocolCode uint16, ok bool) {
	if len(payload) < 8 {
		return 0, 0, 0, false
	}
	return binary.LittleEndian.Uint16(payload[0:2]),
		binary.LittleEndian.Uint32(payload[2:6]),
		binary.LittleEndian.Uint16(payload[6:8]),
		true
}

// handleSecureChannelStatusReport services an inbound Secure-Channel
// StatusReport. The only initial StatusReport a peer legitimately
// opens an exchange with is CloseSession — the session it rides on is
// closed immediately (subscription cleanup cascades through the
// registry's close hooks); every other combination is a stray peer
// retransmit and is ignored with a debug log. Mirrors matter.js
// SecureChannelProtocol.ts:54-82 handleInitialStatusReport: non-close
// reports log + close the exchange, CloseSession resolves the session
// and calls session.handlePeerClose (SecureChannelProtocol.ts:81-82
// "Closed by peer").
func (b *Bridge) handleSecureChannelStatusReport(src *net.UDPAddr, requestHdr *message.Header, payload []byte) error {
	generalCode, protocolID, protocolCode, ok := decodeStatusReport(payload)
	isClose := ok &&
		generalCode == mrp.SCStatusGeneralSuccess &&
		protocolID == uint32(mrp.SecureChannelProtocolID) &&
		protocolCode == scStatusProtocolCloseSession
	if !isClose || requestHdr.SessionID == 0 {
		// matter.js ignores unexpected initial StatusReports with a
		// debug log (SecureChannelProtocol.ts:71-77); an unsecured
		// CloseSession is meaningless (session 0 has no keys to drop).
		b.logger.Debug("matter.rx.sc.status_report",
			slog.String("src", srcString(src)),
			slog.Int("session_id", int(requestHdr.SessionID)),
			slog.Int("payload_bytes", len(payload)),
			slog.String("hex", hex.EncodeToString(payload)))
		return nil
	}
	b.sessionPeerAddrs.Delete(requestHdr.SessionID)
	// Every exchange the session carried dies with it, so any timed
	// deadline it registered can never be consumed.
	b.routing.dropSessionTimedDeadlines(requestHdr.SessionID)
	b.mu.RLock()
	reg := b.sessionRegistry
	b.mu.RUnlock()
	if reg == nil {
		b.logger.Debug("matter.rx.sc.close_session.no_registry",
			slog.Int("session_id", int(requestHdr.SessionID)))
		return nil
	}
	if err := reg.Close(requestHdr.SessionID); err != nil {
		// Already gone — racing a local reap / fabric teardown.
		b.logger.Debug("matter.rx.sc.close_session.miss",
			slog.Int("session_id", int(requestHdr.SessionID)),
			slog.String("err", err.Error()))
		return nil
	}
	b.logger.Info("matter.rx.sc.close_session",
		slog.String("src", srcString(src)),
		slog.Int("session_id", int(requestHdr.SessionID)))
	b.diagRing().Record(diagevent.Event{
		Kind:     diagevent.KindSession,
		Severity: diagevent.SeverityInfo,
		Message:  "A controller closed its secure session with the bridge.",
		Detail: map[string]string{
			"peer":       srcString(src),
			"session_id": strconv.Itoa(int(requestHdr.SessionID)),
		},
	})
	return nil
}

// resolveSessionPeerAddr finds the last-known UDP address for a secure
// session so the graceful CloseSession StatusReport can be routed.
// Resolution order:
//
//  1. the authenticated Secure-Channel receive path's per-session
//     record (StandaloneAcks and other SC datagrams),
//  2. the per-subscription routing targets (a subscribed controller —
//     the common Apple Home case — always has one),
//  3. the owed-ack exchange table (a recent reliable IM request).
//
// Returns nil when the peer address was never observed; the caller
// skips the notification (best-effort, matching the try/catch around
// matter.js ExchangeManager.ts:658-666 #sendCloseSession).
func (b *Bridge) resolveSessionPeerAddr(sessionID uint16) *net.UDPAddr {
	if raw, ok := b.sessionPeerAddrs.Load(sessionID); ok {
		if addr, ok := raw.(*net.UDPAddr); ok && addr != nil {
			return addr
		}
	}
	var addr *net.UDPAddr
	b.routing.subTargets.Range(func(_, v any) bool {
		if t, ok := v.(subTarget); ok && t.sessionID == sessionID && t.src != nil {
			addr = t.src
			return false
		}
		return true
	})
	if addr != nil {
		return addr
	}
	b.routing.exchangeSrcs.Range(func(k, v any) bool {
		key, ok := k.(mrp.ExchangeKey)
		if !ok || key.SessionID != sessionID {
			return true
		}
		if t, ok := v.(exchangeReplyTarget); ok && t.src != nil {
			addr = t.src
			return false
		}
		return true
	})
	return addr
}

// sendCloseSessionReport ships a Secure-Channel CloseSession
// StatusReport (GeneralCode=SUCCESS, ProtocolCode=CLOSE_SESSION) to
// the session's peer on a fresh bridge-initiated exchange. Wired as
// the operational manager's graceful-close notifier, so it runs while
// the session keys are still live. Strictly best-effort: any failure
// (unknown peer address, encrypt error, socket error) is logged at
// debug and swallowed — matter.js wraps the same send in a
// try/catch-and-warn (ExchangeManager.ts:658-666 #sendCloseSession).
//
// Wire shape mirrors matter.js SecureChannelMessenger.ts:156-158
// sendCloseSession → #sendStatusReport(Success, CloseSession,
// requiresAck=false): the report is NOT MRP-tracked, the peer does not
// ack a farewell.
func (b *Bridge) sendCloseSessionReport(sessionID uint16, sess *channel.Session) {
	// The session is being torn down whether or not the farewell
	// reaches the peer, so its per-exchange timed deadlines and its
	// learned peer address can never be used again — neither of the
	// early returns below means the session survives. Teardown paths
	// that do not notify the peer fall back to the expiry sweep and to
	// overwrite-on-reuse respectively.
	b.routing.dropSessionTimedDeadlines(sessionID)
	// Deferred, not dropped here: this farewell still has to be routed
	// on that address, and every exit path must release it.
	defer b.sessionPeerAddrs.Delete(sessionID)
	b.mu.RLock()
	listener := b.listener
	b.mu.RUnlock()
	if listener == nil || sess == nil {
		return
	}
	dst := b.resolveSessionPeerAddr(sessionID)
	if dst == nil {
		b.logger.Debug("matter.tx.sc.close_session.no_peer_addr",
			slog.Int("session_id", int(sessionID)))
		return
	}
	proto := message.ProtocolHeader{
		Initiator:  true,
		Opcode:     mrp.SCOpcodeStatusReport,
		ExchangeID: b.nextOutboundExchangeID(),
		ProtocolID: mrp.SecureChannelProtocolID,
		NeedsAck:   false,
	}
	body := append(proto.Marshal(), mrp.EncodeStatusReport(
		mrp.SCStatusGeneralSuccess,
		uint32(mrp.SecureChannelProtocolID),
		scStatusProtocolCloseSession,
		nil,
	)...)
	// Stamp the peer's view of the SessionID so their inbound table
	// resolves the session — see [Bridge.sendReply] for the rationale.
	hdr := message.Header{SessionID: sess.PeerSessionID()}
	if hdr.SessionID == 0 {
		hdr.SessionID = sessionID
	}
	// Deliberately NOT [Bridge.encryptSecureOutbound]: the farewell seals
	// for a session that is being torn down — its manager entry is already
	// gone, and refreshing activity on a dying session would be
	// meaningless at best.
	enc, err := sess.Encrypt(&hdr, securityFlagsByte(&hdr), body)
	if err != nil {
		b.logger.Debug("matter.tx.sc.close_session.encrypt",
			slog.Int("session_id", int(sessionID)),
			slog.String("err", err.Error()))
		return
	}
	datagram := append(hdr.Marshal(), enc.Ciphertext...) //nolint:gocritic // single-allocation join
	if err := listener.Send(dst, datagram); err != nil {
		b.logger.Debug("matter.tx.sc.close_session.send",
			slog.Int("session_id", int(sessionID)),
			slog.String("err", err.Error()))
		return
	}
	b.logger.Debug("matter.tx.sc.close_session",
		slog.String("dst", srcString(dst)),
		slog.Int("session_id", int(sessionID)))
}

// triggerSessionReannounce resumes mDNS broadcast after a session
// teardown left a peer with zero live sessions, so the controller
// rediscovers the bridge and re-establishes CASE instead of showing it
// unresponsive until the next periodic re-announce tick. Mirrors
// matter.js DeviceAdvertiser.ts:132-149 (sessions.deleted →
// serviceDisconnected resumes broadcasting).
//
// Fire-and-forget: the republish runs on its own goroutine bounded by
// the advertise timeout, because the trigger fires from the reaper /
// receive paths which must not block on mDNS probe/announce rounds.
// No-op once the bridge stopped — matter.js shuts the advertiser down
// before removing sessions to prevent exactly this re-announce
// (ServerNetworkRuntime.ts:427).
func (b *Bridge) triggerSessionReannounce() {
	b.mu.RLock()
	started := b.started
	advertiser := b.advertiser
	timeout := b.cfg.AdvertiseTimeout
	b.mu.RUnlock()
	if !started || advertiser == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		// Zeroconf exposes a direct republish trigger; other
		// advertisers degrade to a per-record re-publish, which the
		// [mdns.Advertiser] contract defines as a re-announce.
		if fast, ok := advertiser.(interface{ TriggerReannounce(context.Context) }); ok {
			fast.TriggerReannounce(ctx)
		} else {
			active := advertiser.Active()
			for i := range active {
				if err := advertiser.Publish(ctx, active[i]); err != nil {
					b.logger.Debug("matter.mdns.session_reannounce",
						slog.String("instance", active[i].InstanceName),
						slog.String("err", err.Error()))
				}
			}
		}
		b.logger.Debug("matter.mdns.session_reannounce.done")
	}()
	// An mDNS advertisement change is exactly what an operator needs in
	// the trace when a controller stopped finding the bridge: the
	// re-announce says the records went back on the wire, and its
	// absence says they did not.
	b.diagRing().Record(diagevent.Event{
		Kind:     diagevent.KindDiscovery,
		Severity: diagevent.SeverityInfo,
		Message:  "The bridge re-announced itself on the network after a controller's last session ended.",
	})
}

// closeSecureSessionsForShutdown drains every operational session with
// a best-effort CloseSession StatusReport per peer, bounded by
// [stopSessionCloseBudget]. Called by [Bridge.Stop] BEFORE the UDP
// listener is torn down — without the farewell, controllers keep their
// CASE sessions alive and show the bridge unresponsive for minutes
// after a restart while their retransmits time out. The overall select
// cap guarantees Stop never blocks on the teardown even if the
// registry wedges; the registry additionally enforces the same
// deadline per notification.
func (b *Bridge) closeSecureSessionsForShutdown() {
	b.mu.RLock()
	started := b.started
	reg := b.sessionRegistry
	b.mu.RUnlock()
	if !started || reg == nil {
		return
	}
	deadline := time.Now().Add(stopSessionCloseBudget)
	done := make(chan int, 1)
	go func() { done <- reg.CloseAllGraceful(deadline) }()
	select {
	case n := <-done:
		if n > 0 {
			b.logger.Info("matter.bridge.stop.sessions_closed", slog.Int("count", n))
		}
	case <-time.After(stopSessionCloseBudget):
		b.logger.Warn("matter.bridge.stop.session_close_timeout",
			slog.Duration("budget", stopSessionCloseBudget))
	}
}
