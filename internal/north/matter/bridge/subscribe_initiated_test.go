// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

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

// TestSendInitiatedReport_FreshExchange locks the F4 fix: ongoing attribute
// reports must travel on a fresh bridge-initiated exchange, not the
// commissioner's Subscribe exchange. Mirrors matter.js
// packages/node/src/node/server/ServerSubscription.ts:764 and
// packages/protocol/src/protocol/ExchangeManager.ts:130-139.
//
// Invariants verified:
//   - proto.ExchangeID != target.exchangeID (fresh, not the Subscribe exchange)
//   - proto.Initiator == true (we opened the exchange)
//   - freshExchangeID in (0, 0x7FFF]
//   - returned freshExchangeID matches proto.ExchangeID
func TestSendInitiatedReport_FreshExchange(t *testing.T) {
	t.Parallel()

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

	listener, err := udp.New(udp.Config{LocalAddr: "127.0.0.1:0", PreferIPv4: true})
	if err != nil {
		t.Fatalf("udp.New: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

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

	const peerExchangeID uint16 = 7
	target := subTarget{
		src:                 peerAddr,
		hasPeerSourceNodeID: true,
		peerSourceNodeID:    peerNodeID,
		exchangeID:          peerExchangeID,
		sessionID:           sessionID,
		peerInitiator:       true,
	}

	report := im.ReportData{
		HasSubscription: true,
		SubscriptionID:  0x99,
		Reports: []im.AttributeReport{{
			Path:        im.ConcreteAttributePath{Endpoint: 3, Cluster: 0x0006, Attribute: 0x0000},
			DataVersion: 12,
			Value:       im.AttributeValue{Value: true},
		}},
	}
	payload, err := EncodeReportData(report)
	if err != nil {
		t.Fatalf("EncodeReportData: %v", err)
	}
	counters, freshExchangeID, err := b.sendInitiatedReport(target, report)
	if err != nil {
		t.Fatalf("sendInitiatedReport: %v", err)
	}

	if freshExchangeID == 0 || freshExchangeID > 0x7FFF {
		t.Errorf("freshExchangeID = %d; want 0 < id <= 0x7FFF", freshExchangeID)
	}
	if freshExchangeID == peerExchangeID {
		t.Errorf("freshExchangeID = %d equals target.exchangeID; must be different", freshExchangeID)
	}
	if len(counters) != 1 {
		t.Fatalf("sendInitiatedReport returned %d counters; want 1 (single-chunk report, NeedsAck mode)", len(counters))
	}

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

	body := got[hdrLen:]
	plain, _, err := peerSess.Decrypt(&hdr, securityFlagsByte(&hdr), body)
	if err != nil {
		t.Fatalf("peer.Decrypt: %v", err)
	}
	proto, protoLen, err := message.UnmarshalProtocolHeader(plain)
	if err != nil {
		t.Fatalf("UnmarshalProtocolHeader: %v", err)
	}

	if proto.ExchangeID != freshExchangeID {
		t.Errorf("proto.ExchangeID = %d, want freshExchangeID=%d (returned value must match wire)", proto.ExchangeID, freshExchangeID)
	}
	if proto.ExchangeID == peerExchangeID {
		t.Errorf("proto.ExchangeID = %d equals target.exchangeID; must be a fresh exchange", proto.ExchangeID)
	}
	if !proto.Initiator {
		t.Error("proto.Initiator = false; bridge must be the initiator on the fresh exchange")
	}
	if proto.Opcode != im.OpcodeReportData {
		t.Errorf("proto.Opcode = %#x, want OpcodeReportData=%#x", proto.Opcode, im.OpcodeReportData)
	}
	if decrypted := plain[protoLen:]; !bytes.Equal(decrypted, payload) {
		t.Errorf("payload mismatch:\n got=%x\nwant=%x", decrypted, payload)
	}
}

// TestSendInitiatedReport_MonotonicallyIncreasing verifies that successive
// calls produce strictly increasing exchange IDs, matching
// matter.js packages/protocol/src/protocol/ExchangeManager.ts:130-139
// which atomically increments and masks to 15 bits.
func TestSendInitiatedReport_MonotonicallyIncreasing(t *testing.T) {
	t.Parallel()

	bridgeKey := bytes.Repeat([]byte{0xCC}, 16)
	peerKey := bytes.Repeat([]byte{0xDD}, 16)
	const (
		bridgeNodeID uint64 = 0x1111AAAA
		peerNodeID   uint64 = 0x2222BBBB
	)
	bridgeSess, err := channel.New(channel.Config{
		EncryptKey:     bridgeKey,
		DecryptKey:     peerKey,
		LocalNodeID:    bridgeNodeID,
		PeerNodeID:     peerNodeID,
		InitialCounter: 300,
	})
	if err != nil {
		t.Fatalf("bridge session: %v", err)
	}

	listener, err := udp.New(udp.Config{LocalAddr: "127.0.0.1:0", PreferIPv4: true})
	if err != nil {
		t.Fatalf("udp.New: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	peerConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("peer ListenUDP: %v", err)
	}
	t.Cleanup(func() { _ = peerConn.Close() })
	peerAddr, ok := peerConn.LocalAddr().(*net.UDPAddr)
	if !ok {
		t.Fatalf("unexpected peer addr type %T", peerConn.LocalAddr())
	}

	const sessionID uint16 = 2
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
		src:           peerAddr,
		exchangeID:    42,
		sessionID:     sessionID,
		peerInitiator: true,
	}

	payload := im.ReportData{HasSubscription: true, SubscriptionID: 1}

	_, id1, err := b.sendInitiatedReport(target, payload)
	if err != nil {
		t.Fatalf("sendInitiatedReport call#1: %v", err)
	}
	_, id2, err := b.sendInitiatedReport(target, payload)
	if err != nil {
		t.Fatalf("sendInitiatedReport call#2: %v", err)
	}

	if id2 <= id1 {
		t.Errorf("exchange IDs not monotonically increasing: call#1=%d call#2=%d", id1, id2)
	}
	if id1 == 0 || id1 > 0x7FFF {
		t.Errorf("id1=%d out of 15-bit range (0, 0x7FFF]", id1)
	}
	if id2 == 0 || id2 > 0x7FFF {
		t.Errorf("id2=%d out of 15-bit range (0, 0x7FFF]", id2)
	}
}
