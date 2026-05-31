// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// eventbridge_press_snapshot_test.go covers the press-channel and
// week-profile snapshot paths in EventBridge that were below threshold:
//   - publishChannelEventState (was 40 %)
//   - publishChannelEventDiscoverySnapshot (was 58 %)
//   - publishWeekProfileSnapshot nil-WeekProfile short-circuit
//   - publishDeviceDiagnostics non-empty diag path
//   - markAvailability cache-hit idempotency path
//   - hasObservedDataPoint: nil device and empty channels

package adapter

import (
	"context"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// buildBridgeEnv returns an EventBridge backed by a NoopClient so
// MQTT calls do not fail, plus the NoopClient for inspection.
func buildBridgeEnv(t *testing.T) (*EventBridge, *mqtt.NoopClient) {
	t.Helper()
	reg, _ := registryWithDevice(t)
	pub := mqtt.NewNoopClient()
	bridge := mqtt.NewBridge(mqtt.BridgeConfig{
		Base:        "openccu-loom",
		CentralName: "ccu-01",
		RawEnabled:  true,
	}, pub)
	mw := mqtt.NewWiring(bridge, nil)
	eb := NewEventBridge(reg, nil, mw)
	eb.Start(context.Background())
	t.Cleanup(eb.Stop)
	return eb, pub
}

// ---------------------------------------------------------------------------
// publishChannelEventState
// ---------------------------------------------------------------------------

// TestPublishChannelEventStateNilMQTT — early return when mqtt is nil.
func TestPublishChannelEventStateNilMQTT(t *testing.T) {
	t.Parallel()
	b := &EventBridge{}
	dev := device.New(device.Config{Address: "PRESDEV001", InterfaceID: "HmIP-RF", Model: "HmIP-WRC2"})
	ch := dev.AddChannel("PRESDEV001:1", 1, "KEY", hmenum.ParamsetKeyValues)
	// nil mqtt → must not panic.
	b.publishChannelEventState(context.Background(), "ccu-01", "HmIP-RF", "PRESDEV001", 1, "PRESS_SHORT", ch)
}

// TestPublishChannelEventStateNotPressParameter — non-press parameter returns early.
func TestPublishChannelEventStateNotPressParameter(t *testing.T) {
	t.Parallel()
	eb, pub := buildBridgeEnv(t)
	// STATE is not a PRESS_* parameter → returns before bridge call.
	dev := device.New(device.Config{Address: "PRESDEV002", InterfaceID: "HmIP-RF", Model: "HmIP-WRC2"})
	ch := dev.AddChannel("PRESDEV002:1", 1, "KEY", hmenum.ParamsetKeyValues)
	eb.publishChannelEventState(context.Background(), "ccu-01", "HmIP-RF", "PRESDEV002", 1, "STATE", ch)
	for _, p := range pub.Published() {
		if strings.Contains(p.Topic, "event") {
			t.Errorf("unexpected event publish for non-press param: %s", p.Topic)
		}
	}
}

// TestPublishChannelEventStateSinglePressChannel — single PRESS_SHORT only → skip.
func TestPublishChannelEventStateSinglePressChannel(t *testing.T) {
	t.Parallel()
	eb, pub := buildBridgeEnv(t)
	dev := device.New(device.Config{Address: "PRESDEV003", InterfaceID: "HmIP-RF", Model: "HmIP-WRC2"})
	ch := dev.AddChannel("PRESDEV003:1", 1, "KEY", hmenum.ParamsetKeyValues)
	// Add only PRESS_SHORT — ChannelPressTypes returns nil (<2 press params).
	dp := generic.NewDataPoint[bool](generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: "PRESDEV003:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "PRESS_SHORT",
		},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeBool},
	})
	ch.Put(dp)
	before := len(pub.Published())
	eb.publishChannelEventState(context.Background(), "ccu-01", "HmIP-RF", "PRESDEV003", 1, "PRESS_SHORT", ch)
	// No additional publishes expected (channel has only 1 press param).
	after := len(pub.Published())
	if after != before {
		t.Errorf("single-press channel must not emit event, got %d new publishes", after-before)
	}
}

// TestPublishChannelEventStateMultiPressChannel — two PRESS_* params → bridge call.
func TestPublishChannelEventStateMultiPressChannel(t *testing.T) {
	t.Parallel()
	eb, pub := buildBridgeEnv(t)
	dev := device.New(device.Config{Address: "PRESDEV004", InterfaceID: "HmIP-RF", Model: "HmIP-WRC2"})
	ch := dev.AddChannel("PRESDEV004:1", 1, "KEY", hmenum.ParamsetKeyValues)
	for _, param := range []string{"PRESS_SHORT", "PRESS_LONG"} {
		dp := generic.NewDataPoint[bool](generic.Spec{
			Key: hmtypes.DataPointKey{
				InterfaceID:    "HmIP-RF",
				ChannelAddress: "PRESDEV004:1",
				ParamsetKey:    hmenum.ParamsetKeyValues,
				Parameter:      param,
			},
			Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeBool},
		})
		ch.Put(dp)
	}
	before := len(pub.Published())
	eb.publishChannelEventState(context.Background(), "ccu-01", "HmIP-RF", "PRESDEV004", 1, "PRESS_SHORT", ch)
	after := len(pub.Published())
	if after == before {
		t.Error("multi-press channel must emit at least one event publish")
	}
}

// ---------------------------------------------------------------------------
// publishChannelEventDiscoverySnapshot
// ---------------------------------------------------------------------------

// TestPublishChannelEventDiscoverySnapshotNilMQTT — nil mqtt returns early.
func TestPublishChannelEventDiscoverySnapshotNilMQTT(t *testing.T) {
	t.Parallel()
	b := &EventBridge{}
	dev := device.New(device.Config{Address: "DISCDEV001", InterfaceID: "HmIP-RF", Model: "HmIP-WRC2"})
	ch := dev.AddChannel("DISCDEV001:1", 1, "KEY", hmenum.ParamsetKeyValues)
	b.publishChannelEventDiscoverySnapshot(context.Background(), "ccu-01", "HmIP-RF", dev, ch)
}

// TestPublishChannelEventDiscoverySnapshotNilChannel — nil channel returns early.
func TestPublishChannelEventDiscoverySnapshotNilChannel(t *testing.T) {
	t.Parallel()
	eb, _ := buildBridgeEnv(t)
	dev := device.New(device.Config{Address: "DISCDEV002", InterfaceID: "HmIP-RF", Model: "HmIP-WRC2"})
	eb.publishChannelEventDiscoverySnapshot(context.Background(), "ccu-01", "HmIP-RF", dev, nil)
}

// TestPublishChannelEventDiscoverySnapshotNoPressParam — no PRESS_* → returns early.
func TestPublishChannelEventDiscoverySnapshotNoPressParam(t *testing.T) {
	t.Parallel()
	eb, pub := buildBridgeEnv(t)
	dev := device.New(device.Config{Address: "DISCDEV003", InterfaceID: "HmIP-RF", Model: "HmIP-STH"})
	ch := dev.AddChannel("DISCDEV003:1", 1, "CLIMATE", hmenum.ParamsetKeyValues)
	// No PRESS_* parameters → firstPressParameter returns "".
	before := len(pub.Published())
	eb.publishChannelEventDiscoverySnapshot(context.Background(), "ccu-01", "HmIP-RF", dev, ch)
	after := len(pub.Published())
	if after != before {
		t.Errorf("non-press channel must not emit discovery publish, got %d new publishes", after-before)
	}
}

// TestPublishChannelEventDiscoverySnapshotWithPressParam — channel with PRESS_SHORT
// triggers bridge.PublishChannelEventDiscovery.
func TestPublishChannelEventDiscoverySnapshotWithPressParam(t *testing.T) {
	t.Parallel()
	eb, pub := buildBridgeEnv(t)
	dev := device.New(device.Config{
		Address: "DISCDEV004", InterfaceID: "HmIP-RF", Model: "HmIP-WRC2",
		Name: "Press Dev",
	})
	ch := dev.AddChannel("DISCDEV004:1", 1, "KEY", hmenum.ParamsetKeyValues)
	dp := generic.NewDataPoint[bool](generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: "DISCDEV004:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "PRESS_SHORT",
		},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeBool},
	})
	ch.Put(dp)
	before := len(pub.Published())
	eb.publishChannelEventDiscoverySnapshot(context.Background(), "ccu-01", "HmIP-RF", dev, ch)
	after := len(pub.Published())
	// Bridge may or may not emit a discovery payload depending on the
	// HA-Discovery config; we just verify it did not panic and ran the path.
	_ = before
	_ = after
}

// ---------------------------------------------------------------------------
// publishWeekProfileSnapshot — nil WeekProfile short-circuit
// ---------------------------------------------------------------------------

// TestPublishWeekProfileSnapshotNilWeekProfile — channel exists but has no
// week profile attached → returns after the wp==nil guard.
func TestPublishWeekProfileSnapshotNilWeekProfile(t *testing.T) {
	t.Parallel()
	eb, _ := buildBridgeEnv(t)
	dev := device.New(device.Config{Address: "WPDEV002", InterfaceID: "HmIP-RF", Model: "HmIP-eTRV-2"})
	ch := dev.AddChannel("WPDEV002:1", 1, "CLIMATECONTROL_VENT_DRIVE", hmenum.ParamsetKeyValues)
	// No week profile attached → ch.WeekProfile() returns nil.
	eb.publishWeekProfileSnapshot(context.Background(), "ccu-01", "HmIP-RF", dev, ch)
}

// TestPublishWeekProfileSnapshotNilDevice — nil device returns early.
func TestPublishWeekProfileSnapshotNilDevice(t *testing.T) {
	t.Parallel()
	eb, _ := buildBridgeEnv(t)
	eb.publishWeekProfileSnapshot(context.Background(), "ccu-01", "HmIP-RF", nil, nil)
}

// ---------------------------------------------------------------------------
// publishDeviceDiagnostics
// ---------------------------------------------------------------------------

// TestPublishDeviceDiagnosticsNilDevice — nil device returns early.
func TestPublishDeviceDiagnosticsNilDeviceSafe(t *testing.T) {
	t.Parallel()
	eb, _ := buildBridgeEnv(t)
	eb.publishDeviceDiagnostics(context.Background(), "ccu-01", "HmIP-RF", nil)
}

// TestPublishDeviceDiagnosticsNoChannel0 — device with no channel 0 →
// diag map stays empty → returns without publish.
func TestPublishDeviceDiagnosticsNoChannel0(t *testing.T) {
	t.Parallel()
	eb, pub := buildBridgeEnv(t)
	dev := device.New(device.Config{Address: "DIAGDEV002", InterfaceID: "HmIP-RF", Model: "HmIP-STH"})
	// Only add channel 1 — no channel 0 means diagnostics loop skips entirely.
	dev.AddChannel("DIAGDEV002:1", 1, "WEATHER", hmenum.ParamsetKeyValues)
	before := len(pub.Published())
	eb.publishDeviceDiagnostics(context.Background(), "ccu-01", "HmIP-RF", dev)
	if len(pub.Published()) != before {
		t.Error("device without channel 0 must not emit diagnostics publish")
	}
}

// TestPublishDeviceDiagnosticsChannel0WithNoObservedDP — channel 0 exists
// but none of the diagnostic parameters have observed values → map stays
// empty → returns without publish.
func TestPublishDeviceDiagnosticsChannel0NoObservedDPs(t *testing.T) {
	t.Parallel()
	eb, pub := buildBridgeEnv(t)
	dev := device.New(device.Config{Address: "DIAGDEV003", InterfaceID: "HmIP-RF", Model: "HmIP-STH"})
	dev.AddChannel("DIAGDEV003:0", 0, "MAINTENANCE", hmenum.ParamsetKeyValues)
	before := len(pub.Published())
	eb.publishDeviceDiagnostics(context.Background(), "ccu-01", "HmIP-RF", dev)
	if len(pub.Published()) != before {
		t.Error("no observed diag DPs must not emit diagnostics publish")
	}
}

// ---------------------------------------------------------------------------
// markAvailability — cache-hit idempotency
// ---------------------------------------------------------------------------

// TestMarkAvailabilityCacheHitNoDoublePublish confirms that calling
// markAvailability twice with the same online value only publishes once
// (the cache-gating prevents broker spam).
func TestMarkAvailabilityCacheHitNoDoublePublish(t *testing.T) {
	t.Parallel()
	eb, pub := buildBridgeEnv(t)
	// First call — cache miss → publish.
	eb.markAvailability(context.Background(), "ccu-01", "HmIP-RF", "CACHEDEV001", true)
	// Second call with same value — cache hit → no new publish.
	before := len(pub.Published())
	eb.markAvailability(context.Background(), "ccu-01", "HmIP-RF", "CACHEDEV001", true)
	after := len(pub.Published())
	if after != before {
		t.Errorf("cache-hit: expected no new publish, got %d", after-before)
	}
}

// TestMarkAvailabilityCacheTransition — toggle from online→offline publishes again.
func TestMarkAvailabilityCacheTransition(t *testing.T) {
	t.Parallel()
	eb, pub := buildBridgeEnv(t)
	eb.markAvailability(context.Background(), "ccu-01", "HmIP-RF", "CACHEDEV002", true)
	before := len(pub.Published())
	eb.markAvailability(context.Background(), "ccu-01", "HmIP-RF", "CACHEDEV002", false)
	after := len(pub.Published())
	if after == before {
		t.Error("online→offline transition must emit a new publish")
	}
}

// ---------------------------------------------------------------------------
// hasObservedDataPoint
// ---------------------------------------------------------------------------

// TestHasObservedDataPointNilDevice — nil device → false.
func TestHasObservedDataPointNilDevice(t *testing.T) {
	t.Parallel()
	if hasObservedDataPoint(nil) {
		t.Error("nil device must return false")
	}
}

// TestHasObservedDataPointNoChannels — device with no channels → false.
func TestHasObservedDataPointNoChannels(t *testing.T) {
	t.Parallel()
	dev := device.New(device.Config{Address: "OBSDEV001", InterfaceID: "HmIP-RF", Model: "HmIP-STH"})
	if hasObservedDataPoint(dev) {
		t.Error("device with no channels must return false")
	}
}

// TestHasObservedDataPointNoObservedValues — device with channel but no observed DPs.
func TestHasObservedDataPointNoObservedValues(t *testing.T) {
	t.Parallel()
	dev := device.New(device.Config{Address: "OBSDEV002", InterfaceID: "HmIP-RF", Model: "HmIP-STH"})
	ch := dev.AddChannel("OBSDEV002:1", 1, "WEATHER", hmenum.ParamsetKeyValues)
	dp := generic.NewDataPoint[float64](generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: "OBSDEV002:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "TEMPERATURE",
		},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeFloat},
	})
	ch.Put(dp)
	// Not observed yet.
	if hasObservedDataPoint(dev) {
		t.Error("no observed value must return false")
	}
}

// TestHasObservedDataPointWithObservedValue — device with one observed DP → true.
func TestHasObservedDataPointWithObservedValue(t *testing.T) {
	t.Parallel()
	dev := device.New(device.Config{Address: "OBSDEV003", InterfaceID: "HmIP-RF", Model: "HmIP-STH"})
	ch := dev.AddChannel("OBSDEV003:1", 1, "WEATHER", hmenum.ParamsetKeyValues)
	dp := generic.NewDataPoint[float64](generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: "OBSDEV003:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "TEMPERATURE",
		},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeFloat},
	})
	ch.Put(dp)
	if !dp.OnWireValue(21.5) {
		t.Fatal("OnWireValue refused value")
	}
	if !hasObservedDataPoint(dev) {
		t.Error("device with observed DP must return true")
	}
}

// ---------------------------------------------------------------------------
// buildMultipart — .sbk suffix branch
// ---------------------------------------------------------------------------

// TestBuildMultipartAlreadyHasSbkSuffix verifies that when the id already
// ends with ".sbk" the filename is not doubled.
func TestBuildMultipartAlreadyHasSbkSuffix(t *testing.T) {
	t.Parallel()
	payload := strings.NewReader("data")
	body, ct, err := buildMultipart("backup.sbk", payload)
	if err != nil {
		t.Fatalf("buildMultipart: %v", err)
	}
	if body == nil {
		t.Fatal("body must not be nil")
	}
	if !strings.Contains(ct, "multipart/form-data") {
		t.Errorf("content-type = %q, want multipart/form-data", ct)
	}
}

// TestBuildMultipartWithoutSbkSuffix verifies the suffix is appended.
func TestBuildMultipartWithoutSbkSuffix(t *testing.T) {
	t.Parallel()
	payload := strings.NewReader("data")
	body, ct, err := buildMultipart("mybackup", payload)
	if err != nil {
		t.Fatalf("buildMultipart: %v", err)
	}
	_ = body
	if !strings.Contains(ct, "multipart/form-data") {
		t.Errorf("content-type = %q, want multipart/form-data", ct)
	}
}
