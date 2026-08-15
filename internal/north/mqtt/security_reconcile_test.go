// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/model/security"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// refusingPublisher records every publish and refuses the ones its
// predicate rejects, which is how the production stack behaves while the
// publish-only circuit breaker is open: the TCP link stays up, some
// publishes return an error, and no reconnect happens.
type refusingPublisher struct {
	mu     sync.Mutex
	sent   []publishRecord
	reject func(topic string) bool
}

func (p *refusingPublisher) Publish(_ context.Context, topic string, body []byte, qos QoS, retain bool, _ ...PublishOption) error {
	p.mu.Lock()
	reject := p.reject
	p.mu.Unlock()
	if reject != nil && reject(topic) {
		return errors.New("broker refused the publish")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sent = append(p.sent, publishRecord{topic: topic, payload: string(body), qos: qos, retain: retain})
	return nil
}

func (p *refusingPublisher) accept() {
	p.mu.Lock()
	p.reject = nil
	p.mu.Unlock()
}

func (p *refusingPublisher) count(topic string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, rec := range p.sent {
		if rec.topic == topic {
			n++
		}
	}
	return n
}

// staticSecuritySnapshot serves one fixed snapshot to the publisher.
type staticSecuritySnapshot struct{ snap security.Snapshot }

func (s staticSecuritySnapshot) Snapshot() security.Snapshot { return s.snap }

func smokeClassSnapshot() security.Snapshot {
	return security.Snapshot{
		Severity:      hmenum.SecuritySeverityInfo,
		EngineHealthy: true,
		Classes: map[hmenum.SecurityClass]security.ClassState{
			hmenum.SecurityClassSmoke: {Known: 1},
		},
		Zones: map[string]security.ZoneState{
			"cellar": {ID: "cellar", Name: "Cellar"},
		},
	}
}

// TestSecurityDiscoveryRetriesConfigsTheBrokerRefused pins the rule the
// bridge already applies to its own discovery cache: record what the
// broker accepted, never what was merely attempted.
//
// The publisher used to mark a class or zone declared before publishing
// it, so a single refused config — a tripped publish breaker needs no
// reconnect — hid that safety entity from Home Assistant for the rest of
// the broker connection, which on a healthy broker can be days.
func TestSecurityDiscoveryRetriesConfigsTheBrokerRefused(t *testing.T) {
	t.Parallel()
	classConfig := "homeassistant/binary_sensor/" + securityDiscoveryNodeID + "/class_smoke/config"
	zoneConfig := "homeassistant/sensor/" + securityDiscoveryNodeID + "/zone_cellar/config"

	pub := &refusingPublisher{reject: func(topic string) bool {
		return strings.HasSuffix(topic, "/class_smoke/config") || strings.HasSuffix(topic, "/zone_cellar/config")
	}}
	bridge := NewBridge(BridgeConfig{
		Base: "openccu-loom", CentralName: "ccu-01",
		RawEnabled: true, HADiscoveryEnabled: true,
	}, pub)
	p := NewSecurityMQTTPublisher(staticSecuritySnapshot{smokeClassSnapshot()},
		NewWiring(bridge, slog.Default()), "en", "", slog.Default())
	bus := events.NewBus()
	p.Start(bus)
	t.Cleanup(p.Stop)

	if n := pub.count(classConfig); n != 0 {
		t.Fatalf("the refused class config must not reach the recorder; got %d", n)
	}
	if bridge.planeDeclared(securityDiscoveryNodeID) {
		t.Fatal("the plane declared while a config was still unpublished; the orphan sweep would treat live entities as orphans")
	}

	// The broker recovers without dropping the connection, so nothing
	// resets the publisher — only the next reconcile can repair this.
	pub.accept()
	events.Publish(bus, hmevent.SecurityClassChangedEvent{Base: hmevent.NewBaseAt(time.Now())})

	if n := pub.count(classConfig); n != 1 {
		t.Fatalf("class config publishes after recovery=%d, want 1", n)
	}
	if n := pub.count(zoneConfig); n != 1 {
		t.Fatalf("zone config publishes after recovery=%d, want 1", n)
	}
	if !bridge.planeDeclared(securityDiscoveryNodeID) {
		t.Fatal("the plane never declared although every config was accepted; its orphans could never be swept")
	}

	// A further reconcile must not re-publish the unchanged configs —
	// the bridge's payload dedup carries that, and losing it would put a
	// full discovery pass on every security event.
	events.Publish(bus, hmevent.SecurityClassChangedEvent{Base: hmevent.NewBaseAt(time.Now())})
	if n := pub.count(classConfig); n != 1 {
		t.Fatalf("unchanged class config re-published %d times, want 1", n)
	}
}

// TestSecurityZoneDiscoveryFollowsARename covers the reason the declared
// state belongs in the bridge's payload cache rather than in a
// "seen this slug once" flag: the zone's config carries its name, and a
// once-only gate left the old label in Home Assistant forever.
func TestSecurityZoneDiscoveryFollowsARename(t *testing.T) {
	t.Parallel()
	zoneConfig := "homeassistant/sensor/" + securityDiscoveryNodeID + "/zone_cellar/config"
	pub := &refusingPublisher{}
	bridge := NewBridge(BridgeConfig{
		Base: "openccu-loom", CentralName: "ccu-01",
		RawEnabled: true, HADiscoveryEnabled: true,
	}, pub)
	src := &mutableSecuritySnapshot{snap: smokeClassSnapshot()}
	p := NewSecurityMQTTPublisher(src, NewWiring(bridge, slog.Default()), "en", "", slog.Default())
	bus := events.NewBus()
	p.Start(bus)
	t.Cleanup(p.Stop)

	src.rename("cellar", "Basement")
	events.Publish(bus, hmevent.SecurityZoneChangedEvent{Base: hmevent.NewBaseAt(time.Now())})

	pub.mu.Lock()
	defer pub.mu.Unlock()
	var last string
	for _, rec := range pub.sent {
		if rec.topic == zoneConfig {
			last = rec.payload
		}
	}
	if last == "" {
		t.Fatalf("no zone config published to %s", zoneConfig)
	}
	if !strings.Contains(last, "Basement") {
		t.Fatalf("the renamed zone kept its old label in discovery: %s", last)
	}
}

// mutableSecuritySnapshot lets a test change the domain state between
// two reconciles.
type mutableSecuritySnapshot struct {
	mu   sync.Mutex
	snap security.Snapshot
}

func (s *mutableSecuritySnapshot) Snapshot() security.Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snap
}

func (s *mutableSecuritySnapshot) rename(slug, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	z := s.snap.Zones[slug]
	z.Name = name
	s.snap.Zones[slug] = z
}
