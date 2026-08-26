// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"encoding/json"
	"testing"
)

// daemonStatusPayload builds the daemon-status discovery payload and
// decodes it.
func daemonStatusPayload(t *testing.T, central string) map[string]any {
	t.Helper()
	db := newHubBuilder()
	db.SetHubInfoFor(central, HubInfo{Serial: "3014F711A0001234"})
	item := db.BuildDaemonStatusDiscovery(central)
	if !item.OK {
		t.Fatal("daemon-status discovery not built")
	}
	var body map[string]any
	if err := json.Unmarshal(item.Payload, &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return body
}

// TestDaemonStatusSensorCarriesNoAvailability is the whole point of the
// entity. Every other hub entity lists the bridge status as its
// availability source, which is right for them: their data goes stale when
// the daemon dies. This one reports that death, so pointing its
// availability at the same topic it reads its state from would make it go
// `unavailable` in exactly the situation it exists to report — and a
// disconnect would once again be visible only as an absence, which is the
// state #591 describes.
func TestDaemonStatusSensorCarriesNoAvailability(t *testing.T) {
	t.Parallel()
	body := daemonStatusPayload(t, "Haus CCÜ")

	if _, ok := body["availability"]; ok {
		t.Fatal("daemon-status sensor declares an availability block — it would render unavailable instead of off when the daemon dies")
	}
	if _, ok := body["availability_topic"]; ok {
		t.Fatal("daemon-status sensor declares availability_topic — same failure as an availability block")
	}
}

// TestDaemonStatusSensorReadsTheTopicTheWillIsSetOn pins the sensor to the
// bridge status topic and to the exact payloads written to it. The state
// side of this entity has no publisher of its own: AnnounceOnline, the
// offline announce and the broker's last will are the only writers, and
// all three are in different files from the builder. A payload word
// changed on either side leaves a sensor that never turns on, or one that
// never turns off.
func TestDaemonStatusSensorReadsTheTopicTheWillIsSetOn(t *testing.T) {
	t.Parallel()
	const central = "Haus CCÜ"
	body := daemonStatusPayload(t, central)

	topics := NewTopicBuilder("openccu-loom")
	if got, want := body["state_topic"], topics.BridgeStatus(); got != want {
		t.Fatalf("state_topic = %v, want %v", got, want)
	}

	// The payload words are compared against what the real announce calls
	// put on the wire, not against literals — a literal on both sides
	// would move together with a rename and prove nothing.
	rec := newObservedPlane()
	bridge := NewBridge(BridgeConfig{Base: "openccu-loom", CentralName: central}, rec)
	ctx := context.Background()
	if err := bridge.AnnounceOnline(ctx); err != nil {
		t.Fatalf("announce online: %v", err)
	}
	online := lastPayloadOn(t, rec, topics.BridgeStatus())
	if err := bridge.AnnounceOffline(ctx); err != nil {
		t.Fatalf("announce offline: %v", err)
	}
	offline := lastPayloadOn(t, rec, topics.BridgeStatus())

	if got := body["payload_on"]; got != online {
		t.Fatalf("payload_on = %v, but the bridge announces %q — the sensor would never turn on", got, online)
	}
	if got := body["payload_off"]; got != offline {
		t.Fatalf("payload_off = %v, but the bridge announces %q — the sensor would never turn off", got, offline)
	}
	if online == offline {
		t.Fatal("online and offline announce the same payload; the comparison above cannot distinguish them")
	}
}

// TestDaemonStatusSensorIsADiagnosticConnectivityBinarySensor pins the
// shape Home Assistant needs to render it as a connection indicator
// rather than an anonymous on/off box.
func TestDaemonStatusSensorIsADiagnosticConnectivityBinarySensor(t *testing.T) {
	t.Parallel()
	item := func() DiscoveryItem {
		db := newHubBuilder()
		db.SetHubInfoFor("ccu-01", HubInfo{Serial: "3014F711A0001234"})
		return db.BuildDaemonStatusDiscovery("ccu-01")
	}()
	if item.Component != string(HAComponentBinarySensor) {
		t.Fatalf("component = %q, want binary_sensor", item.Component)
	}
	body := daemonStatusPayload(t, "ccu-01")
	if got := body["device_class"]; got != "connectivity" {
		t.Fatalf("device_class = %v, want connectivity", got)
	}
	if got := body["entity_category"]; got != "diagnostic" {
		t.Fatalf("entity_category = %v, want diagnostic", got)
	}
	if body["device"] == nil {
		t.Fatal("no device block — the sensor would not attach to the CCU's card")
	}
}

// lastPayloadOn returns the most recent payload the plane wrote to topic.
func lastPayloadOn(t *testing.T, o *observedPlane, topic string) string {
	t.Helper()
	out := ""
	found := false
	for _, rec := range o.records() {
		if rec.topic == topic {
			out = rec.payload
			found = true
		}
	}
	if !found {
		t.Fatalf("nothing was published on %q", topic)
	}
	return out
}
