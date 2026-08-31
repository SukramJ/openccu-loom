// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// diagnosticsParamsWireSpellings is the guard's own anchor: the channel-0
// parameter names the fixture device puts on the wire, written here as plain
// strings so neither the fixture nor the expectation reads the enumeration
// the production code now ranges over. Both spelling pairs the CCU families
// use are present, which is the whole point of the aggregate.
var diagnosticsParamsWireSpellings = []string{
	"RSSI_DEVICE", "RSSI_PEER",
	"DUTY_CYCLE", "DUTYCYCLE",
	"LOW_BAT", "LOWBAT",
	"UNREACH", "STICKY_UNREACH",
	"CONFIG_PENDING", "UPDATE_PENDING",
}

// TestDeviceDiagnosticsCoversEveryChannel0Parameter pins the retained
// `<addr>/diagnostics` aggregate to [hmenum.DeviceChannel0Parameters].
//
// The event bridge used to carry its own eight-name list, so a device whose
// channel 0 spells the battery or duty-cycle parameter the other way had those
// readings published on the granular per-parameter topics but silently missing
// from the aggregate that claims to summarise them. The observation here comes
// from the production snapshot path (adapter.NewEventBridge → Start →
// PublishCentralSnapshot → the retained diagnostics topic); the expectation
// comes from the wire spellings the fixture itself declares.
func TestDeviceDiagnosticsCoversEveryChannel0Parameter(t *testing.T) {
	t.Parallel()
	const (
		centralName = "ccu-01"
		interfaceID = "HmIP-RF"
		address     = "00DIAGPARM1"
		topicBase   = "openccu-loom"
	)
	ctx := context.Background()

	// The enumeration is the classification the daemon must honour; the
	// fixture is the wire. If a member gains or loses a spelling without
	// this guard's wire list following, the two no longer describe the same
	// concept and the guard is measuring the wrong device.
	declared := make([]string, 0, len(hmenum.DeviceChannel0Parameters))
	for p := range hmenum.DeviceChannel0Parameters {
		declared = append(declared, string(p))
	}
	if !diagnosticsParamsSameSet(declared, diagnosticsParamsWireSpellings) {
		// Reported, not fatal: the effect assertion below is the half that
		// shows what a divergence actually costs on the published topic.
		t.Errorf("hmenum.DeviceChannel0Parameters = %v, guard fixture carries %v — update the fixture so the guard still drives every declared parameter",
			diagnosticsParamsSorted(declared), diagnosticsParamsSorted(diagnosticsParamsWireSpellings))
	}

	c, err := central.New(central.Config{Name: centralName})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("registry.Register: %v", err)
	}
	d := device.New(device.Config{
		Address:     address,
		Interface:   hmenum.InterfaceHmIPRF,
		InterfaceID: interfaceID,
		Model:       "HmIP-BSM",
	})
	ch0 := d.AddChannel(address+":0", 0, "MAINTENANCE", hmenum.ParamsetKeyValues)
	for i, param := range diagnosticsParamsWireSpellings {
		dp := generic.NewInteger(generic.Spec{
			Key: hmtypes.DataPointKey{
				InterfaceID:    interfaceID,
				ChannelAddress: ch0.Address,
				ParamsetKey:    hmenum.ParamsetKeyValues,
				Parameter:      param,
			},
			Descriptor: hmproto.ParameterData{
				Type:       hmenum.ParameterTypeInteger,
				Operations: hmenum.OperationsRead,
			},
		})
		if !dp.OnWireValue(int32(i)) {
			t.Fatalf("OnWireValue(%s): rejected, the guard never observes a value", param)
		}
		ch0.Put(dp)
	}
	c.ModelRegistry.Put(d)
	// The snapshot walk skips a central that has not latched its southbound
	// bring-up, which is the production gate — not a shortcut.
	c.MarkSouthboundReady()

	client := mqtt.NewNoopClient()
	bridge := mqtt.NewBridge(mqtt.BridgeConfig{
		Base: topicBase, CentralName: centralName, RawEnabled: true,
	}, client)
	eb := adapter.NewEventBridge(reg, nil, mqtt.NewWiring(bridge, nil))
	t.Cleanup(eb.Stop)
	eb.Start(ctx)
	eb.PublishCentralSnapshot(ctx, centralName)

	want := mqtt.NewTopicBuilder(topicBase).DeviceDiagnostics(centralName, interfaceID, address)
	var body []byte
	for _, pub := range client.Published() {
		if pub.Topic == want {
			body = pub.Payload
		}
	}
	if body == nil {
		// Without this the whole guard could pass by observing nothing.
		t.Fatalf("no publication on %s — the diagnostics aggregate never ran", want)
	}
	var diag map[string]any
	if err := json.Unmarshal(body, &diag); err != nil {
		t.Fatalf("diagnostics body %q: %v", body, err)
	}
	for _, param := range diagnosticsParamsWireSpellings {
		key := strings.ToLower(param)
		if _, ok := diag[key]; !ok {
			t.Errorf("channel 0 carries an observed %s, diagnostics aggregate has no %q key (body %s)", param, key, body)
		}
	}
	for key := range diag {
		if !diagnosticsParamsHasKey(diagnosticsParamsWireSpellings, key) {
			t.Errorf("diagnostics aggregate carries key %q that no channel-0 parameter on the fixture produces (body %s)", key, body)
		}
	}
}

func diagnosticsParamsSorted(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

func diagnosticsParamsSameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[string]struct{}, len(a))
	for _, s := range a {
		seen[s] = struct{}{}
	}
	for _, s := range b {
		if _, ok := seen[s]; !ok {
			return false
		}
	}
	return true
}

func diagnosticsParamsHasKey(params []string, key string) bool {
	for _, p := range params {
		if strings.EqualFold(p, key) {
			return true
		}
	}
	return false
}
