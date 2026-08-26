// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// sweepRetainedConfig runs the discovery orphan sweep for centralName and
// hands it one retained config while its snapshot window is open, the way a
// broker replays its retained store to a fresh subscriber. It reports whether
// the sweep retracted that topic.
func sweepRetainedConfig(t *testing.T, b *mqtt.Bridge, pub *mqtt.NoopClient, centralName, topic string) bool {
	t.Helper()
	const window = 300 * time.Millisecond
	before := len(pub.Published())
	done := make(chan error, 1)
	go func() {
		_, err := b.RunDiscoveryOrphanCleanupOnce(context.Background(), centralName, window)
		done <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for !pub.DeliverInboundRetained("homeassistant/#", topic, []byte(`{"unique_id":"loom_0123456789_sysvar_gone"}`)) {
		if time.Now().After(deadline) {
			t.Fatal("the sweep never installed its homeassistant/# subscription")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := <-done; err != nil {
		t.Fatalf("RunDiscoveryOrphanCleanupOnce: %v", err)
	}
	for _, p := range pub.Published()[before:] {
		if p.Topic == topic && p.Retain && len(p.Payload) == 0 {
			return true
		}
	}
	return false
}

// TestHubPublisherDeclaresItsPlaneToTheOrphanSweep pins the effect of the hub
// publisher's declaration on the retained-orphan sweep: before the publisher
// has run, the sweep must leave the central's hub configs alone; after it has
// published its pass, the same leftover is evictable again.
//
// Without the declaration the sweep judges hub node ids against a `declared`
// map the hub plane has not filled yet — it publishes only once the CCU serial
// resolves, well after the device snapshot that triggers the sweep — and every
// sysvar, program, install-mode and system entity of the previous boot is
// retracted seconds before this boot re-announces it. In Home Assistant that
// empty retained config deletes the entity together with the registry entry
// carrying the operator's name, area and entity_id.
func TestHubPublisherDeclaresItsPlaneToTheOrphanSweep(t *testing.T) {
	t.Parallel()

	const centralName = "ccu-01"
	c, err := central.New(central.Config{Name: centralName})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}
	pub := mqtt.NewNoopClient()
	bridge := mqtt.NewBridge(mqtt.BridgeConfig{
		Base:               "openccu-loom",
		CentralName:        centralName,
		RawEnabled:         true,
		HADiscoveryEnabled: true,
	}, pub)
	publisher := NewHubMQTTPublisher(reg, mqtt.NewWiring(bridge, nil), nil)

	// The serial resolves during the readiness-gated bring-up; it gates every
	// hub payload, so the plane declares nothing before it lands.
	c.SetSystemInformation(central.SystemInfo{Serial: "3014F711A0001F0123456789"})
	c.HubModel.PutSysvar(&hub.Sysvar{
		HubDataPoint: hub.HubDataPoint{Name: "Anwesenheit"},
		ValueType:    hmenum.HubValueTypeLogic,
	})

	leftover := "homeassistant/sensor/ccu-01_sysvars/from_last_boot/config"
	if sweepRetainedConfig(t, bridge, pub, centralName, leftover) {
		t.Errorf("the sweep retracted %s before the hub publisher ran", leftover)
	}

	publisher.Start(context.Background())
	defer publisher.Stop()
	publisher.Flush()

	if !sweepRetainedConfig(t, bridge, pub, centralName, leftover) {
		t.Errorf("after the hub publisher's pass the sweep still refuses to evict %s, so hub orphans can never be cleaned", leftover)
	}
}
