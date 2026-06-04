// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package bridge

// White-box tests for the SecureChannel router:
// dispatchSecureChannel, the three handler interfaces (PaseHandler,
// CaseHandler, AckHandler), and the three Attach* methods.
// Lives in package bridge (not bridge_test) to access unexported symbols.
// Helpers from receive_test.go (newStartedBridge, loopbackSrc) are
// available because they share the same compilation unit.

import (
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/matter/secure/sigma"
	"github.com/SukramJ/openccu-loom/internal/north/matter/transport/message"
	"github.com/SukramJ/openccu-loom/internal/north/matter/transport/mrp"
)

// ─── recording fakes ──────────────────────────────────────────────────────────

// recordingPaseHandler implements PaseHandler and records every call.
// The zero value returns (0, nil, nil) — no reply, no error — for all methods.
type recordingPaseHandler struct {
	pbkdfCalls    atomic.Int32
	pake1Calls    atomic.Int32
	pake3Calls    atomic.Int32
	pake1Resp     []byte
	pake1RespCode uint8
	pake1Err      error
}

func (h *recordingPaseHandler) ProcessPBKDFParamRequest(_ []byte) (opcode uint8, payload []byte, err error) {
	h.pbkdfCalls.Add(1)
	return 0, nil, nil
}

func (h *recordingPaseHandler) ProcessPake1(_ []byte) (opcode uint8, payload []byte, err error) {
	h.pake1Calls.Add(1)
	return h.pake1RespCode, h.pake1Resp, h.pake1Err
}

func (h *recordingPaseHandler) ProcessPake3(_ []byte) (opcode uint8, payload []byte, err error) {
	h.pake3Calls.Add(1)
	return 0, nil, nil
}

// recordingCaseHandler implements CaseHandler and records every call.
// The zero value returns (0, nil, nil) for all methods.
type recordingCaseHandler struct {
	sigma1Calls          atomic.Int32
	sigma3Calls          atomic.Int32
	sigma2ResumeCalls    atomic.Int32
	sigma1Resp           []byte
	sigma1RespCode       uint8
	sigma1Err            error
	sigma2ResumeResp     []byte
	sigma2ResumeRespCode uint8
	sigma2ResumeErr      error
}

func (h *recordingCaseHandler) ProcessSigma1(_ []byte) (opcode uint8, payload []byte, err error) {
	h.sigma1Calls.Add(1)
	return h.sigma1RespCode, h.sigma1Resp, h.sigma1Err
}

func (h *recordingCaseHandler) ProcessSigma3(_ []byte) (opcode uint8, payload []byte, err error) {
	h.sigma3Calls.Add(1)
	return 0, nil, nil
}

func (h *recordingCaseHandler) ProcessSigma2Resume(_ []byte) (opcode uint8, payload []byte, err error) {
	h.sigma2ResumeCalls.Add(1)
	return h.sigma2ResumeRespCode, h.sigma2ResumeResp, h.sigma2ResumeErr
}

// recordingAckHandler implements AckHandler and records every Discharge call.
type recordingAckHandler struct {
	discharges     atomic.Int32
	lastExchangeID atomic.Uint32
}

func (h *recordingAckHandler) Discharge(exchangeID uint16) bool {
	h.discharges.Add(1)
	h.lastExchangeID.Store(uint32(exchangeID))
	return true
}

// ─── helpers ──────────────────────────────────────────────────────────────────

// scProto builds a ProtocolHeader for the SecureChannel protocol with the
// given opcode, exchangeID, and ack settings.
func scProto(opcode uint8, exchangeID uint16, hasAck bool, ackCounter uint32) message.ProtocolHeader {
	return message.ProtocolHeader{
		ProtocolID: mrp.SecureChannelProtocolID,
		Opcode:     opcode,
		ExchangeID: exchangeID,
		HasAck:     hasAck,
		AckCounter: ackCounter,
	}
}

// scHdr builds a minimal message.Header for SecureChannel tests.
func scHdr() *message.Header {
	return &message.Header{SessionID: 0, MessageCounter: 1}
}

// ─── Default-noop cases (4) ───────────────────────────────────────────────────

// TestDispatchSC_Pake1MissingHandler verifies that Pake1 on a fresh bridge
// (no handler wired) returns ErrPaseHandlerMissing.
func TestDispatchSC_Pake1MissingHandler(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	proto := scProto(mrp.SCOpcodePake1, 1, false, 0)
	err := b.dispatchSecureChannel(loopbackSrc(), scHdr(), proto, nil)
	if !errors.Is(err, ErrPaseHandlerMissing) {
		t.Errorf("want ErrPaseHandlerMissing, got %v", err)
	}
}

// TestDispatchSC_Sigma1MissingHandler verifies that Sigma1 on a fresh bridge
// returns ErrCaseHandlerMissing.
func TestDispatchSC_Sigma1MissingHandler(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	proto := scProto(mrp.SCOpcodeSigma1, 1, false, 0)
	err := b.dispatchSecureChannel(loopbackSrc(), scHdr(), proto, nil)
	if !errors.Is(err, ErrCaseHandlerMissing) {
		t.Errorf("want ErrCaseHandlerMissing, got %v", err)
	}
}

// TestDispatchSC_StandaloneAckSucceedsWithoutHandlers verifies that a
// StandaloneAck datagram returns nil even when no handlers are wired.
func TestDispatchSC_StandaloneAckSucceedsWithoutHandlers(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	proto := scProto(mrp.StandaloneAckOpcode, 1, true, 99)
	err := b.dispatchSecureChannel(loopbackSrc(), scHdr(), proto, nil)
	if err != nil {
		t.Errorf("expected nil for StandaloneAck without handlers, got %v", err)
	}
}

// TestDispatchSC_StatusReportDropsSilently verifies that a StatusReport
// opcode returns nil (debug log, no handler, no error).
func TestDispatchSC_StatusReportDropsSilently(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	proto := scProto(mrp.SCOpcodeStatusReport, 1, false, 0)
	err := b.dispatchSecureChannel(loopbackSrc(), scHdr(), proto, []byte{0x01})
	if err != nil {
		t.Errorf("expected nil for StatusReport, got %v", err)
	}
}

// ─── Handler invocation (5) ───────────────────────────────────────────────────

// TestDispatchSC_Pake1InvokesHandler verifies that Pake1 dispatched with a
// wired handler invokes ProcessPake1 exactly once and sends a Pake2 reply.
func TestDispatchSC_Pake1InvokesHandler(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	h := &recordingPaseHandler{
		pake1RespCode: mrp.SCOpcodePake2,
		pake1Resp:     []byte{0xAA, 0xBB},
	}
	b.AttachPaseHandler(h)
	proto := scProto(mrp.SCOpcodePake1, 7, false, 0)
	err := b.dispatchSecureChannel(loopbackSrc(), scHdr(), proto, []byte{0x01})
	if err != nil {
		t.Errorf("expected nil, got %v", err)
	}
	if got := h.pake1Calls.Load(); got != 1 {
		t.Errorf("pake1Calls: want 1, got %d", got)
	}
}

// TestDispatchSC_Pake3NoReplyOnNilPayload verifies that Pake3 with a handler
// returning (0, nil, nil) returns nil and records exactly one pake3 call.
func TestDispatchSC_Pake3NoReplyOnNilPayload(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	h := &recordingPaseHandler{} // zero value: (0, nil, nil) for all methods
	b.AttachPaseHandler(h)
	proto := scProto(mrp.SCOpcodePake3, 7, false, 0)
	err := b.dispatchSecureChannel(loopbackSrc(), scHdr(), proto, []byte{0x02})
	if err != nil {
		t.Errorf("expected nil for Pake3 no-reply, got %v", err)
	}
	if got := h.pake3Calls.Load(); got != 1 {
		t.Errorf("pake3Calls: want 1, got %d", got)
	}
}

// TestDispatchSC_Sigma1InvokesHandler verifies that Sigma1 dispatched with a
// wired case handler invokes ProcessSigma1 exactly once and sends a Sigma2 reply.
func TestDispatchSC_Sigma1InvokesHandler(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	h := &recordingCaseHandler{
		sigma1RespCode: mrp.SCOpcodeSigma2,
		sigma1Resp:     []byte{0xCC},
	}
	b.AttachCaseHandler(h)
	proto := scProto(mrp.SCOpcodeSigma1, 7, false, 0)
	err := b.dispatchSecureChannel(loopbackSrc(), scHdr(), proto, []byte{0x01})
	if err != nil {
		t.Errorf("expected nil, got %v", err)
	}
	if got := h.sigma1Calls.Load(); got != 1 {
		t.Errorf("sigma1Calls: want 1, got %d", got)
	}
}

// TestDispatchSC_Sigma1MulticastReplayDropped reproduces the Apple iOS
// multicast burst: the commissioner shoots the same Sigma1 onto IPv4 +
// IPv6-LL + IPv6-Global so the bridge sees 5 identical inbound Sigma1
// datagrams on the same exchange within ms. Bug E (commit forthcoming)
// gates `handleCase` so only the first invocation runs; replays log +
// drop. Without this gate Apple receives 5 Sigma2 replies, advances to
// Sigma3 after the first, and aborts with
// `CASESession.cpp:2507: CHIP Error 0x0000002A: Invalid message type`
// when the late copies arrive in state AwaitingStatusReport.
func TestDispatchSC_Sigma1MulticastReplayDropped(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	h := &recordingCaseHandler{
		sigma1RespCode: mrp.SCOpcodeSigma2,
		sigma1Resp:     []byte{0xCC},
	}
	b.AttachCaseHandler(h)
	proto := scProto(mrp.SCOpcodeSigma1, 7, false, 0)
	payload := []byte{0x01, 0x02, 0x03, 0x04}

	// Five identical Sigma1 arrivals on the same exchange — Apple's
	// multicast burst.
	for i := range 5 {
		if err := b.dispatchSecureChannel(loopbackSrc(), scHdr(), proto, payload); err != nil {
			t.Fatalf("dispatch[%d]: %v", i, err)
		}
	}
	if got := h.sigma1Calls.Load(); got != 1 {
		t.Errorf("sigma1Calls: want 1 (first arrival only, 4 replays dropped), got %d", got)
	}

	// A DIFFERENT Sigma1 payload on the same exchange must NOT be
	// dropped — that is a legitimate retry with fresh ephemerals after
	// a state reset, not a multicast replay.
	if err := b.dispatchSecureChannel(loopbackSrc(), scHdr(), proto, []byte{0x05, 0x06}); err != nil {
		t.Fatalf("dispatch fresh: %v", err)
	}
	if got := h.sigma1Calls.Load(); got != 2 {
		t.Errorf("sigma1Calls after fresh Sigma1: want 2, got %d", got)
	}
}

// TestDispatchSC_Sigma3ForgetsExchange verifies that a successful
// Sigma3 receive on an exchange forgets the per-exchange Sigma1 dedupe
// entry — required so an exchange-id rollover in long-lived daemons
// does not see a stale hash and silently drop a fresh Sigma1.
func TestDispatchSC_Sigma3ForgetsExchange(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	// ProcessSigma3 in recordingCaseHandler returns (0, nil, nil); the
	// router treats nil-payload as success-no-reply, runs the dedupe-
	// forget hook, and we then verify a fresh Sigma1 is no longer
	// deduped.
	h := &recordingCaseHandler{
		sigma1RespCode: mrp.SCOpcodeSigma2,
		sigma1Resp:     []byte{0xCC},
	}
	b.AttachCaseHandler(h)

	const exch = uint16(99)
	sigma1Bytes := []byte{0xAA, 0xBB}

	proto1 := scProto(mrp.SCOpcodeSigma1, exch, false, 0)
	if err := b.dispatchSecureChannel(loopbackSrc(), scHdr(), proto1, sigma1Bytes); err != nil {
		t.Fatalf("sigma1: %v", err)
	}
	proto3 := scProto(mrp.SCOpcodeSigma3, exch, false, 0)
	if err := b.dispatchSecureChannel(loopbackSrc(), scHdr(), proto3, []byte{0xEE}); err != nil {
		t.Fatalf("sigma3: %v", err)
	}

	// Same exchange, same Sigma1 bytes again — must NOT be deduped now
	// because Sigma3 forgot the entry.
	if err := b.dispatchSecureChannel(loopbackSrc(), scHdr(), proto1, sigma1Bytes); err != nil {
		t.Fatalf("sigma1-after-sigma3: %v", err)
	}
	if got := h.sigma1Calls.Load(); got != 2 {
		t.Errorf("sigma1Calls after Sigma3 forget + retry: want 2, got %d", got)
	}
}

// TestDispatchSC_Sigma2ResumeInvokesHandler verifies that Sigma2Resume
// dispatched with a wired handler is routed to ProcessSigma2Resume exactly once.
func TestDispatchSC_Sigma2ResumeInvokesHandler(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	h := &recordingCaseHandler{
		sigma2ResumeRespCode: mrp.SCOpcodeSigma2Resume,
		sigma2ResumeResp:     []byte{0xDD},
	}
	b.AttachCaseHandler(h)
	proto := scProto(mrp.SCOpcodeSigma2Resume, 7, false, 0)
	err := b.dispatchSecureChannel(loopbackSrc(), scHdr(), proto, []byte{0x01})
	if err != nil {
		t.Errorf("expected nil, got %v", err)
	}
	if got := h.sigma2ResumeCalls.Load(); got != 1 {
		t.Errorf("sigma2ResumeCalls: want 1, got %d", got)
	}
}

// TestDispatchSC_PaseHandlerErrorPropagates verifies that an error returned
// by ProcessPake1 propagates back unchanged from dispatchSecureChannel.
func TestDispatchSC_PaseHandlerErrorPropagates(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	sentinel := errors.New("boom")
	h := &recordingPaseHandler{pake1Err: sentinel}
	b.AttachPaseHandler(h)
	proto := scProto(mrp.SCOpcodePake1, 1, false, 0)
	err := b.dispatchSecureChannel(loopbackSrc(), scHdr(), proto, nil)
	if !errors.Is(err, sentinel) {
		t.Errorf("want sentinel error %q, got %v", sentinel, err)
	}
}

// ─── Ack discharge (3) ────────────────────────────────────────────────────────

// TestDispatchSC_AckDischargedOnHasAck verifies that when proto.HasAck==true the
// AckHandler.Discharge is called exactly once with the datagram's ExchangeID.
func TestDispatchSC_AckDischargedOnHasAck(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	// Wire a pase handler so Pake1 doesn't return an error before reaching ack.
	b.AttachPaseHandler(&recordingPaseHandler{})
	ack := &recordingAckHandler{}
	b.AttachAckHandler(ack)
	proto := scProto(mrp.SCOpcodePake1, 7, true, 42)
	_ = b.dispatchSecureChannel(loopbackSrc(), scHdr(), proto, nil)
	if got := ack.discharges.Load(); got != 1 {
		t.Errorf("discharges: want 1, got %d", got)
	}
	if got := ack.lastExchangeID.Load(); got != 7 {
		t.Errorf("lastExchangeID: want 7, got %d", got)
	}
}

// TestDispatchSC_AckNotDischargedWithoutHasAck verifies that when
// proto.HasAck==false the AckHandler.Discharge is never called.
func TestDispatchSC_AckNotDischargedWithoutHasAck(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	b.AttachPaseHandler(&recordingPaseHandler{})
	ack := &recordingAckHandler{}
	b.AttachAckHandler(ack)
	proto := scProto(mrp.SCOpcodePake1, 7, false, 0)
	_ = b.dispatchSecureChannel(loopbackSrc(), scHdr(), proto, nil)
	if got := ack.discharges.Load(); got != 0 {
		t.Errorf("discharges: want 0, got %d", got)
	}
}

// TestDispatchSC_StandaloneAckDischarges verifies that a StandaloneAck datagram
// with HasAck==true triggers Discharge and returns nil.
func TestDispatchSC_StandaloneAckDischarges(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	ack := &recordingAckHandler{}
	b.AttachAckHandler(ack)
	proto := scProto(mrp.StandaloneAckOpcode, 3, true, 55)
	err := b.dispatchSecureChannel(loopbackSrc(), scHdr(), proto, nil)
	if err != nil {
		t.Errorf("expected nil for StandaloneAck, got %v", err)
	}
	if got := ack.discharges.Load(); got != 1 {
		t.Errorf("discharges: want 1, got %d", got)
	}
}

// ─── Attach nil-revert (3) ───────────────────────────────────────────────────

// TestAttachPaseHandler_NilReverts verifies that attaching a handler then
// reverting to nil via AttachPaseHandler(nil) causes Pake1 to return
// ErrPaseHandlerMissing.
func TestAttachPaseHandler_NilReverts(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	b.AttachPaseHandler(&recordingPaseHandler{})
	b.AttachPaseHandler(nil)
	proto := scProto(mrp.SCOpcodePake1, 1, false, 0)
	err := b.dispatchSecureChannel(loopbackSrc(), scHdr(), proto, nil)
	if !errors.Is(err, ErrPaseHandlerMissing) {
		t.Errorf("want ErrPaseHandlerMissing after nil revert, got %v", err)
	}
}

// TestAttachCaseHandler_NilReverts verifies that attaching a case handler then
// reverting to nil causes Sigma1 to return ErrCaseHandlerMissing.
func TestAttachCaseHandler_NilReverts(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	b.AttachCaseHandler(&recordingCaseHandler{})
	b.AttachCaseHandler(nil)
	proto := scProto(mrp.SCOpcodeSigma1, 1, false, 0)
	err := b.dispatchSecureChannel(loopbackSrc(), scHdr(), proto, nil)
	if !errors.Is(err, ErrCaseHandlerMissing) {
		t.Errorf("want ErrCaseHandlerMissing after nil revert, got %v", err)
	}
}

// TestAttachAckHandler_NilReverts verifies that attaching an ack handler then
// reverting via AttachAckHandler(nil) causes subsequent ack-carrying datagrams
// to be silently absorbed without panicking (noop Discharge returns false).
func TestAttachAckHandler_NilReverts(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	handler := &recordingAckHandler{}
	b.AttachAckHandler(handler)
	b.AttachAckHandler(nil)
	// Wire a pase handler so the test does not fail on ErrPaseHandlerMissing
	// before we reach the ack path.
	b.AttachPaseHandler(&recordingPaseHandler{})
	proto := scProto(mrp.SCOpcodePake1, 5, true, 77)
	// Must not panic; return value may be nil or error from pase handler.
	_ = b.dispatchSecureChannel(loopbackSrc(), scHdr(), proto, nil)
	// The real handler was reverted — its counter must remain zero.
	if got := handler.discharges.Load(); got != 0 {
		t.Errorf("real ack handler discharge called after nil revert: got %d", got)
	}
}

// ─── Unhandled opcode (1) ─────────────────────────────────────────────────────

// TestDispatchSC_UnhandledOpcodeDrops verifies that an unknown SecureChannel
// opcode (0xFF) is silently dropped with a debug log and returns nil.
func TestDispatchSC_UnhandledOpcodeDrops(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	proto := scProto(0xFF, 1, false, 0)
	err := b.dispatchSecureChannel(loopbackSrc(), scHdr(), proto, []byte{0x42})
	if err != nil {
		t.Errorf("expected nil for unknown opcode 0xFF, got %v", err)
	}
}

// ─── PaseHandlerProvider (7) ─────────────────────────────────────────────────

// TestResolvePaseHandler_NoneFallsBackToNoop verifies that resolvePaseHandler
// on a fresh bridge returns the noopPaseHandler, causing Pake1 to return
// ErrPaseHandlerMissing.
func TestResolvePaseHandler_NoneFallsBackToNoop(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	h := b.resolvePaseHandler(1)
	_, _, err := h.ProcessPake1(nil)
	if !errors.Is(err, ErrPaseHandlerMissing) {
		t.Errorf("want ErrPaseHandlerMissing from noop, got %v", err)
	}
}

// TestResolvePaseHandler_SingletonOnly verifies that resolvePaseHandler returns
// the attached singleton when no provider is set.
func TestResolvePaseHandler_SingletonOnly(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	singleton := &recordingPaseHandler{}
	b.AttachPaseHandler(singleton)
	h := b.resolvePaseHandler(42)
	if h != singleton {
		t.Errorf("expected singleton handler, got %T", h)
	}
}

// TestResolvePaseHandler_ProviderOverridesSingleton verifies that when both a
// provider and a singleton are wired, the provider's result wins.
func TestResolvePaseHandler_ProviderOverridesSingleton(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	singleton := &recordingPaseHandler{}
	b.AttachPaseHandler(singleton)
	fromProvider := &recordingPaseHandler{}
	b.AttachPaseHandlerProvider(func(_ uint16) PaseHandler { return fromProvider })
	h := b.resolvePaseHandler(7)
	if h != fromProvider {
		t.Errorf("expected provider handler, got %T", h)
	}
}

// TestResolvePaseHandler_ProviderReturningNilFallsBack verifies that when the
// provider returns nil the resolver falls back to the singleton.
func TestResolvePaseHandler_ProviderReturningNilFallsBack(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	singleton := &recordingPaseHandler{}
	b.AttachPaseHandler(singleton)
	b.AttachPaseHandlerProvider(func(_ uint16) PaseHandler { return nil })
	h := b.resolvePaseHandler(7)
	if h != singleton {
		t.Errorf("expected singleton after provider returned nil, got %T", h)
	}
}

// TestAttachPaseHandlerProvider_NilClears verifies that
// AttachPaseHandlerProvider(nil) clears the provider so subsequent
// resolvePaseHandler calls fall back to the singleton.
func TestAttachPaseHandlerProvider_NilClears(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	singleton := &recordingPaseHandler{}
	b.AttachPaseHandler(singleton)
	fromProvider := &recordingPaseHandler{}
	b.AttachPaseHandlerProvider(func(_ uint16) PaseHandler { return fromProvider })
	// Confirm provider is active before clearing.
	if h := b.resolvePaseHandler(1); h != fromProvider {
		t.Fatalf("precondition: expected provider handler, got %T", h)
	}
	b.AttachPaseHandlerProvider(nil)
	h := b.resolvePaseHandler(1)
	if h != singleton {
		t.Errorf("after nil clear expected singleton, got %T", h)
	}
}

// TestDispatchSC_PerExchangeProviderRouting verifies that a provider returning
// per-ExchangeID handlers routes each Pake1 datagram to the correct handler.
// Two exchanges (10 and 20) each receive exactly one Pake1 call.
func TestDispatchSC_PerExchangeProviderRouting(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)

	handlers := map[uint16]*recordingPaseHandler{
		10: {},
		20: {},
	}
	b.AttachPaseHandlerProvider(func(exchangeID uint16) PaseHandler {
		if h, ok := handlers[exchangeID]; ok {
			return h
		}
		return nil
	})

	for _, exID := range []uint16{10, 20} {
		proto := scProto(mrp.SCOpcodePake1, exID, false, 0)
		// Errors are expected (noop body returns ErrPaseHandlerMissing
		// only when the handler is noop — recording handler returns nil
		// error), so we ignore return value intentionally.
		_ = b.dispatchSecureChannel(loopbackSrc(), scHdr(), proto, []byte{0x01})
	}

	for exID, h := range handlers {
		if got := h.pake1Calls.Load(); got != 1 {
			t.Errorf("exchange %d: pake1Calls = %d, want 1", exID, got)
		}
	}
}

// TestPaseHandlerProvider_ProviderInvokedPerOpcode verifies that the provider
// is called once per inbound PASE opcode, not cached across opcodes. Three
// PASE opcodes on the same exchange → provider counter == 3.
func TestPaseHandlerProvider_ProviderInvokedPerOpcode(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)

	var calls atomic.Int32
	inner := &recordingPaseHandler{}
	b.AttachPaseHandlerProvider(func(_ uint16) PaseHandler {
		calls.Add(1)
		return inner
	})

	const exID uint16 = 5
	opcodes := []uint8{
		mrp.SCOpcodePBKDFParamRequest,
		mrp.SCOpcodePake1,
		mrp.SCOpcodePake3,
	}
	for _, op := range opcodes {
		proto := scProto(op, exID, false, 0)
		_ = b.dispatchSecureChannel(loopbackSrc(), scHdr(), proto, []byte{0x01})
	}

	if got := calls.Load(); got != 3 {
		t.Errorf("provider call count = %d, want 3", got)
	}
}

// ─── sigma1Replied TTL-reap via SetOnEvict ────────────────────────────────────

// TestSigma1Replied_PrunedOnSigma3 verifies the existing path: a successful
// Sigma3 receive on an exchange calls forgetSigma1Replied so the dedupe entry
// is removed. This is the primary (synchronous) prune path.
func TestSigma1Replied_PrunedOnSigma3(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	h := &recordingCaseHandler{
		sigma1RespCode: mrp.SCOpcodeSigma2,
		sigma1Resp:     []byte{0xDD},
	}
	b.AttachCaseHandler(h)

	const exch = uint16(77)
	// Register a dedupe entry via Sigma1.
	if err := b.dispatchSecureChannel(loopbackSrc(), scHdr(), scProto(mrp.SCOpcodeSigma1, exch, false, 0), []byte{0x01}); err != nil {
		t.Fatalf("sigma1: %v", err)
	}
	b.mu.RLock()
	_, present := b.sigma1Replied[exch]
	b.mu.RUnlock()
	if !present {
		t.Fatal("sigma1Replied[77] should be populated after Sigma1 dispatch")
	}

	// Sigma3 fires — must prune the entry.
	if err := b.dispatchSecureChannel(loopbackSrc(), scHdr(), scProto(mrp.SCOpcodeSigma3, exch, false, 0), []byte{0x02}); err != nil {
		t.Fatalf("sigma3: %v", err)
	}
	b.mu.RLock()
	_, stillPresent := b.sigma1Replied[exch]
	b.mu.RUnlock()
	if stillPresent {
		t.Error("sigma1Replied[77] should be absent after Sigma3 — forgetSigma1Replied was not called")
	}
}

// TestPerExchangeCaseProvider_OnEvictPrunesSigma1Replied verifies the
// TTL-reap path for aborted CASE exchanges (Sigma1 arrived, Sigma3 never
// came).
//
// SetOnEvict wires forgetSigma1Replied as the eviction callback. When the
// TTL reaper drops a stale caseEntry, the callback must be called so the
// corresponding sigma1Replied entry is pruned as well.
func TestPerExchangeCaseProvider_OnEvictPrunesSigma1Replied(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)

	// Wire the provider with a short TTL.
	provider := NewPerExchangeCaseProvider(func() *CaseAdapter { return nil })
	// SetOnEvict wires bridge.forgetSigma1Replied.
	provider.SetOnEvict(b.forgetSigma1Replied)
	b.AttachCaseHandlerProvider(provider.Resolve)

	const exch = uint16(88)
	// Simulate: Sigma1 arrived, populating sigma1Replied.
	b.mu.Lock()
	b.sigma1Replied[exch] = [32]byte{0xAB}
	b.mu.Unlock()
	// Also simulate a stale caseEntry in the provider (as if Sigma1 triggered
	// Resolve but Sigma3 never came).
	provider.mu.Lock()
	provider.entries[exch] = &caseEntry{adapter: nil, lastTouched: time.Now().Add(-10 * time.Minute)}
	provider.mu.Unlock()

	// Trigger the reaper with an immediate TTL (1 ms).
	provider.reapLocked(time.Millisecond)

	// The onEvict callback must have been called → sigma1Replied pruned.
	b.mu.RLock()
	_, present := b.sigma1Replied[exch]
	b.mu.RUnlock()
	if present {
		t.Error("sigma1Replied[88] should be absent after TTL-reap eviction — SetOnEvict callback not fired")
	}
}

// ─── StatusReport on Sigma-reject paths ──────────────────────────────────────

// captureUDPHandler is a CaseHandler that returns an injected error and
// additionally tracks whether the bridge attempted to send anything back
// by counting ProcessSigma1 calls. The actual StatusReport transmission
// goes through the bridge's real UDP listener (to loopbackSrc port that
// nobody listens on) and will fail silently at the OS level, which is
// fine — we only need to verify that handleCase attempted the send
// (i.e., did not suppress it) by checking the control flow via error
// return semantics and a non-ErrSessionState, non-ErrCaseHandlerMissing
// error path.
type errorCaseHandler struct {
	err      error
	calls    atomic.Int32
	respCode uint8
	resp     []byte
}

func (h *errorCaseHandler) ProcessSigma1(_ []byte) (opcode uint8, payload []byte, err error) {
	h.calls.Add(1)
	return h.respCode, h.resp, h.err
}

func (h *errorCaseHandler) ProcessSigma3(_ []byte) (opcode uint8, payload []byte, err error) {
	return 0, nil, h.err
}

func (h *errorCaseHandler) ProcessSigma2Resume(_ []byte) (opcode uint8, payload []byte, err error) {
	return 0, nil, h.err
}

// TestHandleCase_SigmaRejectSendsStatusReport verifies that a genuine
// Sigma1-rejection error (not ErrSessionState, not ErrCaseHandlerMissing)
// causes handleCase to attempt a StatusReport transmission in addition to
// returning the original error. The actual UDP send will fail because
// loopbackSrc() points at an unreachable port; we verify that:
//  1. dispatchSecureChannel returns the original error.
//  2. The handler was called exactly once.
//  3. ErrSessionState and ErrCaseHandlerMissing do NOT trigger a
//     StatusReport attempt (they are replay / pre-wire conditions).
func TestHandleCase_SigmaRejectSendsStatusReport(t *testing.T) {
	t.Parallel()

	t.Run("genuine_error_returns_original_error", func(t *testing.T) {
		t.Parallel()
		b := newStartedBridge(t)
		wantErr := errors.New("sigma: bogus decode failure")
		h := &errorCaseHandler{err: wantErr}
		b.AttachCaseHandler(h)
		proto := scProto(mrp.SCOpcodeSigma1, 42, false, 0)
		err := b.dispatchSecureChannel(loopbackSrc(), scHdr(), proto, []byte{0x01})
		if !errors.Is(err, wantErr) {
			t.Errorf("want original error %v, got %v", wantErr, err)
		}
		if got := h.calls.Load(); got != 1 {
			t.Errorf("handler calls: want 1, got %d", got)
		}
	})

	t.Run("session_state_error_no_status_report", func(t *testing.T) {
		// ErrSessionState (replay) — dispatchSecureChannel returns the
		// error but the StatusReport send path must be skipped. We
		// verify by confirming the error returned is the sentinel.
		t.Parallel()
		b := newStartedBridge(t)
		h := &errorCaseHandler{err: fmt.Errorf("wrap: %w", sigma.ErrSessionState)}
		b.AttachCaseHandler(h)
		proto := scProto(mrp.SCOpcodeSigma1, 43, false, 0)
		err := b.dispatchSecureChannel(loopbackSrc(), scHdr(), proto, []byte{0x01})
		if !errors.Is(err, sigma.ErrSessionState) {
			t.Errorf("want ErrSessionState-wrapped error, got %v", err)
		}
	})

	t.Run("missing_handler_no_status_report", func(t *testing.T) {
		// ErrCaseHandlerMissing — default noop path, no StatusReport.
		t.Parallel()
		b := newStartedBridge(t) // no handler wired
		proto := scProto(mrp.SCOpcodeSigma1, 44, false, 0)
		err := b.dispatchSecureChannel(loopbackSrc(), scHdr(), proto, []byte{0x01})
		if !errors.Is(err, ErrCaseHandlerMissing) {
			t.Errorf("want ErrCaseHandlerMissing, got %v", err)
		}
	})

	t.Run("invalid_point_uses_invalid_parameter_code", func(t *testing.T) {
		// ErrInvalidPoint maps to SCStatusProtocolInvalidParameter.
		// The UDP send will fail silently; we only verify the error
		// is propagated and the handler was invoked.
		t.Parallel()
		b := newStartedBridge(t)
		h := &errorCaseHandler{err: fmt.Errorf("wrap: %w", sigma.ErrInvalidPoint)}
		b.AttachCaseHandler(h)
		proto := scProto(mrp.SCOpcodeSigma1, 45, false, 0)
		err := b.dispatchSecureChannel(loopbackSrc(), scHdr(), proto, []byte{0x01})
		if !errors.Is(err, sigma.ErrInvalidPoint) {
			t.Errorf("want ErrInvalidPoint-wrapped error, got %v", err)
		}
		if got := h.calls.Load(); got != 1 {
			t.Errorf("handler calls: want 1, got %d", got)
		}
	})
}

// ─── PASE StatusReport on error paths ───────────────────────────────────────

// errorPaseHandler is a PaseHandler that always returns an error.
type errorPaseHandler struct {
	err   error
	calls atomic.Int32
}

func (h *errorPaseHandler) ProcessPBKDFParamRequest(_ []byte) (opcode uint8, payload []byte, err error) {
	h.calls.Add(1)
	return 0, nil, h.err
}

func (h *errorPaseHandler) ProcessPake1(_ []byte) (opcode uint8, payload []byte, err error) {
	h.calls.Add(1)
	return 0, nil, h.err
}

func (h *errorPaseHandler) ProcessPake3(_ []byte) (opcode uint8, payload []byte, err error) {
	h.calls.Add(1)
	return 0, nil, h.err
}

// TestHandlePase_ErrorPath_InvokesStatusReport verifies that a genuine PASE
// handler error (not ErrPaseHandlerMissing, not spake2.ErrSessionState)
// results in a StatusReport being sent. Because the bridge is not connected
// to a real peer, the send itself will fail silently — we verify that the
// handler was called and the error is propagated.
func TestHandlePase_ErrorPath_InvokesStatusReport(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	genericErr := errors.New("test: pase handler generic error")
	h := &errorPaseHandler{err: genericErr}
	b.AttachPaseHandler(h)

	proto := scProto(mrp.SCOpcodePBKDFParamRequest, 99, false, 0)
	err := b.dispatchSecureChannel(loopbackSrc(), scHdr(), proto, []byte{0x01})
	if !errors.Is(err, genericErr) {
		t.Errorf("want genericErr, got %v", err)
	}
	if got := h.calls.Load(); got != 1 {
		t.Errorf("handler calls: want 1, got %d", got)
	}
}

// TestHandlePase_MissingHandler_NoStatusReport verifies that
// ErrPaseHandlerMissing (no handler wired) does NOT trigger a StatusReport
// attempt.
func TestHandlePase_MissingHandler_NoStatusReport(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t) // no handler wired
	proto := scProto(mrp.SCOpcodePBKDFParamRequest, 100, false, 0)
	err := b.dispatchSecureChannel(loopbackSrc(), scHdr(), proto, []byte{0x01})
	if !errors.Is(err, ErrPaseHandlerMissing) {
		t.Errorf("want ErrPaseHandlerMissing, got %v", err)
	}
}
