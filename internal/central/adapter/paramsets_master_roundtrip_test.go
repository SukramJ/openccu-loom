// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"log/slog"
	"maps"
	"strings"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/north/filter"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/internal/north/rest/ws"
	"github.com/SukramJ/openccu-loom/internal/store/visibility"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// masterRoundTripCCU is a stateful stand-in for one channel's MASTER paramset.
// The hydration reads its descriptor and seed values, the write lands in it,
// and the post-write read-back serves what the write stored — so the whole
// operator round trip (offer → save → re-read → push) runs against one
// consistent device instead of a stub that forgets.
type masterRoundTripCCU struct {
	paramsetFakeOps
	mu    sync.Mutex
	state map[string]any
	wire  []map[string]any
}

// newMasterRoundTripCCU builds the fake for one device with one channel.
func newMasterRoundTripCCU(
	devAddr, chanAddr, model, channelType string,
	desc map[string]hmproto.ParameterData,
	seed map[string]any,
) *masterRoundTripCCU {
	c := &masterRoundTripCCU{state: maps.Clone(seed)}
	c.listDevicesFn = func(context.Context) ([]hmproto.DeviceDescription, error) {
		return []hmproto.DeviceDescription{
			{Address: devAddr, Type: model},
			{Address: chanAddr, Parent: devAddr, Type: channelType},
		}, nil
	}
	c.getParamsetDescriptionFn = func(_ context.Context, addr string, key hmenum.ParamsetKey) (map[string]hmproto.ParameterData, error) {
		if addr == chanAddr && key == hmenum.ParamsetKeyMaster {
			return desc, nil
		}
		return nil, nil
	}
	c.getParamsetFn = func(_ context.Context, addr string, key hmenum.ParamsetKey) (map[string]any, error) {
		if addr != chanAddr || key != hmenum.ParamsetKeyMaster {
			return map[string]any{}, nil
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		return maps.Clone(c.state), nil
	}
	c.putParamsetFn = func(_ context.Context, addr string, key hmenum.ParamsetKey, values map[string]any) error {
		if addr != chanAddr || key != hmenum.ParamsetKeyMaster {
			return nil
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		c.wire = append(c.wire, maps.Clone(values))
		maps.Copy(c.state, values)
		return nil
	}
	return c
}

// written folds every recorded MASTER write into one map, preserving the Go
// type each value carried on the wire.
func (c *masterRoundTripCCU) written() map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := map[string]any{}
	for _, w := range c.wire {
		maps.Copy(out, w)
	}
	return out
}

// TestMasterParamsetWriteSavesAndReachesBothPushPlanes is the operator round
// trip for a channel-configuration change, end to end through the real
// pipeline, the real paramset domain and the real event bridge.
//
// Three properties, one write:
//
//  1. A MASTER parameter the configuration surfaces offer is writable. The
//     read surfaces hand out the channel's full MASTER descriptor, so gating
//     the write on the MASTER data-point-creation whitelist rejected the save
//     of every parameter that whitelist does not name — which is most of them.
//     The write here carries one whitelisted and one un-whitelisted parameter;
//     both must reach the device.
//
//  2. The value is coerced against the descriptor on the way to the CCU. The
//     values arrive as decoded JSON, where every number is a float64, and the
//     XML-RPC encoder maps float64 to <double>; an INTEGER parameter sent that
//     way faults on the device.
//
//  3. The change reaches the MQTT master state topic and the WebSocket stream
//     without a restart. MASTER is a read/write plane with `optimistic: false`
//     entities, so a write that is never re-published leaves the consumer
//     showing the boot value forever.
func TestMasterParamsetWriteSavesAndReachesBothPushPlanes(t *testing.T) {
	t.Parallel()

	const (
		centralName = "ccu-01"
		ifaceID     = "HmIP-RF"
		devAddr     = "0011AABB"
		chanAddr    = devAddr + ":0"
		// HmIP-RGBW channel 0 whitelists DEVICE_OPERATION_MODE for MASTER
		// data-point creation, so that parameter is a declared north-bound
		// entity. ARR_TIMEOUT is not whitelisted anywhere — it is one of the
		// parameters the edit session offers and the write gate refused.
		declared   = string(hmenum.ParameterDeviceOperationMode)
		undeclared = "ARR_TIMEOUT"
	)

	desc := map[string]hmproto.ParameterData{
		declared: {
			Type:       hmenum.ParameterTypeInteger,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
			Min:        []byte(`0`),
			Max:        []byte(`4`),
		},
		undeclared: {
			Type:       hmenum.ParameterTypeInteger,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
			Min:        []byte(`0`),
			Max:        []byte(`100`),
		},
	}
	ccu := newMasterRoundTripCCU(devAddr, chanAddr, "HmIP-RGBW", "RGBW", desc,
		map[string]any{declared: 1, undeclared: 10})

	visReg := visibility.NewRegistry()
	visReg.SetRequiredParameters(custom.DefaultRegistry().RequiredParameters())

	unit, err := central.New(central.Config{Name: centralName})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(unit); err != nil {
		t.Fatalf("register: %v", err)
	}
	unit.MarkSouthboundReady()

	valueWriter := client.NewValueWriter()
	valueWriter.Register(centralName, ifaceID, ccu)

	pipeline := NewDevicePipeline(unit).WithVisibility(visReg)
	if err := pipeline.IngestFromBackend(
		context.Background(), ifaceID, hmenum.InterfaceHmIPRF,
		ccu, valueWriter, nil, slog.Default(),
	); err != nil {
		t.Fatalf("IngestFromBackend: %v", err)
	}

	pub := mqtt.NewNoopClient()
	bridge := mqtt.NewBridge(mqtt.BridgeConfig{
		Base: "openccu-loom", CentralName: centralName, RawEnabled: true,
	}, pub)
	wsHub := ws.NewHub()
	eb := NewEventBridge(reg, wsHub, mqtt.NewWiring(bridge, nil)).
		WithVisibility(filter.NewAdapter(visReg))
	eb.Start(context.Background())
	defer eb.Stop()

	domain := NewParamsetsDomain(reg, valueWriter).SetVisibilityGate(visReg)

	// The save the config surface issues: JSON-decoded numbers, one parameter
	// inside the MASTER data-point whitelist and one outside it.
	if err := domain.PutParamset(
		context.Background(), chanAddr, hmenum.ParamsetKeyMaster,
		map[string]any{declared: float64(3), undeclared: float64(42)},
	); err != nil {
		t.Fatalf("PutParamset(MASTER): %v — a parameter the read surfaces offer must be writable", err)
	}
	eb.Flush()

	wire := ccu.written()
	for _, name := range []string{declared, undeclared} {
		got, ok := wire[name]
		if !ok {
			t.Fatalf("%s never reached the device (wire=%v)", name, wire)
		}
		switch got.(type) {
		case int, int32, int64:
		default:
			t.Errorf("%s reached the device as %T (%v), want an integer — a float travels as <double> "+
				"and the CCU faults on an INTEGER parameter", name, got, got)
		}
	}
	if got := wire[declared]; !isInt(got, 3) {
		t.Errorf("%s on the wire = %v, want 3", declared, got)
	}
	if got := wire[undeclared]; !isInt(got, 42) {
		t.Errorf("%s on the wire = %v, want 42", undeclared, got)
	}

	// MQTT: the retained master slot topic must carry the new value.
	wantSuffix := "/0/master/" + declared
	found := ""
	for _, p := range pub.Published() {
		if strings.HasSuffix(p.Topic, wantSuffix) {
			found = string(p.Payload)
		}
	}
	if found == "" {
		all := make([]string, 0, len(pub.Published()))
		for _, p := range pub.Published() {
			all = append(all, p.Topic)
		}
		t.Fatalf("no publish to a topic ending %q — a MASTER write never reaches the master state topic, "+
			"so the consumer keeps the boot value; published=%v", wantSuffix, all)
	}
	if !strings.Contains(found, `"value":3`) {
		t.Errorf("master slot payload = %s, want value 3", found)
	}

	// WebSocket: the same change must be broadcast so a second session sees it.
	sawWS := false
	for _, e := range wsHub.Replay(0, nil).Events {
		if e.Type != string(hmevent.EventTypeDataPointValueChanged) {
			continue
		}
		p, ok := e.Payload.(ws.DataPointValueChangedPayload)
		if !ok || p.Parameter != declared {
			continue
		}
		if p.ParamsetKey != string(hmenum.ParamsetKeyMaster) {
			t.Errorf("WS paramset_key = %q, want MASTER", p.ParamsetKey)
		}
		if !isInt(p.Value, 3) {
			t.Errorf("WS value = %v, want 3", p.Value)
		}
		sawWS = true
	}
	if !sawWS {
		t.Error("no WebSocket push for the MASTER change — a second session keeps the stale value")
	}
}

// isInt reports whether v holds the integer want in any of the integer
// representations a value can carry between the model and the wire.
func isInt(v any, want int) bool {
	switch n := v.(type) {
	case int:
		return n == want
	case int32:
		return int(n) == want
	case int64:
		return int(n) == want
	case float64:
		return n == float64(want)
	}
	return false
}
