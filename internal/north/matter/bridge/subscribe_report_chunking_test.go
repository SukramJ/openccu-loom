// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package bridge

// White-box test for the chunking half of the ongoing-report path:
// reportSubscription and reportSubscriptionEvents must split a report
// that does not fit one datagram, the same way the Subscribe-Initial
// and plain-read paths do. Lives in package bridge to reach
// reportSubscription, the routing tables and the topology recipes
// directly.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	endpointpkg "github.com/SukramJ/openccu-loom/internal/north/matter/endpoint"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im/subscription"
	"github.com/SukramJ/openccu-loom/internal/north/matter/secure/channel"
	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
	"github.com/SukramJ/openccu-loom/internal/north/matter/transport/message"
	"github.com/SukramJ/openccu-loom/internal/north/matter/transport/udp"
)

// reportDataChunkInfo walks a wire ReportData body and returns the
// number of AttributeReport entries it carries plus its
// MoreChunkedMessages flag (context tag 3, matches im's
// tagReportMoreChunkedMessages). Same depth-walk shape as
// decodeReportDataSuppressResponse in
// subscribe_report_keepalive_test.go.
func reportDataChunkInfo(body []byte) (reports int, more bool, err error) {
	if len(body) == 0 {
		return 0, false, errors.New("empty IM body")
	}
	dec := tlv.NewDecoder(body)
	first, err := dec.Next()
	if err != nil || !first.IsContainer || first.Type != tlv.TypeStructure {
		return 0, false, fmt.Errorf("outer struct: err=%w el=%+v", err, first)
	}
	depth := 1
	reportsDepth := -1
	for depth > 0 {
		el, err := dec.Next()
		if err != nil {
			return 0, false, fmt.Errorf("walk: %w", err)
		}
		if el.IsEndContainer {
			if depth == reportsDepth {
				reportsDepth = -1
			}
			depth--
			continue
		}
		if depth == 1 && el.Tag.Kind == tlv.TagKindContext && el.Tag.Number == 3 && !el.IsContainer {
			more = el.Bool
		}
		if !el.IsContainer {
			continue
		}
		if depth == reportsDepth {
			reports++
		}
		isReportsArray := depth == 1 && el.Tag.Kind == tlv.TagKindContext && el.Tag.Number == 1
		depth++
		if isReportsArray {
			reportsDepth = depth
		}
	}
	return reports, more, nil
}

// newStartedBridgeWithSnapshotter is newStartedBridge with a caller-
// supplied topology source, so a test can reassemble against one of
// the scenario recipes instead of the empty fixture.
func newStartedBridgeWithSnapshotter(t *testing.T, snap Snapshotter) *Bridge {
	t.Helper()
	b, err := New(
		NewFakeStore(),
		snap,
		nil,
		Config{
			Listen:              ":0",
			VendorID:            0x1234,
			ProductID:           0x5678,
			NodeLabel:           "chunk-test",
			IncludeMeasurements: true,
		},
		nil,
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := b.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer stopCancel()
		_ = b.Stop(stopCtx)
	})
	return b
}

// chunkTestPeer wires a CASE session pair plus a peer UDP socket onto
// b and returns the peer socket, the peer's session (for decrypting
// what the bridge sends) and the peer address.
func chunkTestPeer(t *testing.T, b *Bridge, sessionID uint16) (*net.UDPConn, *channel.Session, *net.UDPAddr) {
	t.Helper()
	bridgeKey := bytes.Repeat([]byte{0x33}, 16)
	peerKey := bytes.Repeat([]byte{0x44}, 16)
	const (
		bridgeNodeID uint64 = 0xAAAA3333
		peerNodeID   uint64 = 0xBBBB4444
	)
	bridgeSess, err := channel.New(channel.Config{
		EncryptKey:     bridgeKey,
		DecryptKey:     peerKey,
		LocalNodeID:    bridgeNodeID,
		PeerNodeID:     peerNodeID,
		InitialCounter: 700,
	})
	if err != nil {
		t.Fatalf("bridge session: %v", err)
	}
	peerSess, err := channel.New(channel.Config{
		EncryptKey:     peerKey,
		DecryptKey:     bridgeKey,
		LocalNodeID:    peerNodeID,
		PeerNodeID:     bridgeNodeID,
		InitialCounter: 800,
	})
	if err != nil {
		t.Fatalf("peer session: %v", err)
	}
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
	return peerConn, peerSess, peerAddr
}

// drainReportDataChunks reads every datagram the bridge sent until the
// socket goes quiet, decrypts each one and returns the per-chunk
// (attributeReports, moreChunkedMessages) pairs in wire order.
func drainReportDataChunks(t *testing.T, peerConn *net.UDPConn, peerSess *channel.Session) (reportCounts []int, moreFlags []bool) {
	t.Helper()
	buf := make([]byte, udp.MaxDatagramSize)
	for {
		if err := peerConn.SetReadDeadline(time.Now().Add(300 * time.Millisecond)); err != nil {
			t.Fatalf("SetReadDeadline: %v", err)
		}
		n, _, err := peerConn.ReadFromUDP(buf)
		if err != nil {
			return reportCounts, moreFlags
		}
		hdr, hdrLen, err := message.UnmarshalHeader(buf[:n])
		if err != nil {
			t.Fatalf("UnmarshalHeader: %v", err)
		}
		plain, _, err := peerSess.Decrypt(&hdr, securityFlagsByte(&hdr), buf[hdrLen:n])
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
		count, more, err := reportDataChunkInfo(plain[protoLen:])
		if err != nil {
			t.Fatalf("reportDataChunkInfo: %v", err)
		}
		reportCounts = append(reportCounts, count)
		moreFlags = append(moreFlags, more)
	}
}

// TestReportSubscriptionChunksAnOversizedOngoingReport pins that an
// ongoing subscription report larger than one datagram is split into
// chunks instead of handed to the listener whole. udp.Listener.Send
// rejects anything above [udp.MaxDatagramSize] outright, and the
// engine has already drained (and cleared) the dirty set before the
// reporter runs — an unchunked oversized report therefore loses every
// change in it silently, and a path that never changes again stays
// stale on the controller forever. The trigger is routine: a CCU
// reconnect re-marks every wire data point, so one dirty path per
// bridged endpoint accumulates before the next MinInterval drain.
// matter.js chunks ongoing updates through the same messenger as the
// initial report (packages/protocol/src/interaction/
// InteractionMessenger.ts:347 sendDataReport).
func TestReportSubscriptionChunksAnOversizedOngoingReport(t *testing.T) {
	t.Parallel()
	const sessionID uint16 = 71
	b := newStartedBridgeWithSnapshotter(t, buildManyTempSensorsTopology().snapshotter)
	peerConn, peerSess, peerAddr := chunkTestPeer(t, b, sessionID)

	// Every reportable path of every bridged endpoint — the set a
	// reconnect-wide value refresh produces.
	var paths []im.ConcreteAttributePath
	for _, ep := range b.Topology().Endpoints {
		if ep == nil || ep.IsRoot() || ep.IsAggregator() {
			continue
		}
		paths = append(paths, endpointpkg.ReportablePaths(ep)...)
	}
	if len(paths) < 100 {
		t.Fatalf("topology yielded %d reportable paths, want >=100 so the report cannot fit one datagram", len(paths))
	}

	const subID uint32 = 8181
	b.routing.subTargets.Store(subID, subTarget{
		src:                 peerAddr,
		hasPeerSourceNodeID: true,
		peerSourceNodeID:    0xBBBB4444,
		exchangeID:          12,
		sessionID:           sessionID,
		peerInitiator:       true,
	})

	b.reportSubscription(context.Background(), &subscription.Subscription{ID: subID}, paths)

	counts, moreFlags := drainReportDataChunks(t, peerConn, peerSess)
	if len(counts) < 2 {
		t.Fatalf("peer received %d ReportData datagram(s) for %d paths; want >=2 chunks (an oversized report reached the listener unchunked and was dropped)", len(counts), len(paths))
	}
	total := 0
	for _, c := range counts {
		total += c
	}
	if total < len(paths) {
		t.Errorf("chunks carried %d attribute reports for %d requested paths; reports were lost in the split", total, len(paths))
	}
	for i, more := range moreFlags {
		want := i < len(moreFlags)-1
		if more != want {
			t.Errorf("chunk %d/%d: MoreChunkedMessages = %v, want %v", i, len(moreFlags), more, want)
		}
	}
}

// TestReportSubscriptionEventsChunksAnOversizedReport is the event-side
// twin: drainEventsIfElapsed has already emptied the pending queue when
// the reporter runs, so an oversized event report is lost the same way
// — and its send-error branch additionally unroutes the subscription
// permanently by deleting the subTarget on the first failure.
func TestReportSubscriptionEventsChunksAnOversizedReport(t *testing.T) {
	t.Parallel()
	const sessionID uint16 = 72
	b := newStartedBridge(t)
	peerConn, peerSess, peerAddr := chunkTestPeer(t, b, sessionID)

	const subID uint32 = 8282
	b.routing.subTargets.Store(subID, subTarget{
		src:                 peerAddr,
		hasPeerSourceNodeID: true,
		peerSourceNodeID:    0xBBBB4444,
		exchangeID:          13,
		sessionID:           sessionID,
		peerInitiator:       true,
	})

	events := make([]im.EventReport, 0, 120)
	for i := range 120 {
		events = append(events, im.EventReport{
			Path: im.ConcreteEventPath{
				HasEndpoint: true, HasCluster: true, HasEvent: true,
				Endpoint: uint16(i + 1), Cluster: 0x003B, Event: 0x01,
			},
			Number:    uint64(i + 1),
			Priority:  im.EventPriorityInfo,
			Timestamp: 1_700_000_000_000,
			Data:      im.AttributeValue{Value: uint64(i)},
		})
	}

	b.reportSubscriptionEvents(context.Background(), &subscription.Subscription{ID: subID}, events)

	counts, _ := drainReportDataChunks(t, peerConn, peerSess)
	if len(counts) < 2 {
		t.Fatalf("peer received %d ReportData datagram(s) for %d events; want >=2 chunks", len(counts), len(events))
	}
	if _, ok := b.routing.subTargets.Load(subID); !ok {
		t.Error("subTarget was deleted; a chunked event report must not be treated as a send failure")
	}
}
