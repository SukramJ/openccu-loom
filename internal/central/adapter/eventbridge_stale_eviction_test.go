// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// fakeValueLoader is a test double for [device.ValueLoader]. It allows
// tests to control whether GetValue succeeds and what it returns.
type fakeValueLoader struct {
	// getValueFn is called for every GetValue call. When nil the
	// loader returns ("", nil) — a successful empty string (treated as
	// observed by the cache).
	getValueFn    func(ctx context.Context, address string, parameter hmenum.Parameter) (any, error)
	getParamsetFn func(ctx context.Context, address string, key hmenum.ParamsetKey) (map[string]any, error)
}

func (f *fakeValueLoader) GetValue(ctx context.Context, address string, parameter hmenum.Parameter) (any, error) {
	if f.getValueFn != nil {
		return f.getValueFn(ctx, address, parameter)
	}
	return nil, errors.New("fakeValueLoader: not configured")
}

func (f *fakeValueLoader) GetParamset(ctx context.Context, address string, key hmenum.ParamsetKey) (map[string]any, error) {
	if f.getParamsetFn != nil {
		return f.getParamsetFn(ctx, address, key)
	}
	return nil, errors.New("fakeValueLoader: not configured")
}

// buildEvictionTestEnv builds a registry+device+channel with the given
// dp installed and wired to a fake loader. Returns the event bridge and
// the noop publisher.
func buildEvictionTestEnv(
	t *testing.T,
	dp device.ParameterDataPoint,
	loader device.ValueLoader,
) (*EventBridge, *mqtt.NoopClient) {
	t.Helper()
	reg, dev := registryWithDevice(t)
	ch := dev.AddChannel("0001ABCD:1", 1, "TEST", hmenum.ParamsetKeyValues)
	ch.Put(dp)
	if loader != nil {
		dev.SetValueLoader(loader)
	}

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

// TestStaleEviction_ObservedDP_NormalPublish confirms that an already-
// observed DataPoint is published normally (no LoadValue, no evict)
// during PublishInitialSnapshot. The value topic must contain the
// seeded value; no empty-payload eviction must appear.
func TestStaleEviction_ObservedDP_NormalPublish(t *testing.T) {
	t.Parallel()

	dp := generic.NewDataPoint[bool](generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: "0001ABCD:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "STATE",
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
	// Seed a real observed value — no loader needed.
	if !dp.OnWireValue(true) {
		t.Fatalf("OnWireValue rejected seed")
	}

	eb, pub := buildEvictionTestEnv(t, dp, nil /* no loader — observed DPs skip LoadValue */)
	eb.PublishInitialSnapshot(context.Background())

	valuePublishes := 0
	evictions := 0
	for _, p := range pub.Published() {
		if strings.HasSuffix(p.Topic, "/0001ABCD/1/values/STATE") {
			if len(p.Payload) == 0 && p.Retain {
				evictions++
			} else {
				valuePublishes++
			}
		}
	}
	if valuePublishes != 1 {
		t.Fatalf("expected 1 value publish for observed DP, got %d (evictions=%d)", valuePublishes, evictions)
	}
	if evictions != 0 {
		t.Fatalf("expected 0 evictions for observed DP, got %d", evictions)
	}
}

// TestUnobservedDP_NoWireLoad_AvailableSlotState pins two boot-time
// invariants for an unobserved DP:
//
//  1. registerAndLoadDP MUST NOT issue a LoadValue / getValue radio
//     call (boot-time radio budget). A per-DP LoadValue on boot fanned
//     out one radio call per unobserved DP across the whole fleet
//     (thousands on a non-trivial CCU) and drove the CCU DutyCycle into
//     the warning band on every restart. The reference design also
//     skips per-DP wire loads — see [seedRelevantInitParameters] /
//     [seedReadableEvents] for the limited boot loads that ARE permitted.
//  2. The DP's slot state is published with `available:true` and a
//     `null` value (NOT evicted to an empty payload). Availability
//     tracks device reachability, not value observation — a reachable
//     device whose DP has not reported yet is online with an `unknown`
//     value. The previous evict-to-empty design left the entity
//     `unavailable` in HA under `availability_mode: all`.
func TestUnobservedDP_NoWireLoad_AvailableSlotState(t *testing.T) {
	t.Parallel()

	dp := generic.NewDataPoint[bool](generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: "0001ABCD:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "STATE",
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
	// DP intentionally NOT seeded — the loader sentinel below proves no
	// LoadValue happens regardless of whether the loader would have
	// returned an observed value, observed=false, or an error.

	loaderCalled := false
	loader := &fakeValueLoader{
		getValueFn: func(_ context.Context, _ string, _ hmenum.Parameter) (any, error) {
			loaderCalled = true
			return true, nil
		},
	}

	eb, pub := buildEvictionTestEnv(t, dp, loader)
	eb.PublishInitialSnapshot(context.Background())

	if loaderCalled {
		t.Fatal("registerAndLoadDP issued a LoadValue for an unobserved DP — boot-time radio budget violated")
	}

	evictions := 0
	availablePublishes := 0
	for _, p := range pub.Published() {
		if strings.HasSuffix(p.Topic, "/0001ABCD/1/values/STATE") {
			switch {
			case len(p.Payload) == 0 && p.Retain:
				evictions++
			case strings.Contains(string(p.Payload), `"available":true`):
				availablePublishes++
				if !strings.Contains(string(p.Payload), `"value":null`) {
					t.Errorf("unobserved slot state should carry a null value, got %s", p.Payload)
				}
			}
		}
	}
	if evictions != 0 {
		t.Fatalf("expected 0 evictions for unobserved DP, got %d", evictions)
	}
	if availablePublishes != 1 {
		t.Fatalf("expected 1 available:true slot publish for unobserved DP, got %d", availablePublishes)
	}
}

// TestStaleEviction_CalculatedDP_NeitherLoadNorEvict confirms that
// unobserved calculated DPs (DEW_POINT, ENTHALPY, …) never trigger a
// wire LoadValue call (no radio-budget fan-out) and are not evicted.
// They ARE registered in HA discovery with an `unknown` (null) slot
// state — the reference stack registers calculated entities at setup
// regardless of observation, so calc binary_sensors that start
// unobserved still surface in HA. No getValue radio call happens.
func TestStaleEviction_CalculatedDP_NeitherLoadNorEvict(t *testing.T) {
	t.Parallel()

	reg, dev := registryWithDevice(t)
	ch := dev.AddChannel("0001ABCD:1", 1, "TEST", hmenum.ParamsetKeyValues)

	// Attach a calculated DP — intentionally NOT seeded.
	calcDP := generic.NewFloatSensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: "0001ABCD:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "DEW_POINT",
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
			Unit:       "°C",
		},
	})
	ch.AttachCalculatedDataPoint(calcDP)

	// Install a loader that should NOT be called for calculated DPs.
	loaderCalled := false
	dev.SetValueLoader(&fakeValueLoader{
		getValueFn: func(_ context.Context, _ string, _ hmenum.Parameter) (any, error) {
			loaderCalled = true
			return nil, errors.New("should not be called for calculated DP")
		},
	})

	pub := mqtt.NewNoopClient()
	bridge := mqtt.NewBridge(mqtt.BridgeConfig{
		Base:        "openccu-loom",
		CentralName: "ccu-01",
		RawEnabled:  true,
	}, pub)
	mw := mqtt.NewWiring(bridge, nil)
	eb := NewEventBridge(reg, nil, mw)
	eb.Start(context.Background())
	defer eb.Stop()

	eb.PublishInitialSnapshot(context.Background())

	if loaderCalled {
		t.Fatal("LoadValue must NOT be called for calculated DPs")
	}

	// The unobserved calculated DP is registered (unknown slot state on
	// the calculated bucket) but never evicted to an empty payload — the
	// reference stack registers calculated entities at setup. Assert the
	// slot state carries a null value and the available flag, never an
	// empty eviction body.
	registered := false
	for _, p := range pub.Published() {
		if strings.HasSuffix(p.Topic, "/calculated/DEW_POINT") {
			registered = true
			if len(p.Payload) == 0 {
				t.Fatalf("calculated DP slot state must not be an empty eviction body: %+v", p)
			}
			if !strings.Contains(string(p.Payload), `"value":null`) {
				t.Fatalf("unobserved calculated DP must carry a null value: %s", p.Payload)
			}
		}
	}
	if !registered {
		t.Fatal("unobserved calculated DP must be registered with an unknown slot state")
	}
}
