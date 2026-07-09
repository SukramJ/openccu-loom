// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build chiptool

package harness

import (
	"context"
	"testing"
	"time"
)

// defaultSendReceiveTimeout bounds each SEND/RECEIVE cell's chip-tool
// round trip when the caller does not pin a tighter or looser value.
const defaultSendReceiveTimeout = 20 * time.Second

// subscribeSettleDelay is how long AwaitProactiveReport waits after
// launching the subscribe before it fires the device-originated change,
// so the subscription is live (and its initial report already shipped)
// when the change lands. Long enough to beat chip-tool subscribe
// establishment, short relative to the cell timeout.
const subscribeSettleDelay = 3 * time.Second

// AwaitProactiveReport is the RECEIVE-direction primitive that actually
// exercises the bridge's change-notifier. It subscribes to cluster/attr
// FIRST, then — once the subscription is live and its initial report has
// shipped — fires inject() to drive a device-originated value change, and
// blocks until a PROACTIVE report satisfying want() arrives (or the
// timeout fires).
//
// Ordering matters: firing the change BEFORE subscribing would let the
// subscribe's own initial read reflect the new value, so want() would
// match even if the notifier never pushed a proactive report — the exact
// gap that hid a bridged dimmer's/thermostat's external changes from a
// controller. To catch that class of regression the change must land
// AFTER the subscription exists, so the only path to a matching report is
// the notifier dirty-marking the attribute. Pick an inject value distinct
// from the attribute's pre-state so the initial report cannot pre-satisfy
// want().
func AwaitProactiveReport(
	ctx context.Context, t *testing.T, ctl *Controller,
	cluster, attr string, endpointID uint16,
	inject func() error,
	want func(out string) bool,
	timeout time.Duration,
) (string, error) {
	t.Helper()
	if timeout == 0 {
		timeout = defaultSendReceiveTimeout
	}
	go func() {
		select {
		case <-time.After(subscribeSettleDelay):
		case <-ctx.Done():
			return
		}
		if err := inject(); err != nil {
			t.Logf("AwaitProactiveReport: inject failed: %v", err)
		}
	}()
	// maxInterval is set well beyond the timeout so a heartbeat report
	// never satisfies want() before the injected change does; minInterval
	// 0 lets the on-change report ship as fast as the daemon produces it.
	maxIntervalSec := int(timeout/time.Second) + 60
	return ctl.SubscribeAndAwait(ctx, t, cluster, attr, endpointID, 0, maxIntervalSec, want, timeout)
}

// SendReceiveCase is the shared per-cluster SEND/RECEIVE skeleton
// table-driven cluster test files build on top of, so each such file
// can stay a thin table of cases instead of hand-rolling endpoint
// discovery + CCU cross-referencing for every cluster.
//
// [SendReceiveCase.Run] resolves the cluster's bridged endpoint plus
// its CCU-side (address, dp_key) once via
// [Bridge.CCUAddressForCluster], then drives up to two independent
// t.Run sub-tests:
//
//   - "send"    — Op issues the chip-tool WRITE/INVOKE; SendAssert
//     then reads the CCU-side ground truth via [MockCCU.GetDPValue]
//     and asserts the write actually landed south.
//   - "receive" — RecvInject fires a simulated device-originated push
//     via [MockCCU.FireDeviceEvent]; RecvAssert then blocks on
//     [Controller.SubscribeAndAwait] (or
//     [Controller.SubscribeEventAndAwait]) until the report reaches
//     chip-tool.
//
// Either direction may be left nil to skip that cell — a read-only
// measurement cluster has no SEND direction, a write-only command has
// no RECEIVE direction. Each cell runs as its own t.Run so a SEND
// failure never hides a RECEIVE regression in the same case.
type SendReceiveCase struct {
	// Name labels the case's t.Run sub-test.
	Name string

	// ClusterID selects the endpoint via
	// [Bridge.CCUAddressForCluster] — the first bridged endpoint
	// whose ServerList advertises this cluster and that resolves to
	// a CCU address.
	ClusterID uint16

	// Op issues the chip-tool write/invoke for the SEND direction.
	// Nil together with SendAssert skips the "send" sub-test.
	Op func(ctx context.Context, t *testing.T, ctl *Controller, endpointID uint16)

	// SendAssert runs after Op and asserts the write landed on the
	// CCU side — typically via ccu.GetDPValue(address, dpKey).
	SendAssert func(t *testing.T, ccu *MockCCU, address, dpKey string)

	// RecvInject fires the simulated device-originated push for the
	// RECEIVE direction — typically via
	// ccu.FireDeviceEvent(address, dpKey, someValue). Nil together
	// with RecvAssert skips the "receive" sub-test.
	RecvInject func(t *testing.T, ccu *MockCCU, address, dpKey string)

	// RecvAssert runs after RecvInject and asserts the report reached
	// chip-tool — typically via
	// ctl.SubscribeAndAwait/SubscribeEventAndAwait.
	RecvAssert func(ctx context.Context, t *testing.T, ctl *Controller, endpointID uint16)

	// Timeout bounds each cell's chip-tool round trip. Zero picks
	// [defaultSendReceiveTimeout].
	Timeout time.Duration
}

// Run resolves the case's endpoint/CCU-address pair once against b,
// then executes the SEND and RECEIVE cells the case populated.
func (sc SendReceiveCase) Run(ctx context.Context, t *testing.T, b *Bridge) {
	t.Helper()
	t.Run(sc.Name, func(t *testing.T) {
		t.Helper()

		endpointID, address, dpKey, ok := b.CCUAddressForCluster(ctx, t, sc.ClusterID)
		if !ok {
			t.Skipf("no bridged endpoint resolves cluster 0x%04X to a CCU address", sc.ClusterID)
		}

		timeout := sc.Timeout
		if timeout == 0 {
			timeout = defaultSendReceiveTimeout
		}
		ctl := b.SharedCtl
		if ctl == nil {
			t.Skip("no shared controller commissioned")
		}

		if sc.Op != nil || sc.SendAssert != nil {
			t.Run("send", func(t *testing.T) {
				t.Helper()
				cctx, cancel := context.WithTimeout(ctx, timeout)
				defer cancel()
				if sc.Op != nil {
					sc.Op(cctx, t, ctl, endpointID)
				}
				if sc.SendAssert != nil {
					sc.SendAssert(t, b.CCU, address, dpKey)
				}
			})
		}

		if sc.RecvInject != nil || sc.RecvAssert != nil {
			t.Run("receive", func(t *testing.T) {
				t.Helper()
				cctx, cancel := context.WithTimeout(ctx, timeout)
				defer cancel()
				if sc.RecvInject != nil {
					sc.RecvInject(t, b.CCU, address, dpKey)
				}
				if sc.RecvAssert != nil {
					sc.RecvAssert(cctx, t, ctl, endpointID)
				}
			})
		}
	})
}
