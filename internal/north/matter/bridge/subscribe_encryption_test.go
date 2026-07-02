// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package bridge

import (
	"bytes"
	"net"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/internal/north/matter/secure/channel"
	"github.com/SukramJ/openccu-loom/internal/north/matter/transport/message"
	"github.com/SukramJ/openccu-loom/internal/north/matter/transport/udp"
)

// TestSendUnsolicitedIM_EncryptedRoundTrip verifies that a report
// shipped via sendUnsolicitedIM with sessionID > 0 is AES-CCM-sealed
// correctly: the bytes that hit the wire decrypt cleanly with the
// peer's complementary keys, the protocol header round-trips
// (NeedsAck=true, Initiator=false, ExchangeID echoed from the
// commissioner-opened Subscribe exchange), and the recovered IM
// payload equals the input.
//
// matter.js's `ServerSubscription.#sendUpdateMessage`
// (ServerSubscription.ts:764) opens a fresh exchange for every ongoing
// report; that pattern works against chip-tool but Apple Home's
// MTRDevice times the subscription out 10 s after SubscribeResponse if
// no further ReportData lands on the **original** Subscribe exchange
// (empirically validated). We therefore stay on the
// commissioner-opened exchange for ongoing reports, matching Apple's
// expectation.
//
// Drives sendUnsolicitedIM through a real udp.Listener so the
// encryption / counter / wire-framing path is exercised end-to-end.
func TestSendUnsolicitedIM_EncryptedRoundTrip(t *testing.T) {
	t.Parallel()

	// Two complementary AES-CCM-128 sessions: bridge ↔ peer.
	bridgeKey := bytes.Repeat([]byte{0xAA}, 16)
	peerKey := bytes.Repeat([]byte{0xBB}, 16)
	const (
		bridgeNodeID uint64 = 0xBBBBAAAA
		peerNodeID   uint64 = 0xCCCCDDDD
	)
	bridgeSess, err := channel.New(channel.Config{
		EncryptKey:     bridgeKey,
		DecryptKey:     peerKey,
		LocalNodeID:    bridgeNodeID,
		PeerNodeID:     peerNodeID,
		InitialCounter: 100,
	})
	if err != nil {
		t.Fatalf("bridge session: %v", err)
	}
	peerSess, err := channel.New(channel.Config{
		EncryptKey:     peerKey,
		DecryptKey:     bridgeKey,
		LocalNodeID:    peerNodeID,
		PeerNodeID:     bridgeNodeID,
		InitialCounter: 200,
	})
	if err != nil {
		t.Fatalf("peer session: %v", err)
	}

	// Bridge listener on loopback. The listener owns a UDP socket; we
	// bind on :0 so the OS picks a free port.
	listener, err := udp.New(udp.Config{LocalAddr: "127.0.0.1:0", PreferIPv4: true})
	if err != nil {
		t.Fatalf("udp.New: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	// Peer socket: a separate UDP socket that captures whatever the
	// bridge sends. Closed via t.Cleanup so the test does not leak FDs.
	peerConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("peer ListenUDP: %v", err)
	}
	t.Cleanup(func() { _ = peerConn.Close() })
	peerAddr, ok := peerConn.LocalAddr().(*net.UDPAddr)
	if !ok {
		t.Fatalf("unexpected peer addr type %T", peerConn.LocalAddr())
	}

	const sessionID uint16 = 1
	b := &Bridge{}
	b.listener = listener
	b.sessions = sessionLookupFunc(func(id uint16) (*channel.Session, bool) {
		if id == sessionID {
			return bridgeSess, true
		}
		return nil, false
	})
	b.outboundReliable = newOutboundReliableTracker(nil)

	target := subTarget{
		src:                 peerAddr,
		hasPeerSourceNodeID: true,
		peerSourceNodeID:    peerNodeID,
		exchangeID:          7,
		sessionID:           sessionID,
		peerInitiator:       true,
	}

	payload := []byte{0xDE, 0xAD, 0xBE, 0xEF, 0x42}
	counter, err := b.sendUnsolicitedIM(target, im.OpcodeReportData, payload)
	if err != nil {
		t.Fatalf("sendUnsolicitedIM: %v", err)
	}
	if counter == 0 {
		t.Fatal("sendUnsolicitedIM returned counter=0; want a non-zero tracker counter (NeedsAck mode)")
	}

	// Receive the datagram on the peer socket within a deadline.
	if err := peerConn.SetReadDeadline(time.Now().Add(500 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	buf := make([]byte, 1500)
	n, _, err := peerConn.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("peer ReadFromUDP: %v", err)
	}
	got := buf[:n]

	hdr, hdrLen, err := message.UnmarshalHeader(got)
	if err != nil {
		t.Fatalf("UnmarshalHeader: %v", err)
	}
	if hdr.SessionID != sessionID {
		t.Errorf("hdr.SessionID = %d, want %d", hdr.SessionID, sessionID)
	}
	if hdr.MessageCounter != counter {
		t.Errorf("hdr.MessageCounter = %d, want %d", hdr.MessageCounter, counter)
	}
	if hdr.DestSize != message.DestNodeID || hdr.DestNodeID != target.peerSourceNodeID {
		t.Errorf("dest echo: size=%d node=%x, want size=%d node=%x",
			hdr.DestSize, hdr.DestNodeID, message.DestNodeID, target.peerSourceNodeID)
	}

	body := got[hdrLen:]
	plain, _, err := peerSess.Decrypt(&hdr, securityFlagsByte(&hdr), body)
	if err != nil {
		t.Fatalf("peer.Decrypt: %v", err)
	}
	proto, protoLen, err := message.UnmarshalProtocolHeader(plain)
	if err != nil {
		t.Fatalf("UnmarshalProtocolHeader: %v", err)
	}
	if proto.Opcode != im.OpcodeReportData {
		t.Errorf("proto.Opcode = %#x, want %#x (OpcodeReportData)", proto.Opcode, im.OpcodeReportData)
	}
	if proto.ExchangeID != target.exchangeID {
		t.Errorf("proto.ExchangeID = %d, want %d (sendUnsolicitedIM with peerInitiator=true must echo target.exchangeID — that is the event-report path that stays on the peer's Subscribe exchange; attribute ongoing reports go through sendInitiatedIM on a fresh exchange)",
			proto.ExchangeID, target.exchangeID)
	}
	if proto.Initiator {
		t.Error("proto.Initiator = true; peerInitiator=true target → we are responder on this exchange, want Initiator=false")
	}
	if !proto.NeedsAck {
		t.Error("proto.NeedsAck = false; tracker is wired, want true (reliable mode)")
	}
	if got := plain[protoLen:]; !bytes.Equal(got, payload) {
		t.Errorf("decrypted payload mismatch:\n got=%x\nwant=%x", got, payload)
	}

	// Tracker registered the counter so a subsequent peer ACK could
	// clear it.
	if got := b.outboundReliable.Pending(); got != 1 {
		t.Errorf("outboundReliable.Pending = %d, want 1", got)
	}
	if !b.outboundReliable.Ack(counter) {
		t.Error("Ack on tracked counter returned false")
	}
}

// sessionLookupFunc adapts a function literal to the SessionLookup
// surface so the test wires lookup logic inline.
type sessionLookupFunc func(uint16) (*channel.Session, bool)

func (f sessionLookupFunc) Lookup(id uint16) (*channel.Session, bool) { return f(id) }
