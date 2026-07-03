// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package bridge

// White-box reproducers for two PASE brute-force-counting gaps that
// matter.js closes but the adapter previously swallowed:
//
//   - A PBKDFParamRequest carrying a non-default PasscodeId must be
//     rejected. matter.js throws UnexpectedDataError when
//     passcodeId !== DEFAULT_PASSCODE_ID and the throw is caught into
//     the #pairingErrors counter (packages/protocol/src/session/pase/
//     PaseServer.ts:144-146 → :94-95).
//   - A malformed / invalid-point Pake1 must surface its error (not be
//     swallowed into a self-emitted StatusReport with a nil Go error) so
//     the SecureChannel router's handlePase path counts the attempt
//     toward PASE_COMMISSIONING_MAX_ERRORS, mirroring matter.js counting
//     every PASE pairing failure (PaseServer.ts:172,174 → :94-95).
//
// Lives in package bridge (not bridge_test) so it can reach the
// unexported PaseAdapter internals, dispatchSecureChannel, and the
// paseFailures counter. Reuses helpers from handlers_test.go
// (newVerifierFactory, buildTestPBKDFParamRequest), securechannel_test.go
// (scProto, scHdr) and receive_test.go (newStartedBridge, loopbackSrc).

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/secure/spake2"
	"github.com/SukramJ/openccu-loom/internal/north/matter/transport/mrp"
)

const (
	failureCountTestIterations = 1000
)

func failureCountTestSalt() []byte { return []byte("SPAKE2P Key Salt") }

// buildTestPBKDFParamRequestPasscodeID assembles a PBKDFParamRequest TLV
// payload whose PasscodeId (context tag 3) is passcodeID rather than the
// default 0, exercising the non-default-passcode rejection path.
func buildTestPBKDFParamRequestPasscodeID(t *testing.T, initRand []byte, passcodeID uint16) []byte {
	t.Helper()
	if len(initRand) != spake2.PBKDFRandomSize {
		t.Fatalf("buildTestPBKDFParamRequestPasscodeID: initRand must be %d bytes", spake2.PBKDFRandomSize)
	}
	var pid [2]byte
	binary.LittleEndian.PutUint16(pid[:], passcodeID)
	out := make([]byte, 0, 4+len(initRand)+11)
	out = append(out, 0x15, 0x30, 0x01, byte(spake2.PBKDFRandomSize)) // Structure, [1]=octets(32)
	out = append(out, initRand...)
	out = append(out,
		0x25, 0x02, 0x2A, 0x00, // [2]=uint16 sessionID 42
		0x25, 0x03, pid[0], pid[1], // [3]=uint16 passcodeID
		0x28, 0x04, // [4]=bool false
		0x18) // EndContainer
	return out
}

// primedPaseAdapter returns a PaseAdapter that has already answered a
// valid PBKDFParamRequest (passcodeId 0), so its PBKDFParam context is
// captured and a subsequent ProcessPake1 reaches the decode / verify
// stage rather than the "Pake1 before PBKDFParamRequest" guard.
func primedPaseAdapter(t *testing.T) *PaseAdapter {
	t.Helper()
	a := NewPaseAdapterWithFactory(newVerifierFactory(t, nil))
	a.SetPBKDFParams(failureCountTestIterations, failureCountTestSalt(), 1)
	a.SetRandomSource(func() [spake2.PBKDFRandomSize]byte { return [spake2.PBKDFRandomSize]byte{0x11} })
	initRand := bytes.Repeat([]byte{0x22}, spake2.PBKDFRandomSize)
	if _, _, err := a.ProcessPBKDFParamRequest(buildTestPBKDFParamRequest(t, initRand)); err != nil {
		t.Fatalf("ProcessPBKDFParamRequest (priming): %v", err)
	}
	return a
}

// TestPaseAdapter_NonDefaultPasscodeID_Rejected asserts a
// PBKDFParamRequest with PasscodeId != 0 is rejected with
// ErrUnsupportedPasscodeID and produces no reply, while the default
// PasscodeId 0 is still accepted. Mirrors matter.js PaseServer.ts:144-146
// (throw UnexpectedDataError on passcodeId !== DEFAULT_PASSCODE_ID).
func TestPaseAdapter_NonDefaultPasscodeID_Rejected(t *testing.T) {
	t.Parallel()
	a := NewPaseAdapterWithFactory(newVerifierFactory(t, nil))
	a.SetPBKDFParams(failureCountTestIterations, failureCountTestSalt(), 1)
	a.SetRandomSource(func() [spake2.PBKDFRandomSize]byte { return [spake2.PBKDFRandomSize]byte{0x11} })
	initRand := bytes.Repeat([]byte{0x22}, spake2.PBKDFRandomSize)

	opcode, body, err := a.ProcessPBKDFParamRequest(buildTestPBKDFParamRequestPasscodeID(t, initRand, 5))
	if !errors.Is(err, ErrUnsupportedPasscodeID) {
		t.Fatalf("ProcessPBKDFParamRequest(passcodeId=5) err = %v, want ErrUnsupportedPasscodeID surfaced so handlePase counts the attempt", err)
	}
	if opcode != 0 || body != nil {
		t.Fatalf("rejected request returned opcode=0x%02X body_len=%d; want (0, nil) so handlePase — not the adapter — emits the StatusReport", opcode, len(body))
	}

	// The default PasscodeId 0 must still be accepted.
	opcode, body, err = a.ProcessPBKDFParamRequest(buildTestPBKDFParamRequestPasscodeID(t, initRand, 0))
	if err != nil {
		t.Fatalf("ProcessPBKDFParamRequest(passcodeId=0) must succeed: %v", err)
	}
	if opcode != mrp.SCOpcodePBKDFParamResponse || len(body) == 0 {
		t.Fatalf("accepted request returned opcode=0x%02X body_len=%d; want PBKDFParamResponse with a body", opcode, len(body))
	}
}

// TestDispatchSecureChannel_NonDefaultPasscodeID_CountsPaseFailure drives
// a non-default-passcode PBKDFParamRequest through the production router
// and asserts the rejection reaches recordPaseFailure — the pre-fix
// adapter answered with a response and never counted the attempt.
func TestDispatchSecureChannel_NonDefaultPasscodeID_CountsPaseFailure(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	a := NewPaseAdapterWithFactory(newVerifierFactory(t, nil))
	a.SetPBKDFParams(failureCountTestIterations, failureCountTestSalt(), 1)
	a.SetRandomSource(func() [spake2.PBKDFRandomSize]byte { return [spake2.PBKDFRandomSize]byte{0x11} })
	b.AttachPaseHandler(a)

	reqBytes := buildTestPBKDFParamRequestPasscodeID(t, bytes.Repeat([]byte{0x22}, spake2.PBKDFRandomSize), 5)
	_ = b.dispatchSecureChannel(loopbackSrc(), scHdr(), scProto(mrp.SCOpcodePBKDFParamRequest, 7, false, 0), reqBytes)

	if got := b.paseFailures.Load(); got != 1 {
		t.Fatalf("paseFailures = %d after a non-default-passcode PBKDFParamRequest, want 1 — the rejection must reach recordPaseFailure", got)
	}
}

// TestPaseAdapter_Pake1Failure_SurfacesErrorForBruteForceCount asserts a
// malformed or invalid-point Pake1 surfaces its error (opcode 0, nil
// body) and clears the freshly-allocated verifier, so handlePase counts
// the attempt toward the brute-force cap. Mirrors matter.js
// PaseServer.ts:172,174 → :94-95 (every PASE failure → #pairingErrors++).
func TestPaseAdapter_Pake1Failure_SurfacesErrorForBruteForceCount(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		payload []byte
		wantErr error
	}{
		{
			// Right TLV shape, wrong pA length — DecodePake1 rejects it.
			name:    "decode_malformed",
			payload: spake2.EncodePake1(make([]byte, 10)),
			wantErr: spake2.ErrWireMalformed,
		},
		{
			// Correct pA length but not a valid curve point —
			// Verifier.ProcessPake1 rejects it in unmarshalAndValidate.
			name:    "invalid_point",
			payload: spake2.EncodePake1(make([]byte, spake2.PointSize)),
			wantErr: spake2.ErrInvalidPoint,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a := primedPaseAdapter(t)
			opcode, body, err := a.ProcessPake1(tc.payload)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("ProcessPake1(%s) err = %v, want %v surfaced so handlePase counts the brute-force attempt", tc.name, err, tc.wantErr)
			}
			if opcode != 0 || body != nil {
				t.Fatalf("ProcessPake1(%s) returned opcode=0x%02X body_len=%d; want (0, nil) so handlePase emits the single StatusReport", tc.name, opcode, len(body))
			}
			if a.verifier != nil {
				t.Fatalf("ProcessPake1(%s) left a stale verifier; a failed Pake1 must clear it so a retry starts fresh", tc.name)
			}
		})
	}
}

// TestDispatchSecureChannel_Pake1Failure_CountsPaseFailure drives a valid
// PBKDFParamRequest followed by a malformed Pake1 through the production
// router and asserts the valid request does NOT count while the malformed
// Pake1 DOES — the pre-fix adapter swallowed the decode error and left
// the counter at 0.
func TestDispatchSecureChannel_Pake1Failure_CountsPaseFailure(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	a := NewPaseAdapterWithFactory(newVerifierFactory(t, nil))
	a.SetPBKDFParams(failureCountTestIterations, failureCountTestSalt(), 1)
	a.SetRandomSource(func() [spake2.PBKDFRandomSize]byte { return [spake2.PBKDFRandomSize]byte{0x11} })
	b.AttachPaseHandler(a)

	const exchangeID = uint16(7)
	reqBytes := buildTestPBKDFParamRequest(t, bytes.Repeat([]byte{0x22}, spake2.PBKDFRandomSize))
	if err := b.dispatchSecureChannel(loopbackSrc(), scHdr(), scProto(mrp.SCOpcodePBKDFParamRequest, exchangeID, false, 0), reqBytes); err != nil {
		t.Fatalf("dispatch valid PBKDFParamRequest: %v", err)
	}
	if got := b.paseFailures.Load(); got != 0 {
		t.Fatalf("paseFailures = %d after a valid PBKDFParamRequest, want 0", got)
	}

	badPake1 := spake2.EncodePake1(make([]byte, 10)) // wrong pA length
	_ = b.dispatchSecureChannel(loopbackSrc(), scHdr(), scProto(mrp.SCOpcodePake1, exchangeID, false, 0), badPake1)
	if got := b.paseFailures.Load(); got != 1 {
		t.Fatalf("paseFailures = %d after a malformed Pake1, want 1 — Pake1 decode failures must count toward the brute-force cap", got)
	}
}
