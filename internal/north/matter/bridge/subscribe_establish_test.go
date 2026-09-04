// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// White-box tests locking in the acceptance side of the Subscribe
// matched-path gate (subscribe.go handleSubscribeRequest,
// subscribe_dispatch.go buildInitialReport): a Subscribe that matches
// at least one path establishes normally (initial ReportData +
// SubscribeResponse, subscription registered), and — the regression
// this file exists to guard — an all-cached re-subscribe (every
// matched cluster suppressed by a DataVersionFilter) still establishes
// rather than being misclassified as "matched nothing". The matched
// count buildInitialReport returns is taken BEFORE DataVersionFilter
// suppression specifically so this case does not misfire; see
// subscribe_dispatch.go:26-29 and matter.js
// ServerSubscription.ts:610-614. Companion to subscribe_reject_test.go
// (the rejection side of the same gate).

package bridge

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im/subscription"
	"github.com/SukramJ/openccu-loom/internal/north/matter/transport/message"
)

// readSubscribeOpcode reads one datagram off peerConn and returns its
// IM opcode. On a StatusResponse it also logs the decoded status code
// so a misrouted-to-rejection failure is diagnosable from the test
// output without a second run.
func readSubscribeOpcode(t *testing.T, peerConn *net.UDPConn) uint8 {
	t.Helper()
	_ = peerConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	rbuf := make([]byte, 1500)
	n, _, err := peerConn.ReadFromUDP(rbuf)
	if err != nil {
		t.Fatalf("ReadFromUDP: %v", err)
	}
	got := rbuf[:n]
	_, hdrLen, err := message.UnmarshalHeader(got)
	if err != nil {
		t.Fatalf("UnmarshalHeader: %v", err)
	}
	rproto, protoLen, err := message.UnmarshalProtocolHeader(got[hdrLen:])
	if err != nil {
		t.Fatalf("UnmarshalProtocolHeader: %v", err)
	}
	if rproto.Opcode == im.OpcodeStatusResponse {
		t.Logf("readSubscribeOpcode: got StatusResponse, status=%v", decodeStatusResponseCode(t, got[hdrLen+protoLen:]))
	}
	return rproto.Opcode
}

// TestHandleSubscribeRequest_MatchingPath_EstablishesAndReplies is the
// positive control for the matched-path gate: a Subscribe naming a
// real (endpoint, cluster, attribute) — AccessControl.ACL on the root
// endpoint, mounted via newACLTestBridge the same way
// subscribe_acl_test.go exercises readAuthorizedResults — matches
// exactly one path and must establish: initial ReportData chunk, then
// SubscribeResponse, with the subscription registered in the manager.
func TestHandleSubscribeRequest_MatchingPath_EstablishesAndReplies(t *testing.T) {
	t.Parallel()
	fake := &aclStoreFake{} // PASE (SessionID=0) bypasses ACL entirely; empty entries is fine.
	b := newACLTestBridge(t, fake)
	mgr := subscription.NewManager(subscription.Config{}, nil, nil)
	b.AttachSubscriptionManager(mgr)

	peerConn, peerAddr := newSubscribeTestPeer(t)
	hdr := &message.Header{SessionID: 0, MessageCounter: 102}
	proto := message.ProtocolHeader{ProtocolID: im.InteractionModelProtocolID, Opcode: im.OpcodeSubscribeRequest, ExchangeID: 1}
	req := im.SubscribeRequest{
		AttributeRequests:  []im.ConcreteAttributePath{accessControlACLPath()},
		MinIntervalFloor:   0,
		MaxIntervalCeiling: 60,
	}

	if err := b.handleSubscribeRequest(context.Background(), peerAddr, hdr, proto, req); err != nil {
		t.Fatalf("handleSubscribeRequest: %v", err)
	}

	if op := readSubscribeOpcode(t, peerConn); op != im.OpcodeReportData {
		t.Fatalf("first reply opcode = 0x%02X, want ReportData (0x%02X)", op, im.OpcodeReportData)
	}
	if op := readSubscribeOpcode(t, peerConn); op != im.OpcodeSubscribeResponse {
		t.Fatalf("second reply opcode = 0x%02X, want SubscribeResponse (0x%02X)", op, im.OpcodeSubscribeResponse)
	}
	if n := mgr.Active(); n != 1 {
		t.Errorf("mgr.Active() = %d, want 1 — a Subscribe matching a real path must register", n)
	}
}

// TestHandleSubscribeRequest_DataVersionFilterFullSuppression_StillEstablishes
// is the regression guard for the "matched BEFORE suppression" ordering
// in buildInitialReport: a Subscribe whose only matched path is fully
// suppressed by a DataVersionFilter (the controller's cached version
// equals the cluster's current version, so the attribute is omitted
// from the initial report) must still establish — an all-cached
// re-subscribe is legitimate, not a "matches nothing" Subscribe.
// Mirrors matter.js ServerSubscription.ts:610-614: the matched-path
// count that gates rejection is computed from path resolution, not from
// what survives DataVersionFilter filtering.
//
// Constructed hermetically: mounts the real cluster/core.AccessControl
// server (which embeds mattercontract.DataVersionTracker, matching production
// wiring) via newACLTestBridge, reads its actual current DataVersion
// through readAuthorizedResults, then subscribes with a
// DataVersionFilter that names that exact version — reproducing
// exactly what a real controller's cache-hit re-subscribe looks like on
// the wire.
func TestHandleSubscribeRequest_DataVersionFilterFullSuppression_StillEstablishes(t *testing.T) {
	t.Parallel()
	fake := &aclStoreFake{}
	b := newACLTestBridge(t, fake)
	mgr := subscription.NewManager(subscription.Config{}, nil, nil)
	b.AttachSubscriptionManager(mgr)

	path := accessControlACLPath()
	results := b.readAuthorizedResults(context.Background(), b.Dispatcher(), path)
	if len(results) != 1 || results[0].Status != im.StatusSuccess {
		t.Fatalf("precondition: want exactly 1 successful read of AccessControl.ACL, got %+v", results)
	}
	dv := results[0].DataVersion
	// mattercontract.DataVersionTracker seeds a uniformly-random non-zero
	// uint32 (see pkg/hmtypes/dataversion.go); the DataVersionFilter
	// suppression guard in buildInitialReport only fires for
	// DataVersion > 1 (the sentinel floor for clusters without
	// per-instance tracking). A value <= 1 here would mean the random
	// seed degenerated or AccessControl stopped tracking its own
	// DataVersion — either way the test cannot exercise suppression and
	// must fail loudly rather than silently pass on an empty report for
	// the wrong reason.
	if dv <= 1 {
		t.Fatalf("precondition: AccessControl DataVersion = %d, want > 1", dv)
	}

	peerConn, peerAddr := newSubscribeTestPeer(t)
	hdr := &message.Header{SessionID: 0, MessageCounter: 103}
	proto := message.ProtocolHeader{ProtocolID: im.InteractionModelProtocolID, Opcode: im.OpcodeSubscribeRequest, ExchangeID: 1}
	req := im.SubscribeRequest{
		AttributeRequests: []im.ConcreteAttributePath{path},
		DataVersionFilters: []im.DataVersionFilter{
			{Endpoint: path.Endpoint, Cluster: path.Cluster, DataVersion: dv},
		},
		MinIntervalFloor:   0,
		MaxIntervalCeiling: 60,
	}

	if err := b.handleSubscribeRequest(context.Background(), peerAddr, hdr, proto, req); err != nil {
		t.Fatalf("handleSubscribeRequest: %v", err)
	}

	// Must still be ReportData (suppressed to zero AttributeReports) then
	// SubscribeResponse — NOT StatusResponse(InvalidAction). If
	// buildInitialReport's matched count were computed AFTER suppression
	// instead of before, this all-cached re-subscribe would misfire into
	// rejectSubscribeInvalidAction here.
	if op := readSubscribeOpcode(t, peerConn); op != im.OpcodeReportData {
		t.Fatalf("first reply opcode = 0x%02X, want ReportData (0x%02X) — an all-cached re-subscribe must still establish", op, im.OpcodeReportData)
	}
	if op := readSubscribeOpcode(t, peerConn); op != im.OpcodeSubscribeResponse {
		t.Fatalf("second reply opcode = 0x%02X, want SubscribeResponse (0x%02X)", op, im.OpcodeSubscribeResponse)
	}
	if n := mgr.Active(); n != 1 {
		t.Errorf("mgr.Active() = %d, want 1 — a fully-suppressed but matched Subscribe must still register", n)
	}
}
