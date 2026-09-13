// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
)

// TestConfiguredLastWillMatchesTheBridgePolicy pins the will the
// composition root puts on CONNECT against the one the bridge's discovery
// runtime derives from its own status topic.
//
// They are built in two places because they have to be: the will belongs to
// CONNECT, and the client is constructed before the bridge exists. That is
// exactly the shape that drifted in the family — two reference bridges
// configure a will whose topic no published entity references, so a hard
// crash leaves every entity available forever, showing the last value it
// ever saw, and a third publishes its availability marker inside Home
// Assistant's own birth tree where nothing reads it. Neither defect is
// visible from the code that contains it. This test is what makes it
// visible here.
//
// It only makes it visible if it reads the will the composition root really
// configures. It used to hand-build one — `mqtt.Will{Topic:
// buildLWTTopic(cfg), Payload: []byte("offline"), Retain: true}` — which is
// the same three literals the production path uses, restated. Finding 8:
// changing the production will's topic, its payload and its Retain flag each
// left this package green, so the test that claims to make the drift visible
// could not see any of it. The will now comes off [northTCPConfig], the
// TCPConfig handed to `mqtt.NewTCPClient`.
func TestConfiguredLastWillMatchesTheBridgePolicy(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{}
	cfg.North.MQTT.TopicBase = "openccu-loom"
	cfg.North.MQTT.BrokerURL = "tcp://127.0.0.1:1"

	bridge := mqtt.NewBridge(mqtt.BridgeConfig{
		Base:               cfg.North.MQTT.TopicBase,
		HADiscoveryEnabled: true,
	}, mqtt.NewNoopClient())

	want, err := bridge.LastWill()
	if err != nil {
		t.Fatalf("LastWill: %v", err)
	}
	will := northTCPConfig(cfg, slog.Default()).Will
	if will == nil {
		t.Fatal("the composition root configures no last will at all: a hard crash then leaves " +
			"every entity available forever, showing the last value it ever saw")
	}
	got := *will
	if got.Topic != want.Topic {
		t.Fatalf("the configured will clears %q, the bridge announces on %q", got.Topic, want.Topic)
	}
	if !bytes.Equal(got.Payload, want.Payload) {
		t.Fatalf("the configured will writes %q, the policy expects %q", got.Payload, want.Payload)
	}
	if got.Retain != want.Retain {
		t.Fatalf("retain = %v, want %v: an unretained will tells a late subscriber nothing", got.Retain, want.Retain)
	}
}
