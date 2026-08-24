// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package contract — Security & Safety MQTT plane hard rules.
//
// Every entity-construction symbol in internal/north/mqtt's security
// plane (securityEntity, securitySystemEntities, securityClassEntity,
// securityEventTypes, the attribute builders) is unexported, so this
// file cannot call into them directly the way the in-package
// security_discovery_test.go does. Instead it drives the real,
// exported production pipeline — [mqtt.NewBridge] over a fake
// [mqtt.Publisher], [mqtt.NewWiring], [mqtt.NewSecurityMQTTPublisher]
// and a real [events.Bus] — and asserts on the bytes that pipeline
// actually hands the (fake) broker. That is a stronger guarantee than
// unit-testing the builders in isolation: it catches a wiring mistake
// between reconcile() and the publish path just as readily as a
// builder-level regression.
package contract

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	modelsecurity "github.com/SukramJ/openccu-loom/internal/model/security"
	mqtt "github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// --- fakes -------------------------------------------------------------

// securityPlaneRecord is one captured [mqtt.Publisher.Publish] call.
type securityPlaneRecord struct {
	topic   string
	payload []byte
	qos     mqtt.QoS
	retain  bool
}

// securityPlanePublisher is a minimal [mqtt.Publisher] fake that
// records every publish so the test can assert on topic, payload, QoS
// and the retain flag — the three facts the Security & Safety plane's
// retained/non-retained split depends on.
type securityPlanePublisher struct {
	mu   sync.Mutex
	sent []securityPlaneRecord
}

func (p *securityPlanePublisher) Publish(_ context.Context, topic string, payload []byte, qos mqtt.QoS, retain bool, _ ...mqtt.PublishOption) error {
	cp := make([]byte, len(payload))
	copy(cp, payload)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sent = append(p.sent, securityPlaneRecord{topic: topic, payload: cp, qos: qos, retain: retain})
	return nil
}

func (p *securityPlanePublisher) records() []securityPlaneRecord {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]securityPlaneRecord, len(p.sent))
	copy(out, p.sent)
	return out
}

// fakeSecuritySnapshotSource implements [mqtt.SecuritySnapshotSource]
// with a snapshot the test controls directly.
type fakeSecuritySnapshotSource struct {
	mu   sync.Mutex
	snap modelsecurity.Snapshot
}

func (f *fakeSecuritySnapshotSource) Snapshot() modelsecurity.Snapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.snap
}

func (f *fakeSecuritySnapshotSource) DuressVisibility() hmenum.DuressVisibility {
	return hmenum.DuressVisibilityFull
}

// --- fixture -------------------------------------------------------------

const securityPlaneBase = "openccu-loom"

// newSecurityPlaneFixture wires a real [mqtt.Bridge]/[mqtt.Wiring]/
// [mqtt.SecurityMQTTPublisher] triple over a fake broker — the same
// component shape production assembles, minus the real network client.
func newSecurityPlaneFixture(t *testing.T, snap modelsecurity.Snapshot) (*mqtt.SecurityMQTTPublisher, *securityPlanePublisher) {
	t.Helper()
	pub := &securityPlanePublisher{}
	bridge := mqtt.NewBridge(mqtt.BridgeConfig{
		Base: securityPlaneBase, CentralName: "gh",
		RawEnabled: true, HADiscoveryEnabled: true,
	}, pub)
	wiring := mqtt.NewWiring(bridge, slog.Default())
	src := &fakeSecuritySnapshotSource{snap: snap}
	p := mqtt.NewSecurityMQTTPublisher(src, wiring, "en", "", slog.Default())
	return p, pub
}

// waitForSecurityTopic polls the fake publisher until a publish to
// topic satisfies want, or fails the test after a bounded timeout.
// [mqtt.SecurityMQTTPublisher] drains its publish queue on its own
// goroutine, so a publish following a bus event is never synchronous
// from the caller's point of view.
func waitForSecurityTopic(t *testing.T, pub *securityPlanePublisher, topic string, want func(securityPlaneRecord) bool) securityPlaneRecord {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var last securityPlaneRecord
	var lastOK bool
	for time.Now().Before(deadline) {
		for _, rec := range pub.records() {
			if rec.topic != topic {
				continue
			}
			last, lastOK = rec, true
			if want == nil || want(rec) {
				return rec
			}
		}
		time.Sleep(2 * time.Millisecond)
	}
	if lastOK {
		t.Fatalf("publish to %s never matched predicate; last seen payload=%q retain=%v qos=%v", topic, last.payload, last.retain, last.qos)
	}
	t.Fatalf("no publish observed for topic %s", topic)
	return securityPlaneRecord{}
}

// findSecurityTopic reports the most recent publish to topic, if any.
// Unlike waitForSecurityTopic it never waits: it is used to assert an
// absence, and waiting for something that must not happen only makes
// the test slow.
func findSecurityTopic(pub *securityPlanePublisher, topic string) (rec securityPlaneRecord, found bool) {
	for _, r := range pub.records() {
		if r.topic == topic {
			rec, found = r, true
		}
	}
	return rec, found
}

func securityPlaneJSONObject(t *testing.T, label string, payload []byte) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(payload, &body); err != nil {
		t.Fatalf("%s: payload is not a JSON object: %v (raw=%s)", label, err, payload)
	}
	return body
}

// manySecuritySources builds n distinct source refs under one class, so
// a test can push the reconcile path's attribute list past its
// truncation cap without depending on the cap's exact value.
func manySecuritySources(n int) []hmevent.SecuritySourceRef {
	out := make([]hmevent.SecuritySourceRef, 0, n)
	for i := range n {
		addr := fmt.Sprintf("DEV%04d:1", i)
		out = append(out, hmevent.NewSecuritySourceRef("gh", "HmIP-RF", addr, "STATE"))
	}
	return out
}

// --- 1. every emitted event type is announced ---------------------------

// TestSecurityPlane_AnnouncedEventTypesCoverEmittedVerbs derives the set
// of [hmenum.SecurityVerb] values the production code actually attaches
// to a security notification or fault-change event — from
// internal/security/subscribe.go (Triggered/Cleared),
// internal/security/fault.go (Raised/Cleared) and
// internal/north/mqtt/security_publisher.go's verbForFault
// (Raised/Cleared) — and asserts every one of them appears in the
// event_types the "event" and "fault" HA-Discovery entities announce.
// hmenum.SecurityVerbs() itself also lists pre_alarm/silenced/
// failed_to_arm, which no code path constructs yet; this test
// deliberately checks the narrower emitted set, not the full catalogue,
// because a consumer only needs the types it will actually see.
//
// A consumer drops an event whose type was not pre-declared, so an
// emitted verb missing from event_types is a silently lost event.
func TestSecurityPlane_AnnouncedEventTypesCoverEmittedVerbs(t *testing.T) {
	t.Parallel()
	p, pub := newSecurityPlaneFixture(t, modelsecurity.Snapshot{EngineHealthy: true})
	bus := events.NewBus()
	p.Start(bus)
	t.Cleanup(p.Stop)

	eventCfg := waitForSecurityTopic(t, pub, "homeassistant/event/security/event/config", nil)
	faultCfg := waitForSecurityTopic(t, pub, "homeassistant/event/security/fault/config", nil)

	emittedVerbs := []hmenum.SecurityVerb{
		hmenum.SecurityVerbTriggered,
		hmenum.SecurityVerbCleared,
		hmenum.SecurityVerbRaised,
	}

	for _, item := range []struct {
		label   string
		payload []byte
	}{
		{"event", eventCfg.payload},
		{"fault", faultCfg.payload},
	} {
		body := securityPlaneJSONObject(t, item.label, item.payload)
		raw, ok := body["event_types"].([]any)
		if !ok {
			t.Fatalf("%s entity: event_types missing or not a list: %v", item.label, body["event_types"])
		}
		announced := make(map[string]bool, len(raw))
		for _, v := range raw {
			if s, ok := v.(string); ok {
				announced[s] = true
			}
		}
		for _, verb := range emittedVerbs {
			if !announced[string(verb)] {
				t.Errorf("%s entity event_types %v does not announce emitted verb %q", item.label, raw, verb)
			}
		}
	}
}

// --- 2. event topics are never retained ---------------------------------

// TestSecurityPlane_EventTopicsNeverRetained is the rule that matters
// most: a retained alarm event re-fires every subscribed automation on
// every broker reconnect. It drives a real fault-raised event and a
// real notification event through the bus and asserts the resulting
// publishes to the two event topics carry retain=false and QoS 0 — at
// most once delivery is the right trade for a moment, never a state.
//
// It also asserts the contrasting case: the same notification, marked
// Retainable, produces a retained last_alarm publish. The split is
// deliberate (see [mqtt.SecurityMQTTPublisher] doc comment); asserting
// only the negative half would not prove the positive half survived
// unrelated refactoring.
func TestSecurityPlane_EventTopicsNeverRetained(t *testing.T) {
	t.Parallel()
	p, pub := newSecurityPlaneFixture(t, modelsecurity.Snapshot{EngineHealthy: true})
	bus := events.NewBus()
	p.Start(bus)
	t.Cleanup(p.Stop)

	// A ledger transition alone must produce no event. The fault topic
	// has exactly one producer — the rendered report below — because a
	// consumer's event entity parses one payload shape per topic, and
	// this event carried a different one: ids and counts, no text. While
	// both wrote here, every automation reading `subject` or `fault_id`
	// found its field on half the messages.
	events.Publish(bus, hmevent.SecurityFaultChangedEvent{
		Base:     hmevent.NewBase(),
		FaultID:  "f1",
		Class:    hmenum.SecurityClassTechnical,
		Reason:   hmenum.SecurityFaultReasonUnreachable,
		Severity: hmenum.SecuritySeverityInfo,
		Source:   hmevent.NewSecuritySourceRef("gh", "HmIP-RF", "ABCD0000001:1", "UNREACH"),
		Open:     true,
	})
	// It does republish the retained half, which is where the ledger
	// facts live now — so waiting on that proves the event was dispatched
	// rather than merely slow, and makes the absence below meaningful.
	waitForSecurityTopic(t, pub, securityPlaneBase+"/security/problem", func(r securityPlaneRecord) bool { return r.retain })
	if rec, found := findSecurityTopic(pub, securityPlaneBase+"/security/fault"); found {
		t.Errorf("a ledger transition published %q on the fault event topic: %s — "+
			"the topic has one producer, and a second payload shape makes every "+
			"consumer field intermittent", securityPlaneBase+"/security/fault", rec.payload)
	}

	events.Publish(bus, hmevent.SecurityNotificationEvent{
		Base:     hmevent.NewBase(),
		Class:    hmenum.SecurityClassTechnical,
		Severity: hmenum.SecuritySeverityInfo,
		Verb:     hmenum.SecurityVerbRaised,
		Subject:  "Device unreachable",
		Message:  "ABCD0000001:1 is unreachable",
		Fault:    true,
		// Not retainable: this asserts the event topic itself, and the
		// retained last_fault half is covered by the last_alarm contrast
		// below.
		Retainable: false,
	})
	faultRec := waitForSecurityTopic(t, pub, securityPlaneBase+"/security/fault", nil)
	if faultRec.retain {
		t.Errorf("security/fault publish has retain=true, want false")
	}
	if faultRec.qos != mqtt.QoS0 {
		t.Errorf("security/fault publish has QoS %v, want QoS0", faultRec.qos)
	}

	events.Publish(bus, hmevent.SecurityNotificationEvent{
		Base:       hmevent.NewBase(),
		Class:      hmenum.SecurityClassIntrusion,
		Severity:   hmenum.SecuritySeverityAlarm,
		Verb:       hmenum.SecurityVerbTriggered,
		Subject:    "Intrusion",
		Message:    "Intrusion detected",
		Fault:      false,
		Retainable: true,
	})
	eventRec := waitForSecurityTopic(t, pub, securityPlaneBase+"/security/event", nil)
	if eventRec.retain {
		t.Errorf("security/event publish has retain=true, want false")
	}
	if eventRec.qos != mqtt.QoS0 {
		t.Errorf("security/event publish has QoS %v, want QoS0", eventRec.qos)
	}

	// Contrast: the same Retainable notification also republishes the
	// retained last_alarm sensor, proving retain=false above is a
	// deliberate split and not an accident that dropped retention
	// everywhere.
	lastAlarmRec := waitForSecurityTopic(t, pub, securityPlaneBase+"/security/last_alarm", func(r securityPlaneRecord) bool { return r.retain })
	if !lastAlarmRec.retain {
		t.Errorf("security/last_alarm publish has retain=false, want true")
	}
}

// --- 3. attribute payloads are objects, never bare lists -----------------

// TestSecurityPlane_AttributePayloadsAreObjects builds a snapshot that
// exercises every attribute builder in internal/north/mqtt/
// security_reconcile.go (systemAttributes, hazardAttributes,
// faultAttributes, classAttributes, zoneAttributes, sourcesAttribute)
// via the real reconcile path and asserts each published payload
// unmarshals as a JSON object. A consumer's recorder discards a
// non-object attribute payload outright, so a builder that started
// returning a bare list would silently lose the whole attribute set.
func TestSecurityPlane_AttributePayloadsAreObjects(t *testing.T) {
	t.Parallel()
	snap := modelsecurity.Snapshot{
		Severity: hmenum.SecuritySeverityCritical,
		Classes: map[hmenum.SecurityClass]modelsecurity.ClassState{
			hmenum.SecurityClassSmoke: {
				Class:    hmenum.SecurityClassSmoke,
				Active:   true,
				Sources:  manySecuritySources(3),
				Known:    3,
				Centrals: []string{"gh"},
				SinceMS:  1000,
			},
		},
		Zones: map[string]modelsecurity.ZoneState{
			"eg": {
				ID:      "zone-eg",
				Slug:    "eg",
				Name:    "Erdgeschoss",
				State:   hmenum.AlarmZoneStateTriggered,
				Mode:    hmenum.AlarmModeFull,
				Sources: manySecuritySources(2),
				ByClass: map[hmenum.SecurityClass][]string{hmenum.SecurityClassSmoke: {"Flur"}},
			},
		},
		Faults: []modelsecurity.Fault{
			{ID: "f1", Class: hmenum.SecurityClassTechnical, Reason: hmenum.SecurityFaultReasonUnreachable, Severity: hmenum.SecuritySeverityInfo},
		},
		EngineHealthy: true,
	}
	p, pub := newSecurityPlaneFixture(t, snap)
	bus := events.NewBus()
	p.Start(bus)
	t.Cleanup(p.Stop)

	// enqueueJSON (security_reconcile.go) always injects a "state" key on
	// top of whatever the builder returns, so `len(body) == 0` cannot
	// distinguish a gutted builder from a working one — both payloads
	// carry that one key. Assert the fields each builder actually owns
	// instead: systemAttributes / hazardAttributes / faultAttributes /
	// classAttributes / zoneAttributes in internal/north/mqtt/
	// security_reconcile.go.
	for _, tc := range []struct {
		topic    string
		wantKeys []string
	}{
		{securityPlaneBase + "/security/state", []string{"classes", "zones", "open_faults", "engine_healthy"}},
		{securityPlaneBase + "/security/alarm", []string{"sources", "source_names", "count", "truncated", "total", "by_class"}},
		{securityPlaneBase + "/security/problem", []string{"faults", "count", "truncated", "total"}},
		{securityPlaneBase + "/security/class/smoke", []string{"sources", "source_names", "count", "truncated", "total", "known", "centrals", "since_ms", "severity"}},
		{securityPlaneBase + "/security/zone/eg", []string{"sources", "source_names", "count", "truncated", "total", "by_class", "zone_id", "zone_name", "zone_state", "mode", "incident_id"}},
	} {
		rec := waitForSecurityTopic(t, pub, tc.topic, nil)
		body := securityPlaneJSONObject(t, tc.topic, rec.payload)
		for _, key := range tc.wantKeys {
			if _, ok := body[key]; !ok {
				t.Errorf("%s: attribute payload missing key %q (builder dropped it): %s", tc.topic, key, rec.payload)
			}
		}
	}
}

// --- 4. truncation is announced ------------------------------------------

// TestSecurityPlane_TruncationIsAnnounced feeds a class with far more
// active sources than the reconcile path's internal cap and asserts the
// published class attributes say truncated:true and report the real
// total — not just the capped list length. Silent truncation reads to
// an operator as "that's all of them", which is worse than an
// explicitly bounded list.
//
// The source count (200) is chosen to outlast any plausible cap value
// rather than importing the unexported maxAttributeSources constant, so
// this test does not need updating if that cap changes.
func TestSecurityPlane_TruncationIsAnnounced(t *testing.T) {
	t.Parallel()
	const totalSources = 200
	snap := modelsecurity.Snapshot{
		Severity: hmenum.SecuritySeverityCritical,
		Classes: map[hmenum.SecurityClass]modelsecurity.ClassState{
			hmenum.SecurityClassSmoke: {
				Class:    hmenum.SecurityClassSmoke,
				Active:   true,
				Sources:  manySecuritySources(totalSources),
				Known:    totalSources,
				Centrals: []string{"gh"},
			},
		},
		EngineHealthy: true,
	}
	p, pub := newSecurityPlaneFixture(t, snap)
	bus := events.NewBus()
	p.Start(bus)
	t.Cleanup(p.Stop)

	rec := waitForSecurityTopic(t, pub, securityPlaneBase+"/security/class/smoke", nil)
	body := securityPlaneJSONObject(t, "class/smoke", rec.payload)

	truncated, ok := body["truncated"].(bool)
	if !ok || !truncated {
		t.Fatalf("truncated = %v, want true for %d sources", body["truncated"], totalSources)
	}
	total, ok := body["total"].(float64)
	if !ok || int(total) != totalSources {
		t.Fatalf("total = %v, want %d (the real source count, not the capped list length)", body["total"], totalSources)
	}
	sources, ok := body["sources"].([]any)
	if !ok {
		t.Fatalf("sources missing or not a list: %v", body["sources"])
	}
	if len(sources) == 0 || len(sources) >= totalSources {
		t.Errorf("sources list length = %d, want a bounded list strictly shorter than %d", len(sources), totalSources)
	}
}
