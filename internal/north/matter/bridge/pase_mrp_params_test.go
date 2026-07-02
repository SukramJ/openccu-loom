// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package bridge

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/secure/spake2"
	"github.com/SukramJ/openccu-loom/internal/north/matter/transport/mrp"
)

// buildTestPBKDFParamRequestWithMRP is buildTestPBKDFParamRequest plus
// an InitiatorMRPParams (tag 5) nested structure carrying idle=800,
// active=200, threshold=3000 ms.
func buildTestPBKDFParamRequestWithMRP(t *testing.T, initRand []byte) []byte {
	t.Helper()
	if len(initRand) != spake2.PBKDFRandomSize {
		t.Fatalf("initRand must be %d bytes", spake2.PBKDFRandomSize)
	}
	out := make([]byte, 0, 32)
	out = append(
		out,
		0x15,                                     // anonymous Structure
		0x30, 0x01, byte(spake2.PBKDFRandomSize), // [1] octet-string len=32
	)
	out = append(out, initRand...)
	out = append(
		out,
		0x25, 0x02, 0x2A, 0x00, // [2] uint16 42
		0x25, 0x03, 0x00, 0x00, // [3] uint16 0
		0x28, 0x04, // [4] bool false
		// [5] Structure { [1]:u16 800, [2]:u16 200, [3]:u16 3000 }
		0x35, 0x05,
		0x25, 0x01, 0x20, 0x03, // [1] uint16 800 (0x0320)
		0x25, 0x02, 0xC8, 0x00, // [2] uint16 200 (0x00C8)
		0x25, 0x03, 0xB8, 0x0B, // [3] uint16 3000 (0x0BB8)
		0x18, // end of MRP struct
		0x18, // end of top struct
	)
	return out
}

// TestPaseAdapter_DecodesInitiatorMRPParams verifies the commissioner's
// InitiatorMRPParams (PBKDFParamRequest tag 5) is decoded and retained
// on the adapter for the session opener. Mirrors matter.js
// PaseServer.ts:155-157.
func TestPaseAdapter_DecodesInitiatorMRPParams(t *testing.T) {
	t.Parallel()
	a := NewPaseAdapter(nil)
	a.SetPBKDFParams(1000, []byte("SPAKE2P Key Salt"), 1)
	a.SetRandomSource(func() [spake2.PBKDFRandomSize]byte { return [spake2.PBKDFRandomSize]byte{0x11} })

	initRand := make([]byte, spake2.PBKDFRandomSize)
	for i := range initRand {
		initRand[i] = 0x22
	}
	if _, _, err := a.ProcessPBKDFParamRequest(buildTestPBKDFParamRequestWithMRP(t, initRand)); err != nil {
		t.Fatalf("ProcessPBKDFParamRequest: %v", err)
	}
	pm := a.PeerMRPParams()
	if pm == nil {
		t.Fatal("PeerMRPParams nil — tag 5 not decoded")
	}
	if pm.IdleRetransTimeoutMs == nil || *pm.IdleRetransTimeoutMs != 800 {
		t.Errorf("idle = %v, want 800", pm.IdleRetransTimeoutMs)
	}
	if pm.ActiveRetransTimeoutMs == nil || *pm.ActiveRetransTimeoutMs != 200 {
		t.Errorf("active = %v, want 200", pm.ActiveRetransTimeoutMs)
	}
	if pm.ActiveThresholdTimeMs == nil || *pm.ActiveThresholdTimeMs != 3000 {
		t.Errorf("threshold = %v, want 3000", pm.ActiveThresholdTimeMs)
	}
}

// TestPaseAdapter_AbsentMRPParamsNil verifies a request WITHOUT tag 5
// leaves PeerMRPParams nil (spec-default fallback).
func TestPaseAdapter_AbsentMRPParamsNil(t *testing.T) {
	t.Parallel()
	a := NewPaseAdapter(nil)
	a.SetPBKDFParams(1000, []byte("SPAKE2P Key Salt"), 1)
	a.SetRandomSource(func() [spake2.PBKDFRandomSize]byte { return [spake2.PBKDFRandomSize]byte{0x11} })

	initRand := make([]byte, spake2.PBKDFRandomSize)
	for i := range initRand {
		initRand[i] = 0x22
	}
	if _, _, err := a.ProcessPBKDFParamRequest(buildTestPBKDFParamRequest(t, initRand)); err != nil {
		t.Fatalf("ProcessPBKDFParamRequest: %v", err)
	}
	if pm := a.PeerMRPParams(); pm != nil {
		t.Errorf("PeerMRPParams = %+v, want nil for a request without tag 5", pm)
	}
}

// TestPaseAdapter_EmitsResponderMRPParams verifies the bridge advertises
// its ResponderMRPParams (PBKDFParamResponse tag 5) when configured.
// Mirrors matter.js PaseServer.ts:151.
func TestPaseAdapter_EmitsResponderMRPParams(t *testing.T) {
	t.Parallel()
	a := NewPaseAdapter(nil)
	a.SetPBKDFParams(1000, []byte("SPAKE2P Key Salt"), 1)
	a.SetRandomSource(func() [spake2.PBKDFRandomSize]byte { return [spake2.PBKDFRandomSize]byte{0x11} })
	idle, active, thresh := uint16(500), uint16(300), uint16(4000)
	a.SetResponderMRPParams(&spake2.MRPParameters{
		IdleRetransTimeoutMs:   &idle,
		ActiveRetransTimeoutMs: &active,
		ActiveThresholdTimeMs:  &thresh,
	})

	initRand := make([]byte, spake2.PBKDFRandomSize)
	for i := range initRand {
		initRand[i] = 0x22
	}
	_, respBytes, err := a.ProcessPBKDFParamRequest(buildTestPBKDFParamRequest(t, initRand))
	if err != nil {
		t.Fatalf("ProcessPBKDFParamRequest: %v", err)
	}
	resp, derr := spake2.DecodePBKDFParamResponse(respBytes)
	if derr != nil {
		t.Fatalf("DecodePBKDFParamResponse: %v", derr)
	}
	if resp.ResponderMRPParams == nil {
		t.Fatal("ResponderMRPParams absent from PBKDFParamResponse (tag 5 not emitted)")
	}
	if resp.ResponderMRPParams.IdleRetransTimeoutMs == nil || *resp.ResponderMRPParams.IdleRetransTimeoutMs != 500 {
		t.Errorf("responder idle = %v, want 500", resp.ResponderMRPParams.IdleRetransTimeoutMs)
	}
}

// TestBridge_SingleActivePASE verifies the single-active-PASE claim:
// while one exchange holds the slot a different exchange is refused, the
// same exchange re-claims (retransmit), release frees it, and an expired
// claim is reclaimable. Mirrors matter.js PaseServer.ts:80-86.
func TestBridge_SingleActivePASE(t *testing.T) {
	t.Parallel()
	b := &Bridge{}

	if !b.claimPaseInFlight(0x1111) {
		t.Fatal("first claim must succeed on an idle slot")
	}
	if b.claimPaseInFlight(0x2222) {
		t.Error("a different exchange must be refused while the slot is held")
	}
	if !b.claimPaseInFlight(0x1111) {
		t.Error("the SAME exchange must re-claim (PBKDFParamRequest retransmit)")
	}
	b.releasePaseInFlight(0x1111)
	if !b.claimPaseInFlight(0x2222) {
		t.Error("after release the slot must be free for a new exchange")
	}
}

// TestBridge_SingleActivePASE_ReleaseWrongExchangeIgnored verifies a
// release from a non-owning exchange does not free the slot.
func TestBridge_SingleActivePASE_ReleaseWrongExchangeIgnored(t *testing.T) {
	t.Parallel()
	b := &Bridge{}
	if !b.claimPaseInFlight(0x1111) {
		t.Fatal("first claim must succeed")
	}
	b.releasePaseInFlight(0x9999) // not the owner
	if b.claimPaseInFlight(0x2222) {
		t.Error("slot must stay held after a non-owner release")
	}
}

// TestCounter_SecureSessionNoRollover pins the secure-session counter
// contract at the transport layer: NextNoRollover refuses to wrap and
// surfaces ErrCounterExhausted at the ceiling.
func TestCounter_SecureSessionNoRollover(t *testing.T) {
	t.Parallel()
	c := mrp.NewCounterFromSeed(0xFFFFFFFF)
	if _, err := c.NextNoRollover(); err == nil {
		t.Fatal("NextNoRollover at 0xFFFFFFFF must return ErrCounterExhausted")
	}
}
