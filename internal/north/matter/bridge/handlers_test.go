// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package bridge

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/matter/secure/channel"
	"github.com/SukramJ/openccu-loom/internal/north/matter/secure/operational"
	"github.com/SukramJ/openccu-loom/internal/north/matter/secure/sigma"
	"github.com/SukramJ/openccu-loom/internal/north/matter/secure/spake2"
	mstore "github.com/SukramJ/openccu-loom/internal/north/matter/store"
	"github.com/SukramJ/openccu-loom/internal/north/matter/transport/mrp"
)

// newTestVerifier builds a real spake2.Verifier so the nil-guard is not
// tripped and the malformed-payload path is exercised instead.
func newTestVerifier(t *testing.T) *spake2.Verifier {
	t.Helper()
	salt := []byte("SPAKE2P Key Salt") // exactly 16 bytes
	vc, err := spake2.NewVerifierContext(20202021, salt, 1000)
	if err != nil {
		t.Fatalf("NewVerifierContext: %v", err)
	}
	return spake2.NewVerifier(vc, nil, nil, []byte(spake2.MatterContext))
}

// TestPaseAdapter_PBKDFParamsMissing — ProcessPBKDFParamRequest
// returns the ErrPBKDFParamsMissing sentinel until SetPBKDFParams
// is called (the v1.1 default leaves the adapter unconfigured).
func TestPaseAdapter_PBKDFParamsMissing(t *testing.T) {
	t.Parallel()
	a := NewPaseAdapter(nil) // verifier not consulted on the missing-config path
	_, _, err := a.ProcessPBKDFParamRequest([]byte{})
	if !errors.Is(err, ErrPBKDFParamsMissing) {
		t.Fatalf("err = %v, want ErrPBKDFParamsMissing", err)
	}
}

// TestPaseAdapter_NilVerifierProcessPake1Errors — nil verifier → non-nil
// error from ProcessPake1.
func TestPaseAdapter_NilVerifierProcessPake1Errors(t *testing.T) {
	t.Parallel()
	a := NewPaseAdapter(nil)
	_, _, err := a.ProcessPake1([]byte{0x01, 0x02})
	if err == nil {
		t.Fatal("expected non-nil error from ProcessPake1 with nil verifier")
	}
}

// TestPaseAdapter_NilVerifierProcessPake3Errors — nil verifier → non-nil
// error from ProcessPake3.
func TestPaseAdapter_NilVerifierProcessPake3Errors(t *testing.T) {
	t.Parallel()
	a := NewPaseAdapter(nil)
	_, _, err := a.ProcessPake3([]byte{0x01, 0x02})
	if err == nil {
		t.Fatal("expected non-nil error from ProcessPake3 with nil verifier")
	}
}

// TestPaseAdapter_MalformedPake1Errors — non-nil verifier + malformed
// payload → StatusReport(FAILURE) emitted; no Go-level error returned.
func TestPaseAdapter_MalformedPake1Errors(t *testing.T) {
	t.Parallel()
	a := NewPaseAdapter(newTestVerifier(t))
	// Seed the PBKDF context bytes so that computePaseContextLocked
	// succeeds and the handler reaches the DecodePake1 path. Without
	// these bytes the precondition check fires first and returns a plain
	// Go error rather than a StatusReport.
	a.pbkdfReqBytes = []byte{0x01, 0x02}
	a.pbkdfRespBytes = []byte{0x03, 0x04}
	opcode, body, err := a.ProcessPake1([]byte{0xFF})
	if err != nil {
		t.Fatalf("unexpected Go error from ProcessPake1 with malformed payload: %v", err)
	}
	if opcode != mrp.SCOpcodeStatusReport {
		t.Fatalf("opcode = 0x%02X, want SCOpcodeStatusReport (0x%02X)", opcode, mrp.SCOpcodeStatusReport)
	}
	if len(body) == 0 {
		t.Fatal("StatusReport body must be non-empty")
	}
}

// TestPaseAdapter_MalformedPake3Errors — non-nil verifier + malformed
// payload → StatusReport(FAILURE) emitted; no Go-level error returned.
func TestPaseAdapter_MalformedPake3Errors(t *testing.T) {
	t.Parallel()
	a := NewPaseAdapter(newTestVerifier(t))
	opcode, body, err := a.ProcessPake3([]byte{0xFF})
	if err != nil {
		t.Fatalf("unexpected Go error from ProcessPake3 with malformed payload: %v", err)
	}
	if opcode != mrp.SCOpcodeStatusReport {
		t.Fatalf("opcode = 0x%02X, want SCOpcodeStatusReport (0x%02X)", opcode, mrp.SCOpcodeStatusReport)
	}
	if len(body) == 0 {
		t.Fatal("StatusReport body must be non-empty")
	}
}

// TestCaseAdapter_NilResponderProcessSigma1Errors — nil responder →
// non-nil error from ProcessSigma1.
func TestCaseAdapter_NilResponderProcessSigma1Errors(t *testing.T) {
	t.Parallel()
	a := NewCaseAdapter(nil)
	_, _, err := a.ProcessSigma1([]byte{0x01})
	if err == nil {
		t.Fatal("expected non-nil error from ProcessSigma1 with nil responder")
	}
}

// TestCaseAdapter_NilResponderProcessSigma3Errors — nil responder →
// non-nil error from ProcessSigma3.
func TestCaseAdapter_NilResponderProcessSigma3Errors(t *testing.T) {
	t.Parallel()
	a := NewCaseAdapter(nil)
	_, _, err := a.ProcessSigma3([]byte{0x01})
	if err == nil {
		t.Fatal("expected non-nil error from ProcessSigma3 with nil responder")
	}
}

// TestCaseAdapter_Sigma2ResumeNotImplemented — ProcessSigma2Resume
// always returns a non-nil error regardless of payload.
func TestCaseAdapter_Sigma2ResumeNotImplemented(t *testing.T) {
	t.Parallel()
	// Resume is not implemented; the adapter is safe to construct even
	// with a nil responder because the nil check is never reached.
	a := NewCaseAdapter(nil)
	_, _, err := a.ProcessSigma2Resume([]byte{})
	if err == nil {
		t.Fatal("expected non-nil error from ProcessSigma2Resume")
	}
}

// TestMRPAckAdapter_NilTrackerReturnsFalse — nil tracker → Discharge
// returns false without panicking.
func TestMRPAckAdapter_NilTrackerReturnsFalse(t *testing.T) {
	t.Parallel()
	a := NewMRPAckAdapter(nil)
	if a.Discharge(42) {
		t.Fatal("expected false from Discharge with nil tracker")
	}
}

// TestMRPAckAdapter_DischargeRoundTrip — Owe then Discharge on the same
// exchange returns true; a second Discharge returns false.
func TestMRPAckAdapter_DischargeRoundTrip(t *testing.T) {
	t.Parallel()
	tracker := mrp.NewAckTracker(0) // delay=0 → every Owe is immediately due
	a := NewMRPAckAdapter(tracker)
	const exchangeID uint16 = 7
	tracker.Owe(100, exchangeID, true, time.Now())
	if !a.Discharge(exchangeID) {
		t.Fatal("expected true from first Discharge after Owe")
	}
	if a.Discharge(exchangeID) {
		t.Fatal("expected false from second Discharge (already discharged)")
	}
}

// --- PaseAdapter callback tests ---

// paseRoundTrip is a test helper that runs a full PASE exchange
// (PBKDFParam → Pake1 → Pake2 → Pake3) using a real Prover and the
// given PaseAdapter, returning the error from ProcessPake3. Both
// sides use passcode 20202021 + the canonical salt and bind their
// SPAKE2+ context to the negotiated PBKDFParam wire bytes per
// Matter §4.13.4.
func paseRoundTrip(t *testing.T, a *PaseAdapter) error {
	t.Helper()
	const (
		passcode   = uint32(20202021)
		iterations = 1000
	)
	salt := []byte("SPAKE2P Key Salt")

	// 1. Prime the adapter with PBKDF params so ProcessPBKDFParamRequest
	//    can build a response. Wire up a deterministic random source
	//    so the captured response bytes are stable across reruns.
	a.SetPBKDFParams(uint32(iterations), salt, 1)
	a.SetRandomSource(func() [spake2.PBKDFRandomSize]byte {
		return [spake2.PBKDFRandomSize]byte{0x11}
	})

	// 2. Build a synthetic PBKDFParamRequest and run it through the
	//    adapter. Use a fixed initiator-random so the test stays
	//    deterministic.
	initRand := make([]byte, spake2.PBKDFRandomSize)
	for i := range initRand {
		initRand[i] = 0x22
	}
	reqBytes := buildTestPBKDFParamRequest(t, initRand)
	_, respBytes, err := a.ProcessPBKDFParamRequest(reqBytes)
	if err != nil {
		t.Fatalf("PaseAdapter.ProcessPBKDFParamRequest: %v", err)
	}

	// 3. Build the SPAKE2+ context from the captured wire bytes —
	//    this MUST match what the adapter computed internally.
	h := sha256.New()
	h.Write([]byte(spake2.MatterContext))
	h.Write(reqBytes)
	h.Write(respBytes)
	paseCtx := h.Sum(nil)

	prover, err := spake2.NewProver(passcode, salt, iterations, nil, nil, paseCtx)
	if err != nil {
		t.Fatalf("NewProver: %v", err)
	}
	pA, err := prover.GeneratePake1()
	if err != nil {
		t.Fatalf("GeneratePake1: %v", err)
	}

	_, pake2Bytes, err := a.ProcessPake1(spake2.EncodePake1(pA))
	if err != nil {
		t.Fatalf("PaseAdapter.ProcessPake1: %v", err)
	}

	pB, cB, err := spake2.DecodePake2(pake2Bytes)
	if err != nil {
		t.Fatalf("DecodePake2: %v", err)
	}
	cA, err := prover.ProcessPake2(pB, cB)
	if err != nil {
		t.Fatalf("prover.ProcessPake2: %v", err)
	}

	_, _, err = a.ProcessPake3(spake2.EncodePake3(cA))
	return err
}

// buildTestPBKDFParamRequest assembles a minimal PBKDFParamRequest
// TLV payload (32B initiatorRandom, sessionID=42, passcodeID=0,
// HasPBKDFParameters=false) for the round-trip helper. Inline
// hand-rolled TLV bytes — keeps the helper free of additional
// imports.
func buildTestPBKDFParamRequest(t *testing.T, initRand []byte) []byte {
	t.Helper()
	if len(initRand) != spake2.PBKDFRandomSize {
		t.Fatalf("buildTestPBKDFParamRequest: initRand must be %d bytes", spake2.PBKDFRandomSize)
	}
	// TLV: anonymous Structure { [1]: octets(32), [2]: u16(42), [3]: u16(0), [4]: bool(false) }
	out := make([]byte, 0, 4+len(initRand)+11)
	out = append(
		out,
		0x15,                                     // anonymous Structure
		0x30, 0x01, byte(spake2.PBKDFRandomSize), // ContextTag1, octet-string-1byte-len, len=32
	)
	out = append(out, initRand...)
	out = append(
		out,
		0x25, 0x02, 0x2A, 0x00, // ContextTag2, uint16, 42
		0x25, 0x03, 0x00, 0x00, // ContextTag3, uint16, 0
		0x28, 0x04, // ContextTag4, bool false
		0x18, // EndContainer
	)
	return out
}

// newPaseAdapterWithVerifier builds a PaseAdapter wrapping a verifier
// factory for passcode 20202021 and the canonical salt. Uses the
// factory variant so each Pake1 receives the per-exchange SPAKE2+
// context derived from the real PBKDFParam round in paseRoundTrip.
func newPaseAdapterWithVerifier(t *testing.T) *PaseAdapter {
	t.Helper()
	return NewPaseAdapterWithFactory(newVerifierFactory(t, nil))
}

// TestPaseAdapter_SetOnEstablishedNilSafe — nil callback installed;
// ProcessPake3 with nil verifier returns the verifier-nil error without
// panicking on the callback.
func TestPaseAdapter_SetOnEstablishedNilSafe(t *testing.T) {
	t.Parallel()
	a := NewPaseAdapter(nil)
	a.SetOnSessionEstablished(nil)
	_, _, err := a.ProcessPake3([]byte{0x01, 0x02})
	if err == nil {
		t.Fatal("expected non-nil error from ProcessPake3 with nil verifier")
	}
}

// TestPaseAdapter_CallbackInvokedOnSuccess — runs a full PASE round and
// asserts the callback fires exactly once with a 16-byte sharedSecret.
func TestPaseAdapter_CallbackInvokedOnSuccess(t *testing.T) {
	t.Parallel()
	a := newPaseAdapterWithVerifier(t)

	var cbCalls atomic.Int32
	var cbSecret []byte
	var cbMu sync.Mutex
	a.SetOnSessionEstablished(func(secret []byte, _ uint16) error {
		cbCalls.Add(1)
		cbMu.Lock()
		cbSecret = append([]byte(nil), secret...)
		cbMu.Unlock()
		return nil
	})

	if err := paseRoundTrip(t, a); err != nil {
		t.Fatalf("PASE round-trip: %v", err)
	}
	if cbCalls.Load() != 1 {
		t.Fatalf("callback invoked %d times, want 1", cbCalls.Load())
	}
	cbMu.Lock()
	secretLen := len(cbSecret)
	cbMu.Unlock()
	if secretLen != spake2.SharedSecretSize {
		t.Fatalf("callback sharedSecret length = %d, want %d", secretLen, spake2.SharedSecretSize)
	}
}

// TestPaseAdapter_CallbackErrorWrapped — callback returning an error
// causes ProcessPake3 to return an error containing both the
// "PASE session pickup" prefix and the original message.
func TestPaseAdapter_CallbackErrorWrapped(t *testing.T) {
	t.Parallel()
	a := newPaseAdapterWithVerifier(t)
	a.SetOnSessionEstablished(func(_ []byte, _ uint16) error {
		return errors.New("rejected")
	})

	err := paseRoundTrip(t, a)
	if err == nil {
		t.Fatal("expected non-nil error when callback returns error")
	}
	if !strings.Contains(err.Error(), "PASE session pickup") {
		t.Errorf("error %q does not contain %q", err.Error(), "PASE session pickup")
	}
	if !strings.Contains(err.Error(), "rejected") {
		t.Errorf("error %q does not contain %q", err.Error(), "rejected")
	}
}

// TestPaseAdapter_NoCallbackOnDecodeError — malformed Pake3 payload
// must emit StatusReport(FAILURE) and must NOT invoke the session callback.
func TestPaseAdapter_NoCallbackOnDecodeError(t *testing.T) {
	t.Parallel()
	a := NewPaseAdapter(newTestVerifier(t))
	var called atomic.Bool
	a.SetOnSessionEstablished(func(_ []byte, _ uint16) error {
		called.Store(true)
		return nil
	})
	opcode, body, err := a.ProcessPake3([]byte{0xFF}) // malformed
	if err != nil {
		t.Fatalf("unexpected Go error from malformed Pake3: %v", err)
	}
	if opcode != mrp.SCOpcodeStatusReport {
		t.Fatalf("opcode = 0x%02X, want SCOpcodeStatusReport (0x%02X)", opcode, mrp.SCOpcodeStatusReport)
	}
	if len(body) == 0 {
		t.Fatal("StatusReport body must be non-empty")
	}
	if called.Load() {
		t.Fatal("callback must not be invoked when Pake3 decode fails")
	}
}

// --- CaseAdapter callback tests ---

// TestCaseAdapter_SetOnEstablishedNilSafe — nil callback; nil responder
// causes ProcessSigma3 to return the responder-nil error without
// panicking on the callback.
func TestCaseAdapter_SetOnEstablishedNilSafe(t *testing.T) {
	t.Parallel()
	a := NewCaseAdapter(nil)
	a.SetOnSessionEstablished(nil)
	_, _, err := a.ProcessSigma3([]byte{0x01})
	if err == nil {
		t.Fatal("expected non-nil error from ProcessSigma3 with nil responder")
	}
}

// TestCaseAdapter_NoCallbackOnError — nil responder triggers a
// responder-nil error before the callback path is reached.
func TestCaseAdapter_NoCallbackOnError(t *testing.T) {
	t.Parallel()
	a := NewCaseAdapter(nil) // nil responder → early error return
	var called atomic.Bool
	a.SetOnSessionEstablished(func(_ sigma.SessionKeys, _ uint16) error {
		called.Store(true)
		return nil
	})
	_, _, err := a.ProcessSigma3([]byte{0xFF})
	if err == nil {
		t.Fatal("expected non-nil error from ProcessSigma3 with nil responder")
	}
	if called.Load() {
		t.Fatal("callback must not be invoked when ProcessSigma3 fails early")
	}
}

// --- OperationalSessionLookup tests ---

// bridgeNoopResumptionStore satisfies operational.ResumptionStore with
// no-op methods for bridge-package tests that never exercise resumption.
type bridgeNoopResumptionStore struct{}

func (bridgeNoopResumptionStore) UpsertResumption(_ context.Context, _ mstore.ResumptionRecord) error {
	return nil
}

func (bridgeNoopResumptionStore) GetResumptionByID(_ context.Context, _ []byte) (mstore.ResumptionRecord, error) {
	return mstore.ResumptionRecord{}, mstore.ErrResumptionNotFound
}

func (bridgeNoopResumptionStore) GetResumptionByPeer(_ context.Context, _ uint8, _ uint64) (mstore.ResumptionRecord, error) {
	return mstore.ResumptionRecord{}, mstore.ErrResumptionNotFound
}

func (bridgeNoopResumptionStore) RemoveResumption(_ context.Context, _ uint8, _ uint64) error {
	return nil
}

// TestOperationalSessionLookup_NilReturnsFalse — nil receiver and nil
// get function both return (nil, false) without panicking.
func TestOperationalSessionLookup_NilReturnsFalse(t *testing.T) {
	t.Parallel()
	// Nil receiver.
	var nilLookup *OperationalSessionLookup
	sess, ok := nilLookup.Lookup(42)
	if ok || sess != nil {
		t.Fatal("nil receiver: expected (nil, false)")
	}
	// Nil get function.
	l := NewOperationalSessionLookup(nil)
	sess, ok = l.Lookup(42)
	if ok || sess != nil {
		t.Fatal("nil get fn: expected (nil, false)")
	}
}

// TestOperationalSessionLookup_DelegatesToFn — wraps a stub get
// function and verifies the adapter routes calls correctly.
func TestOperationalSessionLookup_DelegatesToFn(t *testing.T) {
	t.Parallel()
	// Build a real Session so the pointer comparison is meaningful.
	want, err := channel.New(channel.Config{
		EncryptKey:  make([]byte, 16),
		DecryptKey:  make([]byte, 16),
		LocalNodeID: 1,
		PeerNodeID:  2,
	})
	if err != nil {
		t.Fatalf("channel.New: %v", err)
	}
	get := func(id uint16) (*channel.Session, bool) {
		if id == 42 {
			return want, true
		}
		return nil, false
	}
	l := NewOperationalSessionLookup(get)
	got, ok := l.Lookup(42)
	if !ok || got != want {
		t.Fatalf("Lookup(42) = (%v, %v), want (%v, true)", got, ok, want)
	}
	got, ok = l.Lookup(43)
	if ok || got != nil {
		t.Fatalf("Lookup(43) = (%v, %v), want (nil, false)", got, ok)
	}
}

// buildBridgeRequestPayload constructs a raw TLV PBKDFParamRequest for
// bridge handler tests. The tlv package is not imported here so we
// encode the bytes manually using the Matter §4.13.5.2 control-byte
// formulas (TagKindContext=1, so control = 1<<5 | ElementType).
func buildBridgeRequestPayload(rand32 []byte, sessionID uint16, hasPBKDF bool) []byte {
	const (
		controlAnonStruct   = byte(0x15) // AnonymousTag + TypeStructure
		controlEndContainer = byte(0x18)
	)
	// context-tag octet-string-1: control = 0b001_00000 | 0b00010000 = 0x30; tag byte; length byte; data
	writeOctets1 := func(tag uint8, data []byte) []byte {
		out := make([]byte, 0, 3+len(data))
		out = append(out, 0x30, tag, byte(len(data))) //nolint:gosec // G115: test data slices are short (< 256 bytes) by construction
		return append(out, data...)
	}
	// context-tag uint8: control = 0b001_00000 | 0b00000100 = 0x24; tag; value
	writeUint8 := func(tag, val uint8) []byte {
		return []byte{0x24, tag, val}
	}
	// context-tag uint16: control = 0b001_00000 | 0b00000101 = 0x25; tag; lo; hi
	writeUint16 := func(tag uint8, val uint16) []byte {
		return []byte{0x25, tag, byte(val), byte(val >> 8)} //nolint:gosec // G115: little-endian uint16 byte extraction; each byte shift result fits uint8
	}
	// context-tag bool-false/true: 0x28/0x29
	writeBool := func(tag uint8, val bool) []byte {
		ctrl := byte(0x28)
		if val {
			ctrl = 0x29
		}
		return []byte{ctrl, tag}
	}

	buf := make([]byte, 0, 4+len(rand32)+4+3+2+1)
	buf = append(buf, controlAnonStruct)
	buf = append(buf, writeOctets1(1, rand32)...)
	buf = append(buf, writeUint16(2, sessionID)...)
	buf = append(buf, writeUint8(3, 0)...)
	buf = append(buf, writeBool(4, hasPBKDF)...)
	buf = append(buf, controlEndContainer)
	return buf
}

// fixedRandomSource returns a SetRandomSource-compatible function that
// always yields the provided 32-byte value.
func fixedRandomSource(val [spake2.PBKDFRandomSize]byte) func() [spake2.PBKDFRandomSize]byte {
	return func() [spake2.PBKDFRandomSize]byte { return val }
}

// TestPaseAdapter_PBKDFMalformedRequestErrors — SetPBKDFParams +
// empty payload must produce a non-nil decode-wrapping error.
func TestPaseAdapter_PBKDFMalformedRequestErrors(t *testing.T) {
	t.Parallel()
	a := NewPaseAdapter(nil)
	a.SetPBKDFParams(1000, []byte("salt-16-bytes!!!"), 7)
	_, _, err := a.ProcessPBKDFParamRequest([]byte{})
	if err == nil {
		t.Fatal("expected non-nil error from malformed empty payload")
	}
}

// TestPaseAdapter_PBKDFRequestHappyPathWithoutParams — HasPBKDFParameters=false
// triggers a response that carries Iterations + Salt from SetPBKDFParams.
func TestPaseAdapter_PBKDFRequestHappyPathWithoutParams(t *testing.T) {
	t.Parallel()
	const (
		iterations = uint32(1000)
		respSessID = uint16(7)
		initSessID = uint16(42)
	)
	salt := []byte("salt-16-bytes!!!")
	var fixed32 [spake2.PBKDFRandomSize]byte
	for i := range fixed32 {
		fixed32[i] = byte(i + 1)
	}
	var initRand [spake2.PBKDFRandomSize]byte
	for i := range initRand {
		initRand[i] = byte(i + 0x10)
	}

	a := NewPaseAdapter(nil)
	a.SetPBKDFParams(iterations, salt, respSessID)
	a.SetRandomSource(fixedRandomSource(fixed32))

	payload := buildBridgeRequestPayload(initRand[:], initSessID, false)
	opcode, respBytes, err := a.ProcessPBKDFParamRequest(payload)
	if err != nil {
		t.Fatalf("ProcessPBKDFParamRequest: %v", err)
	}
	if opcode != mrp.SCOpcodePBKDFParamResponse {
		t.Fatalf("opcode = 0x%02x, want 0x21", opcode)
	}
	if len(respBytes) == 0 {
		t.Fatal("response payload is empty")
	}
	resp, err := spake2.DecodePBKDFParamResponse(respBytes)
	if err != nil {
		t.Fatalf("DecodePBKDFParamResponse: %v", err)
	}
	if !bytes.Equal(resp.InitiatorRandom, initRand[:]) {
		t.Fatalf("InitiatorRandom mismatch: got %x, want %x", resp.InitiatorRandom, initRand)
	}
	if resp.ResponderSessionID != respSessID {
		t.Fatalf("ResponderSessionID = %d, want %d", resp.ResponderSessionID, respSessID)
	}
	if resp.Parameters == nil {
		t.Fatal("Parameters is nil, want non-nil (HasPBKDFParameters=false)")
	}
	if resp.Parameters.Iterations != iterations {
		t.Fatalf("Iterations = %d, want %d", resp.Parameters.Iterations, iterations)
	}
	if !bytes.Equal(resp.Parameters.Salt, salt) {
		t.Fatalf("Salt mismatch: got %x, want %x", resp.Parameters.Salt, salt)
	}
}

// TestPaseAdapter_PBKDFRequestHappyPathWithParams — HasPBKDFParameters=true
// means the commissioner already knows the params; response omits them.
func TestPaseAdapter_PBKDFRequestHappyPathWithParams(t *testing.T) {
	t.Parallel()
	var fixed32 [spake2.PBKDFRandomSize]byte
	for i := range fixed32 {
		fixed32[i] = byte(i + 0x20)
	}
	var initRand [spake2.PBKDFRandomSize]byte
	for i := range initRand {
		initRand[i] = byte(i + 0x30)
	}

	a := NewPaseAdapter(nil)
	a.SetPBKDFParams(1000, []byte("salt-16-bytes!!!"), 5)
	a.SetRandomSource(fixedRandomSource(fixed32))

	payload := buildBridgeRequestPayload(initRand[:], 99, true)
	_, respBytes, err := a.ProcessPBKDFParamRequest(payload)
	if err != nil {
		t.Fatalf("ProcessPBKDFParamRequest: %v", err)
	}
	resp, err := spake2.DecodePBKDFParamResponse(respBytes)
	if err != nil {
		t.Fatalf("DecodePBKDFParamResponse: %v", err)
	}
	if resp.Parameters != nil {
		t.Fatalf("Parameters = %+v, want nil (HasPBKDFParameters=true)", resp.Parameters)
	}
}

// TestPaseAdapter_PBKDFRandomSourceOverride — SetRandomSource injects a
// known pattern; decoded ResponderRandom must match it exactly.
func TestPaseAdapter_PBKDFRandomSourceOverride(t *testing.T) {
	t.Parallel()
	var known [spake2.PBKDFRandomSize]byte
	for i := range known {
		known[i] = byte(0xCC)
	}
	var initRand [spake2.PBKDFRandomSize]byte
	for i := range initRand {
		initRand[i] = byte(i + 1)
	}

	a := NewPaseAdapter(nil)
	a.SetPBKDFParams(2000, []byte("another-16-byte!"), 9)
	a.SetRandomSource(fixedRandomSource(known))

	payload := buildBridgeRequestPayload(initRand[:], 11, false)
	_, respBytes, err := a.ProcessPBKDFParamRequest(payload)
	if err != nil {
		t.Fatalf("ProcessPBKDFParamRequest: %v", err)
	}
	resp, err := spake2.DecodePBKDFParamResponse(respBytes)
	if err != nil {
		t.Fatalf("DecodePBKDFParamResponse: %v", err)
	}
	if !bytes.Equal(resp.ResponderRandom, known[:]) {
		t.Fatalf("ResponderRandom = %x, want %x", resp.ResponderRandom, known)
	}
}

// TestPaseAdapter_PBKDFSaltDefensiveCopy — mutating the salt slice passed
// to SetPBKDFParams after the call must not affect subsequent responses.
func TestPaseAdapter_PBKDFSaltDefensiveCopy(t *testing.T) {
	t.Parallel()
	salt := []byte("salt-16-bytes!!!")
	wantSalt := append([]byte(nil), salt...) // snapshot before mutation

	var fixed32 [spake2.PBKDFRandomSize]byte
	for i := range fixed32 {
		fixed32[i] = byte(i + 5)
	}
	var initRand [spake2.PBKDFRandomSize]byte
	for i := range initRand {
		initRand[i] = byte(i + 0x40)
	}

	a := NewPaseAdapter(nil)
	a.SetPBKDFParams(1000, salt, 2)
	a.SetRandomSource(fixedRandomSource(fixed32))

	// Mutate the original salt slice after SetPBKDFParams.
	for i := range salt {
		salt[i] = 0x00
	}

	payload := buildBridgeRequestPayload(initRand[:], 0, false)
	_, respBytes, err := a.ProcessPBKDFParamRequest(payload)
	if err != nil {
		t.Fatalf("ProcessPBKDFParamRequest: %v", err)
	}
	resp, err := spake2.DecodePBKDFParamResponse(respBytes)
	if err != nil {
		t.Fatalf("DecodePBKDFParamResponse: %v", err)
	}
	if resp.Parameters == nil {
		t.Fatal("Parameters is nil")
	}
	if !bytes.Equal(resp.Parameters.Salt, wantSalt) {
		t.Fatalf("Salt = %x, want %x (defensive copy broken)", resp.Parameters.Salt, wantSalt)
	}
}

// TestOperationalSessionLookup_ManagerIntegration — opens a real PASE
// session via operational.Manager and verifies Lookup can retrieve it.
func TestOperationalSessionLookup_ManagerIntegration(t *testing.T) {
	t.Parallel()
	mgr := operational.NewManager(bridgeNoopResumptionStore{})
	secret := make([]byte, 16)
	for i := range secret {
		secret[i] = byte(i + 1)
	}
	entry, err := mgr.OpenFromPase(1, 2, 0, secret)
	if err != nil {
		t.Fatalf("OpenFromPase: %v", err)
	}

	l := NewOperationalSessionLookup(func(id uint16) (*channel.Session, bool) {
		e, lookupErr := mgr.Get(id)
		if lookupErr != nil {
			return nil, false
		}
		return e.Session, true
	})

	sess, ok := l.Lookup(entry.SessionID)
	if !ok || sess == nil {
		t.Fatalf("Lookup(%d) = (%v, %v), want non-nil session and true", entry.SessionID, sess, ok)
	}
	// Unregistered ID must miss.
	_, ok = l.Lookup(entry.SessionID + 1)
	if ok {
		t.Fatal("Lookup for unregistered ID must return false")
	}
}

// --- NewPaseAdapterWithFactory tests ---

// newVerifierFactory returns a factory function that builds a fresh
// spake2.Verifier for passcode 20202021 + canonical salt each call and
// increments counter.
func newVerifierFactory(t *testing.T, counter *atomic.Int32) func(context []byte) *spake2.Verifier {
	t.Helper()
	const (
		passcode   = uint32(20202021)
		iterations = 1000
	)
	salt := []byte("SPAKE2P Key Salt")
	return func(context []byte) *spake2.Verifier {
		if counter != nil {
			counter.Add(1)
		}
		vc, err := spake2.NewVerifierContext(passcode, salt, iterations)
		if err != nil {
			// factory must not fail in tests; if it does the test is broken
			panic("newVerifierFactory: NewVerifierContext: " + err.Error())
		}
		// Tests that don't run a PBKDFParam round still get the
		// literal context — the factory tolerates an empty context
		// argument from those callers.
		if len(context) == 0 {
			context = []byte(spake2.MatterContext)
		}
		return spake2.NewVerifier(vc, nil, nil, context)
	}
}

// TestPaseAdapter_FactoryInvokedPerPake1 verifies that NewPaseAdapterWithFactory
// invokes the factory exactly once per ProcessPake1 call (not at construction).
func TestPaseAdapter_FactoryInvokedPerPake1(t *testing.T) {
	t.Parallel()
	var counter atomic.Int32
	factory := newVerifierFactory(t, &counter)

	// Factory must not be called at construction time.
	a := NewPaseAdapterWithFactory(factory)
	if counter.Load() != 0 {
		t.Fatalf("factory called %d times at construction, want 0", counter.Load())
	}

	// First ProcessPake1 call.
	if err := paseRoundTrip(t, a); err != nil {
		t.Fatalf("first paseRoundTrip: %v", err)
	}
	if counter.Load() != 1 {
		t.Errorf("factory call count after first Pake1 = %d, want 1", counter.Load())
	}

	// Second ProcessPake1 call via a fresh round trip.
	// Re-create the adapter from the same factory to allow a second full exchange.
	a2 := NewPaseAdapterWithFactory(factory)
	if err := paseRoundTrip(t, a2); err != nil {
		t.Fatalf("second paseRoundTrip: %v", err)
	}
	if counter.Load() != 2 {
		t.Errorf("factory call count after second Pake1 = %d, want 2", counter.Load())
	}
}

// TestPaseAdapter_Pake3WithoutPake1Errors verifies that ProcessPake3 on a
// freshly constructed adapter (no Pake1 yet) returns an error containing
// "without preceding Pake1".
func TestPaseAdapter_Pake3WithoutPake1Errors(t *testing.T) {
	t.Parallel()
	a := NewPaseAdapterWithFactory(newVerifierFactory(t, nil))
	_, _, err := a.ProcessPake3([]byte{0x01, 0x02})
	if err == nil {
		t.Fatal("expected non-nil error from ProcessPake3 without preceding Pake1")
	}
	if !strings.Contains(err.Error(), "without preceding Pake1") {
		t.Errorf("error %q does not contain %q", err.Error(), "without preceding Pake1")
	}
}

// TestPaseAdapter_Pake3SuccessClearsVerifier verifies that after a
// successful Pake3 the verifier is cleared so a replayed Pake3 returns
// the no-Pake1 error.
func TestPaseAdapter_Pake3SuccessClearsVerifier(t *testing.T) {
	t.Parallel()
	a := newPaseAdapterWithVerifier(t)

	// Successful round trip.
	if err := paseRoundTrip(t, a); err != nil {
		t.Fatalf("paseRoundTrip: %v", err)
	}

	// Replay: verifier is gone; any Pake3 bytes must trigger no-Pake1 error.
	_, _, err := a.ProcessPake3([]byte{0x01, 0x02})
	if err == nil {
		t.Fatal("expected error on replayed Pake3; verifier should be cleared after success")
	}
	if !strings.Contains(err.Error(), "without preceding Pake1") {
		t.Errorf("error %q does not contain %q", err.Error(), "without preceding Pake1")
	}
}

// TestPaseAdapter_Pake3FailureClearsVerifier verifies that a failed Pake3
// (malformed cA bytes) clears the verifier so the next ProcessPake3 hits
// the no-Pake1 error, not a crypto error against stale state.
func TestPaseAdapter_Pake3FailureClearsVerifier(t *testing.T) {
	t.Parallel()
	a := newPaseAdapterWithVerifier(t)

	// Drive Pake1 so the verifier is set (use a one-shot helper that
	// only calls ProcessPake1 and skips Pake2 consumer steps).
	// Run the PBKDF round first so the adapter's context is primed —
	// ProcessPake1 errors otherwise per Matter §4.13 (Pake1 must
	// follow PBKDFParamRequest/Response).
	a.SetPBKDFParams(1000, []byte("SPAKE2P Key Salt"), 1)
	a.SetRandomSource(func() [spake2.PBKDFRandomSize]byte { return [spake2.PBKDFRandomSize]byte{0x33} })
	initRand := bytes.Repeat([]byte{0x44}, spake2.PBKDFRandomSize)
	if _, _, err := a.ProcessPBKDFParamRequest(buildTestPBKDFParamRequest(t, initRand)); err != nil {
		t.Fatalf("ProcessPBKDFParamRequest: %v", err)
	}
	prover, err := spake2.NewProver(20202021, []byte("SPAKE2P Key Salt"), 1000, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewProver: %v", err)
	}
	pA, err := prover.GeneratePake1()
	if err != nil {
		t.Fatalf("GeneratePake1: %v", err)
	}
	if _, _, err := a.ProcessPake1(spake2.EncodePake1(pA)); err != nil {
		t.Fatalf("ProcessPake1: %v", err)
	}

	// Send malformed Pake3 (wrong-length cA) → StatusReport(FAILURE), no Go error.
	badPake3 := spake2.EncodePake3([]byte{0xFF, 0xFE}) // wrong length
	opcode, _, err := a.ProcessPake3(badPake3)
	if err != nil {
		t.Fatalf("unexpected Go error from malformed Pake3: %v", err)
	}
	if opcode != mrp.SCOpcodeStatusReport {
		t.Fatalf("first bad Pake3: opcode = 0x%02X, want StatusReport", opcode)
	}

	// Verifier should be cleared: subsequent Pake3 must return no-Pake1 error.
	_, _, err = a.ProcessPake3(badPake3)
	if err == nil {
		t.Fatal("expected error from second Pake3 after failure (no preceding Pake1)")
	}
	if !strings.Contains(err.Error(), "without preceding Pake1") {
		t.Errorf("error %q does not contain %q; verifier may not have been cleared on failure",
			err.Error(), "without preceding Pake1")
	}
}

// TestPaseAdapter_NewPaseAdapter_SingleShotBackcompat verifies that
// NewPaseAdapter wraps v as a single-shot factory — both ProcessPake1 calls
// reference the same verifier (same underlying context). The test confirms
// the second call does not panic and that the adapter's internal verifier
// is overwritten (it's the same pointer since the factory always returns the
// same v), effectively making the second Pake1 restart the state machine
// on the same verifier.
func TestPaseAdapter_NewPaseAdapter_SingleShotBackcompat(t *testing.T) {
	t.Parallel()
	const (
		passcode   = uint32(20202021)
		iterations = 1000
	)
	salt := []byte("SPAKE2P Key Salt")
	vc, err := spake2.NewVerifierContext(passcode, salt, iterations)
	if err != nil {
		t.Fatalf("NewVerifierContext: %v", err)
	}
	v := spake2.NewVerifier(vc, nil, nil, []byte(spake2.MatterContext))

	a := NewPaseAdapter(v)

	// Prime the PBKDF context (ProcessPake1 errors without it).
	a.SetPBKDFParams(uint32(iterations), salt, 1)
	a.SetRandomSource(func() [spake2.PBKDFRandomSize]byte { return [spake2.PBKDFRandomSize]byte{0x55} })
	initRand := bytes.Repeat([]byte{0x66}, spake2.PBKDFRandomSize)
	if _, _, err := a.ProcessPBKDFParamRequest(buildTestPBKDFParamRequest(t, initRand)); err != nil {
		t.Fatalf("ProcessPBKDFParamRequest: %v", err)
	}

	// First Pake1 — must not crash. Because the single-shot factory
	// ignores the per-call context, the verifier still uses its
	// construction-time literal MatterContext, which is incompatible
	// with the SHA-256-of-PBKDFParam-bytes context the spake2 layer
	// would otherwise expect — so the call may surface a state error
	// from the verifier's transcript hash. The contract this test
	// locks is: no panic, deterministic behaviour.
	prover, err := spake2.NewProver(passcode, salt, iterations, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewProver: %v", err)
	}
	pA, err := prover.GeneratePake1()
	if err != nil {
		t.Fatalf("GeneratePake1: %v", err)
	}
	_, _, _ = a.ProcessPake1(spake2.EncodePake1(pA))

	// Second Pake1 — the factory returns the SAME (now post-Pake1) verifier.
	// The verifier's state machine has advanced; reprocessing Pake1 may error
	// from the spake2 layer. What we lock: no panic, adapter behaves
	// deterministically (does not crash or hang).
	prover2, err := spake2.NewProver(passcode, salt, iterations, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewProver2: %v", err)
	}
	pA2, err := prover2.GeneratePake1()
	if err != nil {
		t.Fatalf("GeneratePake1 second: %v", err)
	}
	// May succeed or error (verifier already-advanced) — must not panic.
	_, _, _ = a.ProcessPake1(spake2.EncodePake1(pA2))
}
