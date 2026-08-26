// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/internal/north/rest/ws"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// TestEventBridgeDeviceRemovedRetractsOnlyTheOwningCentral pins the scope of
// the discovery retraction a device removal triggers.
//
// Device addresses are unique per CCU but repeat verbatim across CCUs: the
// virtual remote, the BidCoS pseudo devices and the INT000* internal devices
// carry the identical address on every one of them. The bridge is
// daemon-global, so an unscoped sweep over its declared map matched the other
// centrals' node ids too and took their live entities off every consumer
// until the next daemon restart republished them.
func TestEventBridgeDeviceRemovedRetractsOnlyTheOwningCentral(t *testing.T) {
	const addr = "HmIP-RCV-1"

	reg := central.NewRegistry()
	for _, name := range []string{"ccu-01", "ccu-02"} {
		c, err := central.New(central.Config{Name: name})
		if err != nil {
			t.Fatalf("central.New(%s): %v", name, err)
		}
		if err := reg.Register(c); err != nil {
			t.Fatalf("register(%s): %v", name, err)
		}
		c.ModelRegistry.Put(device.New(device.Config{
			InterfaceID: "HmIP-RF", Interface: hmenum.InterfaceHmIPRF,
			Address: addr, Model: "HmIP-RCV-50",
		}))
		c.MarkSouthboundReady()
	}

	pub := mqtt.NewNoopClient()
	bridge := mqtt.NewBridge(mqtt.BridgeConfig{
		Base:               "openccu-loom",
		CentralName:        "ccu-01",
		RawEnabled:         true,
		HADiscoveryEnabled: true,
	}, pub)
	ebridge := NewEventBridge(reg, ws.NewHub(), mqtt.NewWiring(bridge, nil))
	ebridge.Start(context.Background())
	defer ebridge.Stop()

	// Declare one discovery config per central for the shared address.
	topics := map[string]string{}
	for _, name := range []string{"ccu-01", "ccu-02"} {
		item := mqtt.DiscoveryItem{
			Component: "button",
			NodeID:    name + "_" + "hmip-rcv-1",
			ObjectID:  "ch1_press_short",
			Payload:   []byte(`{"name":"press"}`),
			OK:        true,
		}
		if err := bridge.PublishHubDiscovery(context.Background(), item); err != nil {
			t.Fatalf("declare discovery for %s: %v", name, err)
		}
		topics[name] = "homeassistant/" + item.Component + "/" + item.NodeID + "/" + item.ObjectID + "/config"
	}

	u, _ := reg.Get("ccu-01")
	events.Publish(u.EventBus, hmevent.DeviceRemovedEvent{
		Base:        hmevent.NewBaseAt(time.Now()),
		CentralName: "ccu-01",
		InterfaceID: "HmIP-RF",
		Address:     addr,
	})
	ebridge.Flush()

	if !lastPayloadEmpty(pub, topics["ccu-01"]) {
		t.Errorf("the removed device's own discovery config %q was not retracted", topics["ccu-01"])
	}
	if lastPayloadEmpty(pub, topics["ccu-02"]) {
		t.Errorf("removal on ccu-01 retracted ccu-02's live discovery config %q", topics["ccu-02"])
	}
}
