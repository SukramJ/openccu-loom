// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package bridge

// White-box tests for the send-error half of the ongoing-report paths.
// Both reporters must treat a failed send the same way: tolerate a
// transient failure, and only unroute the subscription once the
// consecutive-failure cap is reached. Lives in package bridge to reach
// the reporters and the routing tables directly.

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im/subscription"
)

// oneEventReport is the smallest well-formed event payload the
// reporters accept.
func oneEventReport() []im.EventReport {
	return []im.EventReport{{
		Path: im.ConcreteEventPath{
			HasEndpoint: true, HasCluster: true, HasEvent: true,
			Endpoint: 1, Cluster: 0x003B, Event: 0x01,
		},
		Number:    1,
		Priority:  im.EventPriorityInfo,
		Timestamp: 1_700_000_000_000,
		Data:      im.AttributeValue{Value: uint64(1)},
	}}
}

// TestReportSubscriptionEventsSurvivesTransientSendError pins that a
// single failed event report does not unroute a live subscription. The
// routing entry is written only by captureSubTarget on a fresh
// SubscribeRequest, so dropping it leaves the subscription registered
// and ticked forever while every later report — attribute, event and
// keep-alive alike — returns at the subTargets lookup miss. A session
// being re-adopted is exactly the transient the attribute path already
// tolerates.
func TestReportSubscriptionEventsSurvivesTransientSendError(t *testing.T) {
	t.Parallel()
	const liveSessionID uint16 = 73
	b := newStartedBridge(t)
	peerConn, peerSess, peerAddr := chunkTestPeer(t, b, liveSessionID)

	const subID uint32 = 8383
	// Point the target at a session the lookup does not know: the send
	// fails with ErrUnsolicitedSessionMissing before any datagram leaves.
	b.routing.subTargets.Store(subID, subTarget{
		src:                 peerAddr,
		hasPeerSourceNodeID: true,
		peerSourceNodeID:    0xBBBB4444,
		exchangeID:          14,
		sessionID:           liveSessionID + 100,
		peerInitiator:       true,
	})

	sub := &subscription.Subscription{ID: subID}
	b.reportSubscriptionEvents(context.Background(), sub, oneEventReport())
	if _, ok := b.routing.subTargets.Load(subID); !ok {
		t.Fatal("subTarget deleted after one failed event report; the subscription can never be reported on again")
	}

	// Session re-adopted — the next report must reach the peer.
	b.routing.subTargets.Store(subID, subTarget{
		src:                 peerAddr,
		hasPeerSourceNodeID: true,
		peerSourceNodeID:    0xBBBB4444,
		exchangeID:          15,
		sessionID:           liveSessionID,
		peerInitiator:       true,
	})
	b.reportSubscriptionEvents(context.Background(), sub, oneEventReport())
	if counts, _ := drainReportDataChunks(t, peerConn, peerSess); len(counts) == 0 {
		t.Error("no ReportData reached the peer after the session was re-adopted")
	}
}

// TestReportSubscriptionEventsEvictsAfterSendErrorCap pins the other
// end: a peer that is genuinely gone must still be reaped, on the same
// consecutive-failure cap the attribute path uses.
func TestReportSubscriptionEventsEvictsAfterSendErrorCap(t *testing.T) {
	t.Parallel()
	const sessionID uint16 = 74
	b := newStartedBridge(t)
	_, _, peerAddr := chunkTestPeer(t, b, sessionID)

	const subID uint32 = 8484
	b.routing.subTargets.Store(subID, subTarget{
		src:                 peerAddr,
		hasPeerSourceNodeID: true,
		peerSourceNodeID:    0xBBBB4444,
		exchangeID:          16,
		sessionID:           sessionID + 100, // unknown to the lookup — every send fails
		peerInitiator:       true,
	})

	sub := &subscription.Subscription{ID: subID}
	for attempt := 1; attempt <= sendErrorRetryLimit; attempt++ {
		b.reportSubscriptionEvents(context.Background(), sub, oneEventReport())
		if _, ok := b.routing.subTargets.Load(subID); !ok {
			t.Fatalf("subTarget evicted after %d failure(s), want survival up to the cap of %d", attempt, sendErrorRetryLimit)
		}
	}
	b.reportSubscriptionEvents(context.Background(), sub, oneEventReport())
	if _, ok := b.routing.subTargets.Load(subID); ok {
		t.Errorf("subTarget still routed after %d consecutive failures; an unreachable peer must be reaped", sendErrorRetryLimit+1)
	}
}
