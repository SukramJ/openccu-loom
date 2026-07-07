// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package bridge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"net"
	"time"

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
	// Discharge marks the obligation for the (session, exchange)
	// pair as fulfilled (the peer ACKed our outbound message).
	// Exchange IDs are only unique per session, so the session half
	// keeps concurrent controllers from discharging each other's
	// obligations. Returns whether an obligation existed
	// (informational; the router does not branch on the result).
	Discharge(sessionID, exchangeID uint16) bool
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

func (noopAckHandler) Discharge(uint16, uint16) bool { return false }

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
//   - 0x40 StatusReport          → log (no reply)
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
			ack.Discharge(requestHdr.SessionID, proto.ExchangeID)
		}
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
		b.logger.Debug("matter.rx.sc.status_report",
			slog.String("src", srcString(src)),
			slog.Int("payload_bytes", len(payload)),
			slog.String("hex", hex.EncodeToString(payload)))
		return nil

	default:
		b.logger.Debug("matter.rx.sc.unhandled",
			slog.String("src", srcString(src)),
			slog.Int("opcode", int(proto.Opcode)),
			slog.Int("payload_bytes", len(payload)))
		return nil
	}
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
// resets the counter. Mirrors matter.js PaseServer.ts:95-110 (count →
// MaximumPasePairingErrorsReachedError) + DeviceCommissioner.ts:70-72
// (tooManyPaseErrors → endCommissioning). Called from the handlePase
// error path for genuine pairing failures only (not missing-handler or
// state-replay retransmits).
func (b *Bridge) recordPaseFailure() {
	if b.paseFailures.Add(1) < paseMaxErrors {
		return
	}
	b.paseFailures.Store(0)
	win := b.CommissioningWindow()
	if win == nil {
		return
	}
	b.logger.Warn("matter.rx.sc.pase_bruteforce",
		slog.Int("max_errors", paseMaxErrors),
		slog.String("hint", "too many PASE pairing failures; revoking commissioning window"))
	_ = win.RevokeWindow(context.Background())
}

// resetPaseFailures clears the per-window PASE state — the failure counter
// and the unsecured duplicate-detection windows. Called when a fresh PASE
// acceptor is installed ([Bridge.AttachPaseHandler] /
// [Bridge.AttachPaseHandlerProvider]) — a commissioning-window boundary —
// so each window gets its own [paseMaxErrors] budget and a clean set of
// per-source dedup windows.
func (b *Bridge) resetPaseFailures() {
	b.paseFailures.Store(0)
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
			// decode, confirmation mismatch, …). Count it and abort the
			// window once too many accumulate.
			b.recordPaseFailure()
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
	b.dischargeOwedAck(requestHdr.SessionID, proto.ExchangeID)
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
	b.dischargeOwedAck(requestHdr.SessionID, proto.ExchangeID)
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
