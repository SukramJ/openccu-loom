// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/channelflags"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestBootBuiltMQTTBridgeHonoursHiddenChannels pins the operator-hidden-channel
// gate (G12) through the composition root rather than through a hand-built
// BridgeConfig.
//
// The bridge reads its gate once, at build time. The gate used to be installed
// on the supervisor AFTER wireSharedInfrastructure had already run Start —
// which builds the boot bridge — so the bridge that serves the whole daemon
// lifetime had no gate at all: REST and Matter stopped exposing a hidden
// channel while the MQTT plane kept publishing its state and its HA-Discovery
// config. It only started working after an unrelated `north.mqtt.*` edit
// triggered a Swap, so the same install behaved differently before and after
// any MQTT config change.
func TestBootBuiltMQTTBridgeHonoursHiddenChannels(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	const centralName, hiddenChannel, visibleChannel = "c1", "0001ABCD:3", "0001ABCD:4"

	overlay := channelflags.New()
	overlay.Set(centralName, hiddenChannel, channelflags.Flags{Hidden: true})

	cfg := config.Default()
	// Enabled with no broker URL selects the recording no-op client, so the
	// boot path builds a complete bridge with no network involved.
	cfg.North.MQTT.Enabled = true
	cfg.North.MQTT.BrokerURL = ""
	cfg.North.MQTT.RawEnabled = true
	cfg.North.MQTT.DiscoveryEnabled = true

	si, teardown := wireSharedInfrastructure(
		ctx, cfg, discardTestLogger(), central.NewRegistry(), &reloadDeps{}, overlay,
	)
	defer teardown()

	bridge := si.mqttWiring.Bridge()
	if bridge == nil {
		t.Fatal("boot did not build an MQTT bridge")
	}

	si.mqttSup.mu.Lock()
	swap := si.mqttSup.current
	si.mqttSup.mu.Unlock()
	if swap == nil {
		t.Fatal("MQTT supervisor has no active stack after Start")
	}
	noop, ok := swap.client.(*mqtt.NoopClient)
	if !ok {
		t.Fatalf("expected the recording no-op client, got %T", swap.client)
	}

	publish := func(channelAddress string, channelNo int) {
		t.Helper()
		if err := bridge.PublishState(ctx, mqtt.Event{
			Central:        centralName,
			Interface:      "HmIP-RF",
			DeviceAddress:  "0001ABCD",
			ChannelAddress: channelAddress,
			ChannelNo:      channelNo,
			Parameter:      "STATE",
			Category:       hmenum.DataPointCategorySwitch,
			Value:          true,
		}); err != nil {
			t.Fatalf("PublishState(%s): %v", channelAddress, err)
		}
	}

	publish(hiddenChannel, 3)
	if pubs := noop.Published(); len(pubs) != 0 {
		topics := make([]string, 0, len(pubs))
		for _, p := range pubs {
			topics = append(topics, p.Topic)
		}
		t.Fatalf("hidden channel produced %d publish(es): %v", len(pubs), topics)
	}

	// The gate must be a gate, not a mute: a channel the operator did not
	// hide still publishes.
	publish(visibleChannel, 4)
	if n := len(noop.Published()); n == 0 {
		t.Fatal("visible channel produced no publish — the gate is dropping everything")
	}
}
