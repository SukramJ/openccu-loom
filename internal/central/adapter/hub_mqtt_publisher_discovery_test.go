// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/coordinators"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// hubDiscoveryFixture mirrors [hubMQTTFixture] but turns HA discovery ON so the
// serial-gated hub-discovery plane is exercised (raw state alone is not
// serial-gated and would hide the regression). The central name carries an
// upper-case segment so the test also pins the safeLower node-id slug.
func hubDiscoveryFixture(t *testing.T) (
	c *central.Unit,
	pub *mqtt.NoopClient,
	publisher *HubMQTTPublisher,
) {
	t.Helper()
	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}
	pub = mqtt.NewNoopClient()
	bridge := mqtt.NewBridge(mqtt.BridgeConfig{
		Base:               "openccu-loom",
		CentralName:        "ccu-01",
		RawEnabled:         true,
		HADiscoveryEnabled: true,
	}, pub)
	wiring := mqtt.NewWiring(bridge, nil)
	publisher = NewHubMQTTPublisher(reg, wiring, nil)
	return c, pub, publisher
}

// discoveryConfigFor returns the parsed JSON body of the first published
// discovery config topic (…/config) whose topic contains every marker, or nil.
func discoveryConfigFor(pub *mqtt.NoopClient, markers ...string) map[string]any {
	for _, p := range pub.Published() {
		if !strings.HasSuffix(p.Topic, "/config") {
			continue
		}
		hit := true
		for _, m := range markers {
			if !strings.Contains(p.Topic, m) {
				hit = false
				break
			}
		}
		if !hit {
			continue
		}
		var body map[string]any
		if err := json.Unmarshal(p.Payload, &body); err != nil {
			return nil
		}
		return body
	}
	return nil
}

// TestHubDiscoveryUsesSerialFromCentralSystemInfo is the regression guard for
// the prod defect where HA showed every device's parent as an "unknown device"
// and sysvar assignments were invisible: the hub-discovery plane was never
// published because the CCU serial that gates it never reached the discovery
// builder. The serial DOES resolve into the central's SystemInformation (raw
// sysvar state publishes fine), so the publisher must take the serial from the
// registry's SystemInformation — not from a separately-timed SetHubInfoFor —
// when it builds hub discovery.
//
// Before the fix the discovery builder carries no serial (nothing stamps it in
// this wiring), hubSerial() gates every Build*Discovery to OK=false, and no
// homeassistant/… hub config is published at all — reproducing the prod
// symptom.
func TestHubDiscoveryUsesSerialFromCentralSystemInfo(t *testing.T) {
	t.Parallel()
	c, pub, publisher := hubDiscoveryFixture(t)

	// The serial resolves during (async) bring-up; model it as already present
	// on SystemInformation, exactly the post-ready state.
	c.SetSystemInformation(central.SystemInfo{
		Model:   "HomeMatic Central",
		Version: "3.79.6",
		Serial:  "3014F711A0001F5A4993D962", // canonical last-10 -> 5a4993d962
	})

	// One unlinked read-only LOGIC sysvar -> read-only binary_sensor whose
	// device block is the synthetic central hub card (hubDeviceBlock), i.e. the
	// payload that NAMES openccu-loom_central_ccu-01 and cures the "unknown
	// device" parent.
	sv := &hub.Sysvar{HubDataPoint: hub.HubDataPoint{Name: "Anwesenheit"}, ValueType: hmenum.HubValueTypeLogic}
	c.HubModel.PutSysvar(sv)

	publisher.Start(context.Background())
	defer publisher.Stop()
	publisher.Flush()

	body := discoveryConfigFor(pub, "homeassistant/", "ccu-01_sysvars")
	if body == nil {
		t.Fatalf("no hub sysvar discovery config published; the serial-gated hub plane was skipped. topics=%v", publishedTopics(pub))
	}

	// unique_id must embed the canonical serial suffix (loom_<serial10>_sysvar_…).
	uid, _ := body["unique_id"].(string)
	if !strings.Contains(uid, "5a4993d962") {
		t.Fatalf("sysvar unique_id missing serial discriminator: %q", uid)
	}

	// The device block must declare the NAMED central hub device so HA resolves
	// every via_device parent to a real, named device instead of "unknown".
	dev, _ := body["device"].(map[string]any)
	if dev == nil {
		t.Fatalf("sysvar discovery missing device block: %v", body)
	}
	ids, _ := dev["identifiers"].([]any)
	foundCentral := false
	for _, id := range ids {
		if s, _ := id.(string); s == "openccu-loom_central_ccu-01" {
			foundCentral = true
		}
	}
	if !foundCentral {
		t.Fatalf("central hub device identifier not declared; parent stays unknown. device=%v", dev)
	}
	if name, _ := dev["name"].(string); name == "" {
		t.Fatalf("central hub device has no name -> HA renders it as 'unknown device'. device=%v", dev)
	}
}

// TestHubDiscoveryPublishedAfterSerialResolvesLate reproduces the prod timing
// exactly: the boot-time Start runs while the async readiness-gated bring-up has
// not yet resolved the serial (SystemInformation empty), so no serial-gated hub
// discovery is published — only raw state would flow. Once the serial resolves
// (post-ready) a re-Start must publish the previously-skipped hub discovery.
// This is the sequence the CentralSouthboundReadyEvent-driven re-Start restores.
func TestHubDiscoveryPublishedAfterSerialResolvesLate(t *testing.T) {
	t.Parallel()
	c, pub, publisher := hubDiscoveryFixture(t)

	sv := &hub.Sysvar{HubDataPoint: hub.HubDataPoint{Name: "Anwesenheit"}, ValueType: hmenum.HubValueTypeLogic}
	c.HubModel.PutSysvar(sv)

	// Boot-time Start with an unresolved serial: hub discovery is gated off.
	publisher.Start(context.Background())
	publisher.Flush()
	if body := discoveryConfigFor(pub, "homeassistant/", "ccu-01_sysvars"); body != nil {
		t.Fatalf("hub discovery published before the serial resolved: %v", body)
	}
	publisher.Stop()

	// Serial resolves during bring-up; the ready-driven re-Start re-wires.
	c.SetSystemInformation(central.SystemInfo{Serial: "3014F711A0001F5A4993D962"})
	publisher.Start(context.Background())
	defer publisher.Stop()
	publisher.Flush()

	body := discoveryConfigFor(pub, "homeassistant/", "ccu-01_sysvars")
	if body == nil {
		t.Fatalf("hub discovery still absent after the serial resolved + re-Start; topics=%v", publishedTopics(pub))
	}
	if uid, _ := body["unique_id"].(string); !strings.Contains(uid, "5a4993d962") {
		t.Fatalf("sysvar unique_id missing serial after re-Start: %q", uid)
	}
}

// TestConnectivityDiscoveryStateTopicIsPublished is the declared-vs-published
// round trip for the per-interface connectivity binary_sensor: the topic the
// boot-time seed names in `state_topic` must be a topic the reachability path
// actually writes.
//
// ConnectivityChangedEvent.InterfaceID already carries the `<central>-<iface>`
// wire id that observeProbeLatency stamps before the reconciler publishes,
// the same id the client coordinator is keyed by. The seed used to key off
// the bare interface name instead, so it declared `.../HmIP-RF` while the
// event path published `.../ccu-01-HmIP-RF`: the entity HA created at boot
// stayed unavailable forever and the first reachability change added a
// second, live one under the wire id.
func TestConnectivityDiscoveryStateTopicIsPublished(t *testing.T) {
	t.Parallel()
	c, pub, publisher := hubDiscoveryFixture(t)
	c.SetSystemInformation(central.SystemInfo{Serial: "3014F711A0001F5A4993D962"})

	// Registered exactly as the southbound wiring does it (ccu_wiring.go):
	// the wire id is the registry key, the bare enum rides alongside.
	if err := c.Clients.Register(&coordinators.ClientEntry{
		InterfaceID: WireInterfaceID("ccu-01", hmenum.InterfaceHmIPRF),
		Interface:   hmenum.InterfaceHmIPRF,
	}); err != nil {
		t.Fatalf("Clients.Register: %v", err)
	}

	publisher.Start(context.Background())
	defer publisher.Stop()
	publisher.Flush()

	body := discoveryConfigFor(pub, "homeassistant/", "connectivity")
	if body == nil {
		t.Fatalf("no connectivity discovery seeded; topics=%v", publishedTopics(pub))
	}
	stateTopic, _ := body["state_topic"].(string)
	if stateTopic == "" {
		t.Fatalf("connectivity discovery has no state_topic: %v", body)
	}

	// The reachability path: observeProbeLatency stamps the wire id onto the
	// event before the reconciler publishes it — never the CCU's bare
	// interface name.
	events.Publish(c.EventBus, hmevent.ConnectivityChangedEvent{
		Base:        hmevent.NewBaseAt(time.Now()),
		CentralName: "ccu-01",
		InterfaceID: WireInterfaceID("ccu-01", hmenum.InterfaceHmIPRF),
		Reachable:   true,
	})
	publisher.Flush()

	found := false
	for _, topic := range publishedTopics(pub) {
		if topic == stateTopic {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("declared state_topic %q is never published; topics=%v", stateTopic, publishedTopics(pub))
	}

	// And exactly one connectivity entity per radio — a seed under a second
	// identifier space leaves the operator with a dead duplicate.
	configs := 0
	for _, p := range pub.Published() {
		if strings.Contains(p.Topic, "connectivity") && strings.HasSuffix(p.Topic, "/config") {
			configs++
		}
	}
	if configs != 1 {
		t.Fatalf("published %d connectivity discovery configs, want exactly 1; topics=%v", configs, publishedTopics(pub))
	}
}

// TestRemovedProgramDiscoveryIsRetracted pins that a program the operator
// deleted in the CCU WebUI also disappears from the discovery plane. The
// refresh drops it from the model, but the retained `.../config` topic keeps
// the entity alive in Home Assistant — frozen at its last state and surviving
// daemon restarts — unless the publisher clears it.
func TestRemovedProgramDiscoveryIsRetracted(t *testing.T) {
	t.Parallel()
	c, pub, publisher := hubDiscoveryFixture(t)
	c.SetSystemInformation(central.SystemInfo{
		Model:   "HomeMatic Central",
		Version: "3.79.6",
		Serial:  "3014F711A0001F5A4993D962",
	})

	prog := &hub.Program{HubDataPoint: hub.HubDataPoint{Name: "Abend"}, ID: "prog-9"}
	prog.OnActive(false)
	c.HubModel.PutProgram(prog)

	publisher.Start(context.Background())
	defer publisher.Stop()
	publisher.Flush()

	declared := map[string]bool{}
	for _, p := range pub.Published() {
		if strings.HasSuffix(p.Topic, "/config") && strings.Contains(p.Topic, "prog-9") && len(p.Payload) > 0 {
			declared[p.Topic] = true
		}
	}
	if len(declared) == 0 {
		t.Fatalf("no discovery config published for the program; topics=%v", publishedTopics(pub))
	}

	// The refresh pass drops a program the CCU no longer reports.
	if !c.HubModel.RemoveProgram("prog-9") {
		t.Fatal("RemoveProgram reported no such program")
	}
	publisher.Flush()

	retracted := map[string]bool{}
	for _, p := range pub.Published() {
		if declared[p.Topic] && len(p.Payload) == 0 {
			retracted[p.Topic] = true
		}
	}
	for topic := range declared {
		if !retracted[topic] {
			t.Errorf("retained discovery config %s was never cleared after the program vanished", topic)
		}
	}
}

// TestRetractCentralClearsEveryHubDiscoveryConfig pins the whole-central
// retraction that runs when a CCU is removed at runtime. The per-entity
// OnRemoved hooks only fire for an entity the CCU drops one at a time, and the
// orphan sweep is scoped to registered centrals, so a removed central's
// retained hub-plane discovery configs (sysvars, programs, connectivity,
// alarm/service messages, the metric sensors, inbox, hub update) stayed alive in
// Home Assistant forever. RetractCentral must clear every config the publisher
// declared for that central — asserted here as a declared-vs-retracted round
// trip through the publisher itself.
func TestRetractCentralClearsEveryHubDiscoveryConfig(t *testing.T) {
	t.Parallel()
	c, pub, publisher := hubDiscoveryFixture(t)
	c.SetSystemInformation(central.SystemInfo{
		Model:   "HomeMatic Central",
		Version: "3.79.6",
		Serial:  "3014F711A0001F5A4993D962",
	})

	// A sysvar, a program and a registered interface exercise the per-entity
	// hub-discovery planes on top of the always-declared central-wide singletons.
	c.HubModel.PutSysvar(&hub.Sysvar{HubDataPoint: hub.HubDataPoint{Name: "Anwesenheit"}, ValueType: hmenum.HubValueTypeLogic})
	prog := &hub.Program{HubDataPoint: hub.HubDataPoint{Name: "Abend"}, ID: "prog-9"}
	prog.OnActive(false)
	c.HubModel.PutProgram(prog)
	if err := c.Clients.Register(&coordinators.ClientEntry{
		InterfaceID: WireInterfaceID("ccu-01", hmenum.InterfaceHmIPRF),
		Interface:   hmenum.InterfaceHmIPRF,
	}); err != nil {
		t.Fatalf("Clients.Register: %v", err)
	}

	publisher.Start(context.Background())
	defer publisher.Stop()
	publisher.Flush()

	declared := map[string]bool{}
	for _, p := range pub.Published() {
		if strings.HasSuffix(p.Topic, "/config") && len(p.Payload) > 0 {
			declared[p.Topic] = true
		}
	}
	if len(declared) == 0 {
		t.Fatalf("no hub discovery configs were declared; topics=%v", publishedTopics(pub))
	}

	// Runtime removal of the central: every retained discovery config it
	// declared must be cleared with an empty payload.
	publisher.RetractCentral(c)
	publisher.Flush()

	retracted := map[string]bool{}
	for _, p := range pub.Published() {
		if declared[p.Topic] && len(p.Payload) == 0 {
			retracted[p.Topic] = true
		}
	}
	for topic := range declared {
		if !retracted[topic] {
			t.Errorf("hub discovery config %s was never retracted on central removal", topic)
		}
	}
}
