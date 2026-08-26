// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package bridge

// White-box tests for the session-activity chokepoints feeding the
// operational idle reaper, mirroring matter.js HEAD:
//
//   - packages/protocol/src/session/Session.ts:127-133
//     notifyActivity(messageReceived) — `timestamp` refreshes in both
//     directions, `activeTimestamp` only on receive.
//   - packages/protocol/src/protocol/MessageExchange.ts:429 —
//     #notifyActivity(true) fires for EVERY received message, before
//     the duplicate branch (:432), so MRP retransmits count as
//     activity.
//   - packages/protocol/src/protocol/MessageExchange.ts:562/:814 —
//     #notifyActivity(false) accompanies every send.

import (
	"context"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/internal/north/matter/secure/channel"
	"github.com/SukramJ/openccu-loom/internal/north/matter/transport/message"
)

// markingSessionLookup implements SessionLookup plus the optional
// SessionActivityMarker capability, recording every mark call.
type markingSessionLookup struct {
	session *channel.Session

	mu sync.Mutex
	rx []uint16
	tx []uint16
}

func (f *markingSessionLookup) Lookup(_ uint16) (*channel.Session, bool) {
	return f.session, f.session != nil
}

func (f *markingSessionLookup) MarkActiveRx(sessionID uint16) {
	f.mu.Lock()
	f.rx = append(f.rx, sessionID)
	f.mu.Unlock()
}

func (f *markingSessionLookup) MarkActiveTx(sessionID uint16) {
	f.mu.Lock()
	f.tx = append(f.tx, sessionID)
	f.mu.Unlock()
}

func (f *markingSessionLookup) marks() (rx, tx []uint16) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]uint16(nil), f.rx...), append([]uint16(nil), f.tx...)
}

// activitySessionPair builds the bridge-side and peer-side halves of an
// established secure session (same key material, mirrored directions).
func activitySessionPair(t *testing.T, peerSessionID uint16) (bridgeSess, peerSess *channel.Session) {
	t.Helper()
	encKey := make([]byte, 16) // bridge → peer
	decKey := make([]byte, 16) // peer → bridge
	for i := range encKey {
		encKey[i] = byte(i)
		decKey[i] = byte(i + 16)
	}
	bridgeSess, err := channel.New(channel.Config{
		EncryptKey: encKey, DecryptKey: decKey,
		LocalNodeID: 0xB0B, PeerNodeID: 0xA11,
		PeerSessionID: peerSessionID, InitialCounter: 100,
	})
	if err != nil {
		t.Fatalf("channel.New (bridge): %v", err)
	}
	peerSess, err = channel.New(channel.Config{
		EncryptKey: decKey, DecryptKey: encKey,
		LocalNodeID: 0xA11, PeerNodeID: 0xB0B,
		InitialCounter: 100,
	})
	if err != nil {
		t.Fatalf("channel.New (peer): %v", err)
	}
	return bridgeSess, peerSess
}

// encryptInboundIMDatagram seals an IM ReadRequest from the peer for the
// bridge-local session id and returns the full wire datagram.
func encryptInboundIMDatagram(t *testing.T, peerSess *channel.Session, localSessionID uint16) []byte {
	t.Helper()
	body := buildDatagram(nil,
		buildProtocolHeader(im.InteractionModelProtocolID, im.OpcodeReadRequest),
		buildIMReadRequestPayload(t))
	hdr := message.Header{SessionID: localSessionID}
	enc, err := peerSess.Encrypt(&hdr, securityFlagsByte(&hdr), body)
	if err != nil {
		t.Fatalf("peer Encrypt: %v", err)
	}
	return buildDatagram(hdr.Marshal(), enc.Ciphertext, nil)
}

// TestDispatch_AuthenticatedInboundMarksRxActivity verifies the single
// inbound chokepoint: a message that decrypts + authenticates refreshes Rx
// activity for the LOCAL session id — and an MRP retransmit (authentic
// duplicate) counts too, exactly like matter.js MessageExchange.ts:429
// notifying activity before the duplicate branch. Without the duplicate
// half, a controller stuck retransmitting into a slow exchange would be
// reaped as idle while demonstrably alive.
func TestDispatch_AuthenticatedInboundMarksRxActivity(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	const localSessionID uint16 = 7
	bridgeSess, peerSess := activitySessionPair(t, 0x1234)
	lookup := &markingSessionLookup{session: bridgeSess}
	b.AttachSessionLookup(lookup)

	datagram := encryptInboundIMDatagram(t, peerSess, localSessionID)

	if err := b.dispatch(context.Background(), datagram, loopbackSrc()); err != nil {
		t.Fatalf("dispatch (fresh): %v", err)
	}
	rx, tx := lookup.marks()
	if len(rx) != 1 || rx[0] != localSessionID {
		t.Fatalf("rx marks after fresh message = %v, want [%d]", rx, localSessionID)
	}
	// The Read produced a ReportData reply through the outbound
	// chokepoint — every Tx mark must carry the LOCAL session id.
	for _, id := range tx {
		if id != localSessionID {
			t.Fatalf("tx marks = %v, want only the local id %d", tx, localSessionID)
		}
	}

	// Same datagram again: authenticates but is a duplicate — still marks.
	if err := b.dispatch(context.Background(), datagram, loopbackSrc()); err != nil {
		t.Fatalf("dispatch (duplicate): %v", err)
	}
	rx, _ = lookup.marks()
	if len(rx) != 2 {
		t.Fatalf("rx marks after duplicate = %v, want 2 entries (duplicates count as activity)", rx)
	}
}

// TestDispatch_FailedDecryptDoesNotMarkActivity verifies unauthenticated
// traffic cannot keep a session alive: a datagram that fails AEAD
// authentication must not refresh the activity timestamp, or a spoofing
// peer could pin a dead session in memory forever.
func TestDispatch_FailedDecryptDoesNotMarkActivity(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	bridgeSess, peerSess := activitySessionPair(t, 0x1234)
	lookup := &markingSessionLookup{session: bridgeSess}
	b.AttachSessionLookup(lookup)

	datagram := encryptInboundIMDatagram(t, peerSess, 7)
	// Corrupt the last ciphertext byte — AEAD authentication must fail.
	datagram[len(datagram)-1] ^= 0xFF

	if err := b.dispatch(context.Background(), datagram, loopbackSrc()); err == nil {
		t.Fatal("dispatch accepted a corrupted datagram")
	}
	rx, tx := lookup.marks()
	if len(rx) != 0 || len(tx) != 0 {
		t.Fatalf("marks after failed decrypt = rx %v / tx %v, want none", rx, tx)
	}
}

// TestSendReplyOpts_SecureReplyMarksTxActivity verifies the single outbound
// chokepoint ([Bridge.encryptSecureOutbound]): a secure IM reply refreshes
// Tx activity keyed on the BRIDGE-LOCAL session id — not the peer's view
// stamped into the outbound header (matter.js MessageExchange.ts:562).
func TestSendReplyOpts_SecureReplyMarksTxActivity(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	const (
		localSessionID uint16 = 7
		peerSessionID  uint16 = 0x1234
	)
	bridgeSess, _ := activitySessionPair(t, peerSessionID)
	lookup := &markingSessionLookup{session: bridgeSess}
	b.AttachSessionLookup(lookup)

	requestHdr := &message.Header{SessionID: localSessionID, MessageCounter: 42}
	requestProto := message.ProtocolHeader{
		Initiator:  true,
		ExchangeID: 1,
		ProtocolID: im.InteractionModelProtocolID,
	}
	body, err := EncodeStatusResponse(im.StatusResponse{Status: im.StatusSuccess})
	if err != nil {
		t.Fatalf("EncodeStatusResponse: %v", err)
	}
	if err := b.sendReplyOpts(loopbackSrc(), requestHdr, requestProto, im.OpcodeStatusResponse, body, false); err != nil {
		t.Fatalf("sendReplyOpts: %v", err)
	}

	rx, tx := lookup.marks()
	if len(tx) != 1 || tx[0] != localSessionID {
		t.Fatalf("tx marks = %v, want [%d] (local id, not the peer's %#x)", tx, localSessionID, peerSessionID)
	}
	if len(rx) != 0 {
		t.Fatalf("rx marks after outbound-only traffic = %v, want none", rx)
	}
}

// TestSendReplyOpts_EncryptFailureDoesNotMarkTx verifies the failure
// direction: when the seal fails (keys already zeroised — a session racing
// its own teardown), no Tx activity is recorded, so a dying session cannot
// refresh itself out of the reaper's window.
func TestSendReplyOpts_EncryptFailureDoesNotMarkTx(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	bridgeSess, _ := activitySessionPair(t, 0x1234)
	bridgeSess.Close() // zeroise keys → Encrypt must fail
	lookup := &markingSessionLookup{session: bridgeSess}
	b.AttachSessionLookup(lookup)

	requestHdr := &message.Header{SessionID: 7, MessageCounter: 42}
	requestProto := message.ProtocolHeader{ExchangeID: 1, ProtocolID: im.InteractionModelProtocolID}
	err := b.sendReplyOpts(loopbackSrc(), requestHdr, requestProto, im.OpcodeStatusResponse, []byte{0x15, 0x18}, false)
	if err == nil {
		t.Fatal("sendReplyOpts succeeded on a closed session")
	}
	_, tx := lookup.marks()
	if len(tx) != 0 {
		t.Fatalf("tx marks after failed encrypt = %v, want none", tx)
	}
}

// TestOperationalSessionLookup_ActivityMarkers pins the adapter half: the
// WithActivityMarkers closures receive the session id, and an adapter built
// without them degrades to a no-op (the capability type-assertion in
// [Bridge.notifySessionActivity] still matches, so the methods themselves
// must be nil-safe).
func TestOperationalSessionLookup_ActivityMarkers(t *testing.T) {
	t.Parallel()
	var gotRx, gotTx []uint16
	l := NewOperationalSessionLookup(func(uint16) (*channel.Session, bool) { return nil, false }).
		WithActivityMarkers(
			func(id uint16) { gotRx = append(gotRx, id) },
			func(id uint16) { gotTx = append(gotTx, id) },
		)
	l.MarkActiveRx(7)
	l.MarkActiveTx(9)
	if len(gotRx) != 1 || gotRx[0] != 7 {
		t.Errorf("rx closure calls = %v, want [7]", gotRx)
	}
	if len(gotTx) != 1 || gotTx[0] != 9 {
		t.Errorf("tx closure calls = %v, want [9]", gotTx)
	}

	bare := NewOperationalSessionLookup(func(uint16) (*channel.Session, bool) { return nil, false })
	bare.MarkActiveRx(1) // must not panic
	bare.MarkActiveTx(1) // must not panic
}
