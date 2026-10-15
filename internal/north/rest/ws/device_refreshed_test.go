// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package ws

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// TestDeviceRefreshedSubscriberNilSafe verifies that Start() and Stop()
// with nil reg/hub do not panic.
func TestDeviceRefreshedSubscriberNilSafe(t *testing.T) {
	t.Parallel()
	s := NewDeviceRefreshedSubscriber(nil, nil)
	s.Start()
	s.Stop()
}

// TestDeviceRefreshedPayloadWireShape pins the JSON member names a client
// keys on and the RFC 3339 form of refreshed_at.
func TestDeviceRefreshedPayloadWireShape(t *testing.T) {
	t.Parallel()
	raw, err := json.Marshal(DeviceRefreshedPayload{
		Central: "home", InterfaceID: "HmIP-RF", DeviceAddress: "DEV001",
		RefreshedAt: "2030-05-01T10:00:00.5Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"central": "home", "interface_id": "HmIP-RF", "device_address": "DEV001",
		"refreshed_at": "2030-05-01T10:00:00.5Z",
	}
	if len(m) != len(want) {
		t.Fatalf("payload keys = %v, want %v", m, want)
	}
	for k, v := range want {
		if m[k] != v {
			t.Fatalf("payload[%q] = %q, want %q", k, m[k], v)
		}
	}
}

// TestDeviceRefreshedSubscriberEndToEnd publishes a DeviceRefreshedEvent on
// the central's bus and verifies the hub receives a `device.refreshed`
// frame on the device's lifecycle topic with the observation time in UTC
// RFC 3339.
func TestDeviceRefreshedSubscriberEndToEnd(t *testing.T) {
	t.Parallel()
	hub := NewHub()
	reg := central.NewRegistry()
	cu, err := central.New(central.Config{Name: "test-ccu"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	if err := reg.Register(cu); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}
	sub := NewDeviceRefreshedSubscriber(reg, hub)
	sub.Start()
	t.Cleanup(sub.Stop)

	at := time.Date(2030, 5, 1, 12, 30, 0, 250_000_000, time.FixedZone("CEST", 2*3600))
	cu.EventBus.Publish(hmevent.DeviceRefreshedEvent{
		Base:          hmevent.NewBaseAt(at),
		CentralName:   "test-ccu",
		InterfaceID:   "HmIP-RF",
		DeviceAddress: "DEV001",
		RefreshedAt:   at,
	})

	wantTopic := DeviceLifecycleTopic("DEV001")
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		res := hub.Replay(0, func(topic string) bool { return topic == wantTopic })
		if len(res.Events) == 0 {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		ev := res.Events[0]
		if ev.Type != string(hmevent.EventTypeDeviceRefreshed) {
			t.Fatalf("type = %q, want %q", ev.Type, hmevent.EventTypeDeviceRefreshed)
		}
		if !ev.When.Equal(at) {
			t.Fatalf("when = %v, want %v", ev.When, at)
		}
		p, ok := ev.Payload.(DeviceRefreshedPayload)
		if !ok {
			t.Fatalf("payload type %T, want DeviceRefreshedPayload", ev.Payload)
		}
		if p.Central != "test-ccu" || p.InterfaceID != "HmIP-RF" || p.DeviceAddress != "DEV001" {
			t.Fatalf("payload = %+v", p)
		}
		if want := "2030-05-01T10:30:00.25Z"; p.RefreshedAt != want {
			t.Fatalf("refreshed_at = %q, want %q (UTC, RFC 3339 nano)", p.RefreshedAt, want)
		}
		return
	}
	t.Fatal("DeviceRefreshedEvent did not reach the hub within deadline")
}
