// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package bridge

// White-box test for the keepalive half of reportSubscription: an
// ongoing report whose dirty-path set drained to zero attribute reports
// (a max-interval heartbeat with nothing changed) must carry
// SuppressResponse=true on the wire so the peer does not owe an IM
// StatusResponse for a no-op. Lives in package bridge to reach
// reportSubscription and subTargets directly; reuses the real-session
// wire-capture pattern from TestSendInitiatedIM_FreshExchange in
// subscribe_initiated_test.go.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im/subscription"
	"github.com/SukramJ/openccu-loom/internal/north/matter/secure/channel"
	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
	"github.com/SukramJ/openccu-loom/internal/north/matter/transport/message"
)

// decodeReportDataSuppressResponse walks a ReportData IM body's outer
// anonymous struct and returns the boolean value of the
// SuppressResponse field (context tag 4, matches im's
// tagReportSuppressResponse) plus whether the tag was present at all.
// Mirrors the depth-1 walk in tlvSubscribeResponseMaxInterval
// (scenario_tlv_test.go) but reads a bool instead of a uint16.
func decodeReportDataSuppressResponse(body []byte) (value, present bool, err error) {
	if len(body) == 0 {
		return false, false, errors.New("empty IM body")
	}
	dec := tlv.NewDecoder(body)
	first, err := dec.Next()
	if err != nil || !first.IsContainer || first.Type != tlv.TypeStructure {
		return false, false, fmt.Errorf("outer struct: err=%w el=%+v", err, first)
	}
	depth := 1
	for depth > 0 {
		el, err := dec.Next()
		if err != nil {
			return false, false, fmt.Errorf("walk: %w", err)
		}
		if el.IsEndContainer {
			depth--
			continue
		}
		if depth == 1 && el.Tag.Kind == tlv.TagKindContext && el.Tag.Number == 4 && !el.IsContainer {
			return el.Bool, true, nil
		}
		if el.IsContainer {
			depth++
		}
	}
	return false, false, nil
}

// TestReportSubscription_EmptyPathsSetsSuppressResponse verifies that
// reportSubscription, driven with an empty path set (the keepalive case
// — nothing dirty, or every dirty path was dropped by the ACL gate),
// ships a ReportData with SuppressResponse=true. Mirrors matter.js
// ServerSubscription.ts:782, which suppresses the peer's obligation to
// answer a no-op heartbeat with an IM StatusResponse.
func TestReportSubscription_EmptyPathsSetsSuppressResponse(t *testing.T) {
	t.Parallel()

	bridgeKey := bytes.Repeat([]byte{0x11}, 16)
	peerKey := bytes.Repeat([]byte{0x22}, 16)
	const (
		bridgeNodeID uint64 = 0xAAAA1111
		peerNodeID   uint64 = 0xBBBB2222
		sessionID    uint16 = 55
	)
	bridgeSess, err := channel.New(channel.Config{
		EncryptKey:     bridgeKey,
		DecryptKey:     peerKey,
		LocalNodeID:    bridgeNodeID,
		PeerNodeID:     peerNodeID,
		InitialCounter: 500,
	})
	if err != nil {
		t.Fatalf("bridge session: %v", err)
	}
	peerSess, err := channel.New(channel.Config{
		EncryptKey:     peerKey,
		DecryptKey:     bridgeKey,
		LocalNodeID:    peerNodeID,
		PeerNodeID:     bridgeNodeID,
		InitialCounter: 600,
	})
	if err != nil {
		t.Fatalf("peer session: %v", err)
	}

	b := newStartedBridge(t)
	b.AttachSessionLookup(sessionLookupFunc(func(id uint16) (*channel.Session, bool) {
		if id == sessionID {
			return bridgeSess, true
		}
		return nil, false
	}))

	peerConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("peer ListenUDP: %v", err)
	}
	t.Cleanup(func() { _ = peerConn.Close() })
	peerAddr, ok := peerConn.LocalAddr().(*net.UDPAddr)
	if !ok {
		t.Fatalf("unexpected peer addr type %T", peerConn.LocalAddr())
	}

	const subID uint32 = 4242
	b.subTargets.Store(subID, subTarget{
		src:                 peerAddr,
		hasPeerSourceNodeID: true,
		peerSourceNodeID:    peerNodeID,
		exchangeID:          9,
		sessionID:           sessionID,
		peerInitiator:       true,
	})

	// Empty paths: no attribute survived the dirty-set / ACL gate, so
	// report.Reports stays empty — the keepalive branch under test.
	b.reportSubscription(context.Background(), &subscription.Subscription{ID: subID}, nil)

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
	plain, _, err := peerSess.Decrypt(&hdr, securityFlagsByte(&hdr), got[hdrLen:])
	if err != nil {
		t.Fatalf("peer.Decrypt: %v", err)
	}
	proto, protoLen, err := message.UnmarshalProtocolHeader(plain)
	if err != nil {
		t.Fatalf("UnmarshalProtocolHeader: %v", err)
	}
	if proto.Opcode != im.OpcodeReportData {
		t.Fatalf("proto.Opcode = %#x, want OpcodeReportData=%#x", proto.Opcode, im.OpcodeReportData)
	}

	body := plain[protoLen:]
	suppress, present, err := decodeReportDataSuppressResponse(body)
	if err != nil {
		t.Fatalf("decodeReportDataSuppressResponse: %v", err)
	}
	if !present {
		t.Fatal("SuppressResponse tag (context tag 4) absent from the wire ReportData")
	}
	if !suppress {
		t.Error("SuppressResponse = false, want true for an empty-payload keepalive report")
	}
}
