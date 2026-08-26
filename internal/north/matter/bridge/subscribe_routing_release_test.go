// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package bridge

// The bridge-side routing tables are keyed by subscription ID and are
// only ever written when a SubscribeRequest succeeds. Every path that
// terminates a subscription in the manager must therefore release
// them, or the entries outlive the subscription for the daemon's whole
// uptime — and resolveSessionPeerAddr ranges over subTargets on every
// graceful session close.

import (
	"net"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im/subscription"
	"github.com/SukramJ/openccu-loom/internal/north/matter/transport/message"
)

// subscribeAndCapture registers one subscription through the manager
// the bridge holds and records its routing target the same way a
// successful SubscribeRequest does.
func subscribeAndCapture(t *testing.T, b *Bridge, m *subscription.Manager, sessionID, endpoint uint16) uint32 {
	t.Helper()
	sub, err := m.Subscribe(subscription.SubscribeArgs{
		FabricIndex:        1,
		PeerNodeID:         0xBEEF,
		SessionID:          sessionID,
		MinIntervalFloor:   1,
		MaxIntervalCeiling: 60,
		AttributePaths: []im.ConcreteAttributePath{{
			Endpoint: endpoint, HasEndpoint: true,
			Cluster: 0x0006, HasCluster: true,
			Attribute: 0x0000, HasAttribute: true,
		}},
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	b.captureSubTarget(
		sub.ID,
		&net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 5555},
		&message.Header{SessionID: sessionID},
		message.ProtocolHeader{ExchangeID: 21, Initiator: true},
		false,
	)
	if _, ok := b.routing.subTargets.Load(sub.ID); !ok {
		t.Fatalf("captureSubTarget did not record subscription %d", sub.ID)
	}
	return sub.ID
}

// TestSubscriptionCloseReleasesBridgeRouting pins that every manager
// close path releases the bridge-side routing entry. The manager is
// handed to the bridge exactly as the daemon does it, and the
// assertion is on the effect (entry gone) rather than on any hook
// being called.
func TestSubscriptionCloseReleasesBridgeRouting(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		close func(m *subscription.Manager, subID uint32)
	}{
		{"Close", func(m *subscription.Manager, subID uint32) { _ = m.Close(subID) }},
		{"CloseSession", func(m *subscription.Manager, _ uint32) { m.CloseSession(9) }},
		{"ClosePeer", func(m *subscription.Manager, _ uint32) { m.ClosePeer(1, 0xBEEF) }},
		{"CloseFabric", func(m *subscription.Manager, _ uint32) { m.CloseFabric(1) }},
		{"CloseFabricExcept", func(m *subscription.Manager, _ uint32) { m.CloseFabricExcept(1, 0) }},
		{"CloseEndpoint", func(m *subscription.Manager, _ uint32) { m.CloseEndpoint(4) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b := newStartedBridge(t)
			m := subscription.NewManager(subscription.Config{}, b.SubscriptionReporter(), nil)
			b.AttachSubscriptionManager(m)

			subID := subscribeAndCapture(t, b, m, 9, 4)
			b.reportCounterOwner.Store(reportCounterKey(9, 4242), subID)

			tc.close(m, subID)

			if _, ok := b.routing.subTargets.Load(subID); ok {
				t.Errorf("subTarget for subscription %d survived %s", subID, tc.name)
			}
			if _, ok := b.reportCounterOwner.Load(reportCounterKey(9, 4242)); ok {
				t.Errorf("reportCounterOwner entry for subscription %d survived %s", subID, tc.name)
			}
		})
	}
}
