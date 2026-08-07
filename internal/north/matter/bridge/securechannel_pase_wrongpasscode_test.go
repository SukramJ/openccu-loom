// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package bridge

// White-box reproducers for the wrong-passcode PASE brute-force gap: a
// Pake3 key-confirmation mismatch (the on-wire symptom of a wrong
// setup passcode) MUST count toward the PASE brute-force cap, exactly
// as matter.js counts every PASE pairing failure toward
// PASE_COMMISSIONING_MAX_ERRORS (packages/protocol/src/session/pase/
// PaseServer.ts:94-95,178-181). Previously the PaseAdapter swallowed
// the confirmation error into its own StatusReport and returned a nil
// Go error, so handlePase's recordPaseFailure path never ran — leaving
// an open commissioning window open to unlimited passcode guessing.
//
// Lives in package bridge (not bridge_test) so it can reach the
// unexported PaseAdapter internals, dispatchSecureChannel, and the
// paseFailures counter directly. Reuses helpers from handlers_test.go
// (newVerifierFactory, buildTestPBKDFParamRequest, paseRoundTrip),
// securechannel_bruteforce_test.go (openedWindow), securechannel_test.go
// (scProto, scHdr), and receive_test.go (newStartedBridge, loopbackSrc).

import (
	"bytes"
	"encoding/binary"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/wire"
	"github.com/SukramJ/openccu-loom/internal/north/matter/secure/spake2"
	"github.com/SukramJ/openccu-loom/internal/north/matter/transport/message"
	"github.com/SukramJ/openccu-loom/internal/north/matter/transport/mrp"
)

const (
	wrongPasscodeTestPasscode   = uint32(20202021)
	wrongPasscodeTestIterations = 1000
)

func wrongPasscodeTestSalt() []byte { return []byte("SPAKE2P Key Salt") }

// primePaseAdapterToPake1 drives a real PASE handshake up to and
// including Pake1 (PBKDFParamRequest → Pake1) so the adapter's verifier
// sits in the post-Pake1 state, ready to verify a Pake3. The prover's
// pA is a valid curve point (context-independent), so ProcessPake1
// accepts it; the caller supplies its own — genuine or tampered — cA to
// the following ProcessPake3.
func primePaseAdapterToPake1(t *testing.T, a *PaseAdapter) {
	t.Helper()
	salt := wrongPasscodeTestSalt()
	a.SetPBKDFParams(wrongPasscodeTestIterations, salt, 1)
	a.randomSource = func() [spake2.PBKDFRandomSize]byte { return [spake2.PBKDFRandomSize]byte{0x11} }
	initRand := bytes.Repeat([]byte{0x22}, spake2.PBKDFRandomSize)
	if _, _, err := a.ProcessPBKDFParamRequest(buildTestPBKDFParamRequest(t, initRand)); err != nil {
		t.Fatalf("ProcessPBKDFParamRequest: %v", err)
	}
	prover, err := spake2.NewProver(wrongPasscodeTestPasscode, salt, wrongPasscodeTestIterations, nil, nil, nil)
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
}

// wrongConfirmationTag is a right-length (ConfirmTagSize) but incorrect
// cA — it decodes cleanly yet fails the verifier's constant-time
// comparison against the expected hAY, exactly as a wrong-passcode
// prover's confirmation would. Mirrors the tampered-cA injection in the
// spake2 confirmation parity test.
func wrongConfirmationTag() []byte { return bytes.Repeat([]byte{0xAB}, spake2.ConfirmTagSize) }

// TestPaseAdapter_Pake3ConfirmationMismatch_SurfacesErrorForBruteForceCount is
// the core reproducer: a wrong-passcode Pake3 confirmation mismatch must
// surface spake2.ErrConfirmationFailed (not be swallowed into a
// self-emitted StatusReport) and must NOT fire the session callback, so
// the SecureChannel router's handlePase path can emit the single
// StatusReport and count the attempt toward the brute-force cap.
func TestPaseAdapter_Pake3ConfirmationMismatch_SurfacesErrorForBruteForceCount(t *testing.T) {
	t.Parallel()
	a := NewPaseAdapterWithFactory(newVerifierFactory(t, nil))
	primePaseAdapterToPake1(t, a)

	var cbCalled atomic.Bool
	a.SetOnSessionEstablished(func([]byte, uint16) error {
		cbCalled.Store(true)
		return nil
	})

	opcode, body, err := a.ProcessPake3(spake2.EncodePake3(wrongConfirmationTag()))

	if !errors.Is(err, spake2.ErrConfirmationFailed) {
		t.Fatalf("ProcessPake3 err = %v, want spake2.ErrConfirmationFailed surfaced so handlePase counts the brute-force attempt", err)
	}
	if opcode != 0 || body != nil {
		t.Fatalf("ProcessPake3 returned opcode=0x%02X body_len=%d; want (0, nil) so handlePase — not the adapter — emits the single StatusReport", opcode, len(body))
	}
	if cbCalled.Load() {
		t.Fatal("session-established callback must not fire on a confirmation mismatch")
	}
}

// TestDispatchSecureChannel_WrongPasscodePake3_CountsAndRevokesWindowAtCap
// drives paseMaxErrors full wrong-passcode PASE handshakes through the
// production router (dispatchSecureChannel → handlePase → real
// PaseAdapter) and asserts that every mismatch reaches recordPaseFailure
// and that the cap revokes the open commissioning window. This is the
// end-to-end failing reproducer: with the pre-fix adapter swallowing the
// confirmation error, paseFailures never increments and the window stays
// open to unlimited guessing.
func TestDispatchSecureChannel_WrongPasscodePake3_CountsAndRevokesWindowAtCap(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	w := openedWindow(t, b)

	a := NewPaseAdapterWithFactory(newVerifierFactory(t, nil))
	a.SetPBKDFParams(wrongPasscodeTestIterations, wrongPasscodeTestSalt(), 1)
	a.randomSource = func() [spake2.PBKDFRandomSize]byte { return [spake2.PBKDFRandomSize]byte{0x11} }
	b.AttachPaseHandler(a)

	// Pre-built, reusable wire payloads for the three handshake steps.
	reqBytes := buildTestPBKDFParamRequest(t, bytes.Repeat([]byte{0x22}, spake2.PBKDFRandomSize))
	prover, err := spake2.NewProver(wrongPasscodeTestPasscode, wrongPasscodeTestSalt(), wrongPasscodeTestIterations, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewProver: %v", err)
	}
	pA, err := prover.GeneratePake1()
	if err != nil {
		t.Fatalf("GeneratePake1: %v", err)
	}
	pake1Payload := spake2.EncodePake1(pA)
	pake3Payload := spake2.EncodePake3(wrongConfirmationTag())

	const exchangeID = uint16(7)
	drive := func() {
		if err := b.dispatchSecureChannel(loopbackSrc(), scHdr(), scProto(mrp.SCOpcodePBKDFParamRequest, exchangeID, false, 0), reqBytes); err != nil {
			t.Fatalf("dispatch PBKDFParamRequest: %v", err)
		}
		if err := b.dispatchSecureChannel(loopbackSrc(), scHdr(), scProto(mrp.SCOpcodePake1, exchangeID, false, 0), pake1Payload); err != nil {
			t.Fatalf("dispatch Pake1: %v", err)
		}
		// Wrong-passcode Pake3: dispatch returns the confirmation error
		// (handlePase already emitted the StatusReport + counted it).
		_ = b.dispatchSecureChannel(loopbackSrc(), scHdr(), scProto(mrp.SCOpcodePake3, exchangeID, false, 0), pake3Payload)
	}

	for range paseMaxErrors - 1 {
		drive()
	}
	if got := b.paseFailures.Load(); got != int32(paseMaxErrors-1) {
		t.Fatalf("paseFailures = %d after %d wrong-passcode attempts, want %d — each wrong-passcode Pake3 must reach recordPaseFailure", got, paseMaxErrors-1, paseMaxErrors-1)
	}
	if got := w.CurrentWindow().Status; got != wire.WindowStatusEnhanced {
		t.Fatalf("window after %d wrong-passcode attempts = %v, want still Enhanced (open)", paseMaxErrors-1, got)
	}

	// The paseMaxErrors-th genuine failure trips the cap and revokes.
	drive()
	if got := w.CurrentWindow().Status; got != wire.WindowStatusClosed {
		t.Fatalf("window after %d wrong-passcode attempts = %v, want Closed (revoked) — the wrong-passcode Pake3 must reach the brute-force cap", paseMaxErrors, got)
	}
}

// TestDispatchSecureChannel_WrongPasscodePake3_StatusReportUsesInvalidParameter
// captures the StatusReport the bridge ships on a wrong-passcode Pake3
// and asserts it carries FAILURE + InvalidParameter. matter.js answers
// every PASE pairing failure with SecureChannelStatusCode.InvalidParam
// (PaseServer.ts:207-212 cancelPairing → sendError); NoSharedTrustRoots
// is a CASE-only code and must not ride a PASE failure.
func TestDispatchSecureChannel_WrongPasscodePake3_StatusReportUsesInvalidParameter(t *testing.T) {
	// Not parallel: binds a UDP peer socket and reads the reply back.
	peer, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv6loopback, Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP peer: %v", err)
	}
	t.Cleanup(func() { _ = peer.Close() })
	src, ok := peer.LocalAddr().(*net.UDPAddr)
	if !ok {
		t.Fatalf("peer LocalAddr type = %T, want *net.UDPAddr", peer.LocalAddr())
	}

	b := newStartedBridge(t)
	a := NewPaseAdapterWithFactory(newVerifierFactory(t, nil))
	primePaseAdapterToPake1(t, a)
	b.AttachPaseHandler(a)

	// The bridge ships its StatusReport to `src` (our peer socket).
	_ = b.dispatchSecureChannel(src, scHdr(), scProto(mrp.SCOpcodePake3, 7, false, 0), spake2.EncodePake3(wrongConfirmationTag()))

	_ = peer.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 1500)
	n, _, err := peer.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("reading StatusReport from bridge: %v", err)
	}
	general, protocol := decodeStatusReportCodes(t, buf[:n])
	if general != mrp.SCStatusGeneralFailure {
		t.Errorf("StatusReport general code = 0x%04X, want FAILURE 0x%04X", general, mrp.SCStatusGeneralFailure)
	}
	if protocol != mrp.SCStatusProtocolInvalidParameter {
		t.Errorf("StatusReport protocol code = 0x%04X, want InvalidParameter 0x%04X (matter.js sends InvalidParam on PASE failure, not NoSharedTrustRoots 0x%04X)",
			protocol, mrp.SCStatusProtocolInvalidParameter, mrp.SCStatusProtocolNoSharedTrustRoots)
	}
}

// TestPaseAdapter_HappyPathPake3_StillEstablishes guards against a
// regression of the success path: a correct Pake3 still fires the
// session-established callback exactly once and closes the handshake
// with StatusReport(SUCCESS, SESSION_ESTABLISHMENT_SUCCESS) — Matter
// §4.13.4 step 11.
func TestPaseAdapter_HappyPathPake3_StillEstablishes(t *testing.T) {
	t.Parallel()
	a := NewPaseAdapterWithFactory(newVerifierFactory(t, nil))
	var cbCalls atomic.Int32
	a.SetOnSessionEstablished(func([]byte, uint16) error {
		cbCalls.Add(1)
		return nil
	})

	opcode, body, err := paseRoundTrip(t, a)
	if err != nil {
		t.Fatalf("happy-path PASE round trip must still succeed: %v", err)
	}
	if cbCalls.Load() != 1 {
		t.Fatalf("session-established callback fired %d times, want exactly 1", cbCalls.Load())
	}
	if opcode != mrp.SCOpcodeStatusReport {
		t.Fatalf("success opcode = 0x%02X, want StatusReport (0x%02X)", opcode, mrp.SCOpcodeStatusReport)
	}
	if len(body) < 8 {
		t.Fatalf("success StatusReport body = %d bytes, want ≥ 8", len(body))
	}
	general := binary.LittleEndian.Uint16(body[0:2])
	protocol := binary.LittleEndian.Uint16(body[6:8])
	if general != mrp.SCStatusGeneralSuccess {
		t.Errorf("success general code = 0x%04X, want SUCCESS 0x%04X", general, mrp.SCStatusGeneralSuccess)
	}
	if protocol != mrp.SCStatusProtocolSessionEstablishmentSuccess {
		t.Errorf("success protocol code = 0x%04X, want SESSION_ESTABLISHMENT_SUCCESS 0x%04X", protocol, mrp.SCStatusProtocolSessionEstablishmentSuccess)
	}
}

// decodeStatusReportCodes strips the message + protocol headers off a
// captured Secure-Channel datagram and returns the StatusReport general
// + protocol codes (little-endian uint16s at body offsets 0 and 6 per
// Matter §4.10.1.1).
func decodeStatusReportCodes(t *testing.T, datagram []byte) (general, protocol uint16) {
	t.Helper()
	_, n1, err := message.UnmarshalHeader(datagram)
	if err != nil {
		t.Fatalf("UnmarshalHeader: %v", err)
	}
	_, n2, err := message.UnmarshalProtocolHeader(datagram[n1:])
	if err != nil {
		t.Fatalf("UnmarshalProtocolHeader: %v", err)
	}
	body := datagram[n1+n2:]
	if len(body) < 8 {
		t.Fatalf("StatusReport body too short: %d bytes", len(body))
	}
	return binary.LittleEndian.Uint16(body[0:2]), binary.LittleEndian.Uint16(body[6:8])
}
