// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// White-box tests for two dormant-capability wirings:
//
//   - Bridge.NotifyDeviceReachable: CCU device-availability flips fire the
//     §9.13.6 BridgedDeviceBasicInformation ReachableChanged event for the
//     matching bridged endpoint(s).
//   - Bridge.ForgetSigma1Replied wired through PerExchangeCaseProvider's
//     evict hook: the TTL reaper cleans up aborted CASE handshakes so the
//     sigma1Replied dedupe map cannot leak.
//
// Lives in package bridge so it can reach the unexported sigma1Replied map
// and the in-package test helpers (NewFakeStore, wbEmptySnapshotter).
package bridge

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/core"
	endpointpkg "github.com/SukramJ/openccu-loom/internal/north/matter/endpoint"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im/subscription"
	matterstore "github.com/SukramJ/openccu-loom/internal/north/matter/store"
)

// newReachabilityBridge builds a started bridge whose topology carries one
// bridged endpoint keyed to (central, address).
func newReachabilityBridge(t *testing.T, ep *endpointpkg.Endpoint) *Bridge {
	t.Helper()
	b, err := New(
		NewFakeStore(),
		wbEmptySnapshotter,
		nil,
		Config{
			Listen:    ":0",
			VendorID:  0x1234,
			ProductID: 0x5678,
			NodeLabel: "reachable-test",
		},
		nil,
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer stopCancel()
		_ = b.Stop(stopCtx)
	})
	if err := b.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	b.mu.Lock()
	b.topology = &endpointpkg.Topology{
		Endpoints: []*endpointpkg.Endpoint{
			{ID: 0, DeviceType: 0x0016}, // root
			{ID: 1, DeviceType: 0x000E}, // aggregator
			ep,
		},
	}
	b.mu.Unlock()
	return b
}

// TestNotifyDeviceReachable_FiresReachableChanged verifies that a CCU
// device going unreachable fires the §9.13.6 ReachableChanged event on the
// matching bridged endpoint with ReachableNewValue=false.
func TestNotifyDeviceReachable_FiresReachableChanged(t *testing.T) {
	t.Parallel()
	ep := &endpointpkg.Endpoint{
		ID:         7,
		DeviceType: 0x010A,
		SourceKey: matterstore.EndpointKey{
			CentralName:   "ccu-1",
			DeviceAddress: "00021BE9957782",
		},
	}
	b := newReachabilityBridge(t, ep)

	b.NotifyDeviceReachable("ccu-1", "00021BE9957782", false)

	records := b.EventLog().Query(0xFFFF, 0xFFFFFFFF, 0xFFFFFFFF, 0)
	if len(records) != 1 {
		t.Fatalf("EventLog: got %d records, want 1", len(records))
	}
	r := records[0]
	if r.Endpoint != 7 {
		t.Errorf("Endpoint=%d, want 7", r.Endpoint)
	}
	if r.Cluster != 0x0039 {
		t.Errorf("Cluster=0x%04X, want 0x0039 (BridgedDeviceBasicInformation)", r.Cluster)
	}
	if r.EventID != 0x0003 {
		t.Errorf("EventID=0x%02X, want 0x0003 (ReachableChanged)", r.EventID)
	}
	evt, ok := r.Payload.(core.ReachableChangedEvent)
	if !ok {
		t.Fatalf("Payload=%T, want core.ReachableChangedEvent", r.Payload)
	}
	if evt.ReachableNewValue {
		t.Error("ReachableNewValue=true, want false")
	}
}

// TestNotifyDeviceReachable_IgnoresNonMatchingDevice verifies that a flip on
// a device that does not back any bridged endpoint fires no event.
func TestNotifyDeviceReachable_IgnoresNonMatchingDevice(t *testing.T) {
	t.Parallel()
	ep := &endpointpkg.Endpoint{
		ID:         7,
		DeviceType: 0x010A,
		SourceKey: matterstore.EndpointKey{
			CentralName:   "ccu-1",
			DeviceAddress: "AAAA",
		},
	}
	b := newReachabilityBridge(t, ep)

	// Different address: no matching endpoint.
	b.NotifyDeviceReachable("ccu-1", "BBBB", false)
	// Different central: no matching endpoint.
	b.NotifyDeviceReachable("ccu-2", "AAAA", false)

	if records := b.EventLog().Query(0xFFFF, 0xFFFFFFFF, 0xFFFFFFFF, 0); len(records) != 0 {
		t.Fatalf("EventLog: got %d records, want 0", len(records))
	}
}

// reachAttrReporterSpy records every dirty-path report the subscription
// engine hands to the [subscription.Reporter] hook, so a test can assert
// which [im.ConcreteAttributePath] values were marked dirty without
// depending on the wire encoding.
type reachAttrReporterSpy struct {
	mu    sync.Mutex
	calls [][]im.ConcreteAttributePath
}

func (s *reachAttrReporterSpy) report(_ context.Context, _ *subscription.Subscription, paths []im.ConcreteAttributePath) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, paths)
}

// TestNotifyDeviceReachable_DirtiesReachableAttribute verifies that, in
// addition to firing the ReachableChanged event, a reachability flip
// marks the BridgedDeviceBasicInformation.Reachable ATTRIBUTE (cluster
// 0x0039, attribute 0x0011) dirty on every subscription covering it —
// so an attribute-only subscriber (e.g. Google Home, which polls the
// attribute rather than tracking the event) sees the new value on the
// next engine tick instead of only on re-subscribe. Mirrors matter.js's
// reactive `reachable` state, where a plain assignment on the
// BridgedDeviceBasicInformationServer dirties the attribute the same way
// an explicit write would.
func TestNotifyDeviceReachable_DirtiesReachableAttribute(t *testing.T) {
	t.Parallel()
	ep := &endpointpkg.Endpoint{
		ID:         7,
		DeviceType: 0x010A,
		SourceKey: matterstore.EndpointKey{
			CentralName:   "ccu-1",
			DeviceAddress: "00021BE9957782",
		},
	}
	b := newReachabilityBridge(t, ep)

	spy := &reachAttrReporterSpy{}
	mgr := subscription.NewManager(subscription.Config{}, spy.report, nil)
	sub, err := mgr.Subscribe(subscription.SubscribeArgs{
		FabricIndex:        0,
		PeerNodeID:         1,
		SessionID:          1,
		MinIntervalFloor:   0,
		MaxIntervalCeiling: 60,
		AttributePaths: []im.ConcreteAttributePath{
			{
				HasEndpoint:  true,
				HasCluster:   true,
				HasAttribute: true,
				Endpoint:     7,
				Cluster:      core.BridgedDeviceBasicInformationClusterID,
				Attribute:    0x0011,
			},
		},
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	b.AttachSubscriptionManager(mgr)

	b.NotifyDeviceReachable("ccu-1", "00021BE9957782", false)

	// Drive the engine synchronously with a wall-clock offset rather than
	// waiting in real time: Manager.Subscribe stamps lastReport=now at
	// admission (so the very first tick after Subscribe never fires a
	// spurious keepalive — see manager.go) and the manager floors
	// MinIntervalFloor to 1s even for a request of 0, so the dirty-path
	// drain gate needs `now` to be at least that far past admission.
	mgr.Tick(context.Background(), time.Now().Add(2*time.Second))

	spy.mu.Lock()
	defer spy.mu.Unlock()
	if len(spy.calls) != 1 {
		t.Fatalf("reporter invocations = %d, want 1 (dirty-path report for subscription %d)", len(spy.calls), sub.ID)
	}
	got := spy.calls[0]
	if len(got) != 1 {
		t.Fatalf("dirty paths = %d, want 1: %+v", len(got), got)
	}
	p := got[0]
	if p.Endpoint != 7 || p.Cluster != core.BridgedDeviceBasicInformationClusterID || p.Attribute != 0x0011 {
		t.Errorf("dirty path = %+v, want {Endpoint:7 Cluster:0x0039 Attribute:0x0011}", p)
	}
}

// TestForgetSigma1Replied_ClearsEntry verifies the exported wrapper drops
// the dedupe entry so an aborted CASE handshake does not leak.
func TestForgetSigma1Replied_ClearsEntry(t *testing.T) {
	t.Parallel()
	b, err := New(
		NewFakeStore(),
		wbEmptySnapshotter,
		nil,
		Config{Listen: ":0", VendorID: 0x1, ProductID: 0x1, NodeLabel: "evict-test"},
		nil,
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Simulate a Sigma1-replied entry for an exchange whose Sigma3 never
	// arrived (aborted handshake).
	b.mu.Lock()
	b.sigma1Replied[42] = [32]byte{0xAB}
	b.mu.Unlock()

	b.ForgetSigma1Replied(42)

	b.mu.Lock()
	_, present := b.sigma1Replied[42]
	b.mu.Unlock()
	if present {
		t.Error("sigma1Replied[42] still present after ForgetSigma1Replied")
	}
}

// TestPerExchangeCaseProvider_EvictHookForgetsSigma1 verifies the reaper's
// eviction hook, when wired to Bridge.ForgetSigma1Replied, removes the
// dedupe entry for the evicted exchange id — closing the aborted-handshake
// leak the dormant audit flagged.
func TestPerExchangeCaseProvider_EvictHookForgetsSigma1(t *testing.T) {
	t.Parallel()
	b, err := New(
		NewFakeStore(),
		wbEmptySnapshotter,
		nil,
		Config{Listen: ":0", VendorID: 0x1, ProductID: 0x1, NodeLabel: "evict-hook-test"},
		nil,
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	b.mu.Lock()
	b.sigma1Replied[99] = [32]byte{0xCD}
	b.mu.Unlock()

	provider := NewPerExchangeCaseProvider(func() *CaseAdapter { return &CaseAdapter{} })
	provider.SetOnEvict(b.ForgetSigma1Replied)
	// Allocate an entry for exchange 99, then Reset evicts every entry and
	// fires the onEvict hook for each — exercising the same path the TTL
	// reaper drives.
	_ = provider.Resolve(99)
	provider.Reset()

	b.mu.Lock()
	_, present := b.sigma1Replied[99]
	b.mu.Unlock()
	if present {
		t.Error("sigma1Replied[99] still present after evict hook fired")
	}
}
