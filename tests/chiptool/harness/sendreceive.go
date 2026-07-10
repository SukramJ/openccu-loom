// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build chiptool

package harness

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// defaultSendReceiveTimeout bounds each SEND/RECEIVE cell's chip-tool
// round trip when the caller does not pin a tighter or looser value.
const defaultSendReceiveTimeout = 20 * time.Second

// changeSettleDelay is how long AwaitProactiveReport waits after firing the
// device-originated change before it subscribes, so the daemon has processed
// the CCU event and updated the projected attribute value before chip-tool
// reads it.
const changeSettleDelay = 1500 * time.Millisecond

// AwaitProactiveReport is the RECEIVE-direction primitive: it fires a
// simulated device-originated change, lets the daemon propagate it, then
// subscribes and blocks until a report satisfying want() arrives.
//
// chip-tool's `subscribe` in this harness is a one-shot Subscribe-Init: it
// establishes the subscription, ships the current value once, and exits — it
// does NOT stay alive to receive a report from a change that lands afterwards.
// So the change is fired FIRST; the Subscribe-Init report then reflects the
// propagated value. This validates that a CCU value change reaches the Matter
// projection and is correctly encoded per DP type (the read-reflection half of
// the receive direction). The proactive-push half — that the change-notifier
// actually dirty-marks the attribute — is covered separately by the model-layer
// notifier unit tests and the source-walking contract test, which chip-tool's
// one-shot subscribe cannot exercise.
//
// Pick an inject value distinct from the attribute's pre-state so a stale
// pre-change value cannot satisfy want().
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
	if err := inject(); err != nil {
		return "", fmt.Errorf("inject device-originated change: %w", err)
	}
	select {
	case <-time.After(changeSettleDelay):
	case <-ctx.Done():
		return "", ctx.Err()
	}
	return ctl.SubscribeAndAwait(ctx, t, cluster, attr, endpointID, 0, 5, want, timeout)
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
