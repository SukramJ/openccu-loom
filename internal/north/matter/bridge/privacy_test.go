// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package bridge

import (
	"bytes"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/internal/north/matter/secure/channel"
	"github.com/SukramJ/openccu-loom/internal/north/matter/transport/message"
)

// TestPrivacy_NonPrivacyDatagramPassthrough verifies that a header
// without the P bit is left untouched. Both maybeUnmaskPrivacy
// (inbound) and applyOutboundPrivacy must skip non-privacy frames
// even when no session is wired.
func TestPrivacy_NonPrivacyDatagramPassthrough(t *testing.T) {
	t.Parallel()
	hdr := message.Header{SessionID: 7, MessageCounter: 0xCAFE}
	buf := append(hdr.Marshal(), bytes.Repeat([]byte{0x42}, 32)...) // header + 16-byte body + 16-byte MIC
	orig := append([]byte(nil), buf...)

	b := &Bridge{}
	if err := b.maybeUnmaskPrivacy(buf); err != nil {
		t.Fatalf("maybeUnmaskPrivacy on non-privacy frame: %v", err)
	}
	if !bytes.Equal(buf, orig) {
		t.Errorf("non-privacy buffer mutated: got %x want %x", buf, orig)
	}

	if err := applyOutboundPrivacy(nil, 7, buf); err != nil {
		t.Errorf("applyOutboundPrivacy on non-privacy frame: %v", err)
	}
}

// TestPrivacy_OutboundInboundRoundTrip verifies that a Privacy-
// flagged datagram round-trips correctly: outbound mask + inbound
// unmask cancel out, leaving the same plaintext header. Drives the
// XOR symmetry test against a real channel.Session.
func TestPrivacy_OutboundInboundRoundTrip(t *testing.T) {
	t.Parallel()
	bridgeKey := bytes.Repeat([]byte{0xAA}, 16)
	peerKey := bytes.Repeat([]byte{0xBB}, 16)
	const sessionID uint16 = 0x1234
	bridgeSess, err := channel.New(channel.Config{
		EncryptKey:  bridgeKey,
		DecryptKey:  peerKey,
		LocalNodeID: 1,
		PeerNodeID:  2,
	})
	if err != nil {
		t.Fatalf("bridge sess: %v", err)
	}
	peerSess, err := channel.New(channel.Config{
		EncryptKey:  peerKey,
		DecryptKey:  bridgeKey,
		LocalNodeID: 2,
		PeerNodeID:  1,
	})
	if err != nil {
		t.Fatalf("peer sess: %v", err)
	}

	// Build a Privacy-flagged datagram: 4-byte header prefix +
	// 4-byte counter + 32 bytes body (16 ciphertext-ish + 16 MIC).
	hdr := message.Header{
		SessionID:      sessionID,
		MessageCounter: 0xDEADBEEF,
		Privacy:        true,
	}
	body := bytes.Repeat([]byte{0xCC}, 32)
	datagram := append(hdr.Marshal(), body...)

	// Snapshot the protected slice (counter bytes) BEFORE outbound
	// mask so we can verify XOR-symmetry after a roundtrip.
	originalProtected := append([]byte(nil), datagram[4:8]...)

	if err := applyOutboundPrivacy(bridgeSess, sessionID, datagram); err != nil {
		t.Fatalf("applyOutboundPrivacy: %v", err)
	}
	if bytes.Equal(datagram[4:8], originalProtected) {
		t.Errorf("outbound mask did not modify protected slice; mask is broken or session keys collide")
	}

	// Inbound unmask using the peer's privacy key (which equals the
	// bridge's, since both sessions share a complementary key pair).
	b := &Bridge{}
	b.sessions = sessionLookupFunc(func(id uint16) (*channel.Session, bool) {
		if id == sessionID {
			return peerSess, true
		}
		return nil, false
	})
	if err := b.maybeUnmaskPrivacy(datagram); err != nil {
		t.Fatalf("maybeUnmaskPrivacy: %v", err)
	}
	if !bytes.Equal(datagram[4:8], originalProtected) {
		t.Errorf("roundtrip did not recover original protected slice:\n got=%x\nwant=%x",
			datagram[4:8], originalProtected)
	}
}

// TestPrivacy_TooShortBuffer verifies that a buffer shorter than 4
// bytes returns nil (caller's UnmarshalHeader will catch it).
func TestPrivacy_TooShortBuffer(t *testing.T) {
	t.Parallel()
	b := &Bridge{}
	if err := b.maybeUnmaskPrivacy([]byte{0x01, 0x02}); err != nil {
		t.Errorf("short buffer: want nil, got %v", err)
	}
}

// TestPrivacy_NoSessionLookup_ReturnsError verifies that a privacy-flagged
// datagram with no session lookup wired returns an error.
func TestPrivacy_NoSessionLookup_ReturnsError(t *testing.T) {
	t.Parallel()
	// Build a datagram with the P bit set and a non-zero SessionID.
	hdr := message.Header{SessionID: 0x0001, Privacy: true}
	buf := append(hdr.Marshal(), make([]byte, 32)...)
	b := &Bridge{} // no sessions wired
	err := b.maybeUnmaskPrivacy(buf)
	if err == nil {
		t.Error("no session lookup: want error, got nil")
	}
}

// TestPrivacy_SessionNotFound_ReturnsError verifies that when the
// session lookup does not find the session ID, an error is returned.
func TestPrivacy_SessionNotFound_ReturnsError(t *testing.T) {
	t.Parallel()
	hdr := message.Header{SessionID: 0x0001, Privacy: true}
	buf := append(hdr.Marshal(), make([]byte, 32)...)
	b := &Bridge{}
	// Wires a lookup that always misses.
	b.sessions = sessionLookupFunc(func(_ uint16) (*channel.Session, bool) {
		return nil, false
	})
	err := b.maybeUnmaskPrivacy(buf)
	if err == nil {
		t.Error("session not found: want error, got nil")
	}
}

// TestPrivacy_PASESessionIDRejected verifies that a P-bit on
// SessionID=0 (PASE pre-fabric) surfaces an error per spec.
func TestPrivacy_PASESessionIDRejected(t *testing.T) {
	t.Parallel()
	hdr := message.Header{
		SessionID: 0, // PASE
		Privacy:   true,
	}
	buf := append(hdr.Marshal(), make([]byte, 32)...)
	b := &Bridge{}
	err := b.maybeUnmaskPrivacy(buf)
	if err == nil {
		t.Error("P bit on SessionID=0 should error; got nil")
	}
}

// ─── privacyHeaderEnd ────────────────────────────────────────────────────────

// TestPrivacyHeaderEnd_TooShort verifies that a buffer shorter than 4
// bytes returns its own length.
func TestPrivacyHeaderEnd_TooShort(t *testing.T) {
	t.Parallel()
	for _, n := range []int{0, 1, 3} {
		buf := make([]byte, n)
		if got := privacyHeaderEnd(buf); got != n {
			t.Errorf("len=%d: want %d, got %d", n, n, got)
		}
	}
}

// TestPrivacyHeaderEnd_NoNodeIDs returns 8 (flags + sessionID/secflags + counter).
func TestPrivacyHeaderEnd_NoNodeIDs(t *testing.T) {
	t.Parallel()
	// flags=0x00: no SourceNodeID bit, DestSize=0 → end = 4+4 = 8.
	buf := make([]byte, 20)
	buf[0] = 0x00
	if got := privacyHeaderEnd(buf); got != 8 {
		t.Errorf("no NodeIDs: want 8, got %d", got)
	}
}

// TestPrivacyHeaderEnd_WithSourceNodeID adds 8 bytes for SourceNodeID.
func TestPrivacyHeaderEnd_WithSourceNodeID(t *testing.T) {
	t.Parallel()
	// flags bit 0x04 = HasSourceNodeID → end = 4+4+8 = 16.
	buf := make([]byte, 30)
	buf[0] = 0x04
	if got := privacyHeaderEnd(buf); got != 16 {
		t.Errorf("SourceNodeID: want 16, got %d", got)
	}
}

// TestPrivacyHeaderEnd_Dest64bit adds 8 bytes for DestNodeID (flags & 0x03 == 1).
func TestPrivacyHeaderEnd_Dest64bit(t *testing.T) {
	t.Parallel()
	// flags & 0x03 == 1 → DestNodeID 64-bit (8 bytes) → end = 4+4+8 = 16.
	buf := make([]byte, 30)
	buf[0] = 0x01
	if got := privacyHeaderEnd(buf); got != 16 {
		t.Errorf("Dest64bit: want 16, got %d", got)
	}
}

// TestPrivacyHeaderEnd_Dest16bit adds 2 bytes for DestGroupID (flags & 0x03 == 2).
func TestPrivacyHeaderEnd_Dest16bit(t *testing.T) {
	t.Parallel()
	// flags & 0x03 == 2 → DestNodeID 16-bit (group, 2 bytes) → end = 4+4+2 = 10.
	buf := make([]byte, 20)
	buf[0] = 0x02
	if got := privacyHeaderEnd(buf); got != 10 {
		t.Errorf("Dest16bit: want 10, got %d", got)
	}
}

// TestPrivacyHeaderEnd_ClampedToLength verifies that if the computed
// end exceeds the actual buffer length, the function clamps to len(buf).
func TestPrivacyHeaderEnd_ClampedToLength(t *testing.T) {
	t.Parallel()
	// flags=0x04 would imply end=16, but only 10 bytes are provided.
	buf := make([]byte, 10)
	buf[0] = 0x04 // SourceNodeID flag → end would be 16 but buf is only 10
	if got := privacyHeaderEnd(buf); got != 10 {
		t.Errorf("clamped: want 10, got %d", got)
	}
}

// ─── applyOutboundPrivacy additional branches ────────────────────────────────

// TestApplyOutboundPrivacy_TooShortForMIC verifies that a Privacy-flagged
// datagram shorter than header+privacyMICSuffix bytes returns an error.
func TestApplyOutboundPrivacy_TooShortForMIC(t *testing.T) {
	t.Parallel()
	bridgeKey := bytes.Repeat([]byte{0xAA}, 16)
	peerKey := bytes.Repeat([]byte{0xBB}, 16)
	sess, err := channel.New(channel.Config{
		EncryptKey:  bridgeKey,
		DecryptKey:  peerKey,
		LocalNodeID: 1,
		PeerNodeID:  2,
	})
	if err != nil {
		t.Fatalf("channel.New: %v", err)
	}
	// Build a Privacy-flagged datagram that is too short to hold the MIC.
	hdr := message.Header{SessionID: 0x0001, Privacy: true}
	buf := hdr.Marshal() // only 4 bytes — too short for privacyMICSuffix (16)
	if err := applyOutboundPrivacy(sess, 0x0001, buf); err == nil {
		t.Error("applyOutboundPrivacy with too-short datagram: want error, got nil")
	}
}

// TestApplyOutboundPrivacy_NoPrivacyBit_NonNilSession verifies that a
// datagram WITHOUT the Privacy bit set is left untouched even when a
// non-nil session is provided. Exercises the `datagram[3]&secFlagPrivacyBit==0`
// early-return on line 137.
func TestApplyOutboundPrivacy_NoPrivacyBit_NonNilSession(t *testing.T) {
	t.Parallel()
	bridgeKey := bytes.Repeat([]byte{0xCC}, 16)
	peerKey := bytes.Repeat([]byte{0xDD}, 16)
	sess, err := channel.New(channel.Config{
		EncryptKey:  bridgeKey,
		DecryptKey:  peerKey,
		LocalNodeID: 3,
		PeerNodeID:  4,
	})
	if err != nil {
		t.Fatalf("channel.New: %v", err)
	}
	// Build a datagram WITHOUT the Privacy bit — Privacy field is false (default).
	hdr := message.Header{SessionID: 0x0002}
	buf := append(hdr.Marshal(), make([]byte, 32)...)
	orig := append([]byte(nil), buf...)

	if err := applyOutboundPrivacy(sess, 0x0002, buf); err != nil {
		t.Errorf("applyOutboundPrivacy no P-bit: unexpected error: %v", err)
	}
	if !bytes.Equal(buf, orig) {
		t.Errorf("applyOutboundPrivacy no P-bit: buffer mutated unexpectedly")
	}
}

// TestApplyOutboundPrivacy_ClosedSession_ReturnsError verifies that a
// Privacy-flagged datagram passed to a closed session returns an error
// from PrivacyKey. Exercises lines 143-146.
func TestApplyOutboundPrivacy_ClosedSession_ReturnsError(t *testing.T) {
	t.Parallel()
	bridgeKey := bytes.Repeat([]byte{0xEE}, 16)
	peerKey := bytes.Repeat([]byte{0xFF}, 16)
	sess, err := channel.New(channel.Config{
		EncryptKey:  bridgeKey,
		DecryptKey:  peerKey,
		LocalNodeID: 5,
		PeerNodeID:  6,
	})
	if err != nil {
		t.Fatalf("channel.New: %v", err)
	}
	sess.Close()

	// Privacy-flagged datagram with enough bytes for the MIC tail check.
	hdr := message.Header{SessionID: 0x0003, Privacy: true}
	buf := append(hdr.Marshal(), make([]byte, 32)...)
	if err := applyOutboundPrivacy(sess, 0x0003, buf); err == nil {
		t.Error("applyOutboundPrivacy with closed session: want error, got nil")
	}
}

// TestMaybeUnmaskPrivacy_ClosedSession_ReturnsError verifies that a
// Privacy-flagged inbound datagram whose session has been closed
// surfaces an error from PeerPrivacyKey. Exercises lines 67-70.
func TestMaybeUnmaskPrivacy_ClosedSession_ReturnsError(t *testing.T) {
	t.Parallel()
	bridgeKey := bytes.Repeat([]byte{0x11}, 16)
	peerKey := bytes.Repeat([]byte{0x22}, 16)
	const sessionID uint16 = 0x0007
	sess, err := channel.New(channel.Config{
		EncryptKey:  bridgeKey,
		DecryptKey:  peerKey,
		LocalNodeID: 7,
		PeerNodeID:  8,
	})
	if err != nil {
		t.Fatalf("channel.New: %v", err)
	}
	sess.Close()

	// Build a Privacy-flagged datagram (P bit in secFlags / byte 3)
	// with enough bytes to pass the MIC-tail check.
	hdr := message.Header{SessionID: sessionID, Privacy: true}
	buf := append(hdr.Marshal(), make([]byte, 32)...)

	b := &Bridge{}
	b.sessions = sessionLookupFunc(func(id uint16) (*channel.Session, bool) {
		if id == sessionID {
			return sess, true
		}
		return nil, false
	})
	if err := b.maybeUnmaskPrivacy(buf); err == nil {
		t.Error("maybeUnmaskPrivacy with closed session: want error, got nil")
	}
}

// TestMaybeUnmaskPrivacy_TooShortForMIC_AfterSessionResolve verifies that
// a Privacy-flagged datagram that is long enough to have the P-bit checked
// but too short for the MIC tail check (len < 4+privacyMICSuffix) returns
// an error. Exercises lines 75-77.
func TestMaybeUnmaskPrivacy_TooShortForMIC_AfterSessionResolve(t *testing.T) {
	t.Parallel()
	bridgeKey := bytes.Repeat([]byte{0x33}, 16)
	peerKey := bytes.Repeat([]byte{0x44}, 16)
	const sessionID uint16 = 0x0009
	sess, err := channel.New(channel.Config{
		EncryptKey:  bridgeKey,
		DecryptKey:  peerKey,
		LocalNodeID: 9,
		PeerNodeID:  10,
	})
	if err != nil {
		t.Fatalf("channel.New: %v", err)
	}

	// Build a Privacy-flagged datagram that is > 4 bytes (so we pass the
	// initial length check) but < 4+privacyMICSuffix bytes (so the MIC-tail
	// check fails). Marshal gives 8 bytes; append 5 more → 13 < 18.
	hdr := message.Header{SessionID: sessionID, Privacy: true}
	buf := append(hdr.Marshal(), make([]byte, 5)...)

	b := &Bridge{}
	b.sessions = sessionLookupFunc(func(id uint16) (*channel.Session, bool) {
		if id == sessionID {
			return sess, true
		}
		return nil, false
	})
	if err := b.maybeUnmaskPrivacy(buf); err == nil {
		t.Error("maybeUnmaskPrivacy with short MIC tail: want error, got nil")
	}
}

// TestSendReply_PrivacyFlaggedRequestGetsMaskedReply pins the outbound
// half of Matter §4.7.3.1 on the production reply path: the response
// header echoes the request's Privacy flag, so the header suffix it
// announces as masked MUST actually be masked. Shipping the P bit over
// an unmasked counter makes the peer XOR-unmask valid bytes, derive the
// wrong nonce, and drop every reply on the exchange until MRP gives up.
func TestSendReply_PrivacyFlaggedRequestGetsMaskedReply(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	const (
		localSessionID uint16 = 7
		peerSessionID  uint16 = 0x1234
	)
	bridgeSess, peerSess := activitySessionPair(t, peerSessionID)
	b.AttachSessionLookup(sessionLookupFunc(func(id uint16) (*channel.Session, bool) {
		if id == localSessionID {
			return bridgeSess, true
		}
		return nil, false
	}))

	peerConn, peerAddr := newSubscribeTestPeer(t)
	requestHdr := &message.Header{SessionID: localSessionID, MessageCounter: 42, Privacy: true}
	requestProto := message.ProtocolHeader{
		Initiator:  true,
		ExchangeID: 1,
		ProtocolID: im.InteractionModelProtocolID,
	}
	body, err := EncodeStatusResponse(im.StatusResponse{Status: im.StatusSuccess})
	if err != nil {
		t.Fatalf("EncodeStatusResponse: %v", err)
	}
	if err := b.sendReply(peerAddr, requestHdr, requestProto, im.OpcodeStatusResponse, body); err != nil {
		t.Fatalf("sendReply: %v", err)
	}

	_ = peerConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	rbuf := make([]byte, 1500)
	n, _, err := peerConn.ReadFromUDP(rbuf)
	if err != nil {
		t.Fatalf("ReadFromUDP: %v", err)
	}
	datagram := rbuf[:n]
	if datagram[3]&secFlagPrivacyBit == 0 {
		t.Fatal("reply did not carry the P bit — precondition for this test")
	}

	// The peer unmasks with its own view of the session, then decrypts.
	// Both only work when the bridge applied the mask.
	peer := &Bridge{}
	peer.sessions = sessionLookupFunc(func(id uint16) (*channel.Session, bool) {
		if id == peerSessionID {
			return peerSess, true
		}
		return nil, false
	})
	if err := peer.maybeUnmaskPrivacy(datagram); err != nil {
		t.Fatalf("peer maybeUnmaskPrivacy: %v", err)
	}
	hdr, hdrLen, err := message.UnmarshalHeader(datagram)
	if err != nil {
		t.Fatalf("UnmarshalHeader after unmask: %v", err)
	}
	if _, _, err := peerSess.Decrypt(&hdr, datagram[3], datagram[hdrLen:]); err != nil {
		t.Fatalf("peer Decrypt of the privacy-protected reply: %v", err)
	}
}
