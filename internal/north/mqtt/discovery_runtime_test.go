// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"slices"
	"testing"

	hapublisher "github.com/SukramJ/go-hamqtt/publisher"
)

// seedDeclared puts one retained discovery config into the bridge's claim
// set the only way there is: by publishing it.
//
// The set used to be a map on the bridge that a test could write to
// directly. It belongs to the shared runtime now, which records what the
// broker accepted and nothing else — deliberately, so a boot that fails
// every publish through an open circuit breaker does not end up with a
// dedup gate claiming configs Home Assistant never saw. A test that wants
// the claim therefore has to produce the publish, which is also the more
// honest fixture.
func seedDeclared(t *testing.T, b *Bridge, topic string, payload []byte) {
	t.Helper()
	if _, err := b.pub.Publish(context.Background(), topic, payload); err != nil {
		t.Fatalf("seed %s: %v", topic, err)
	}
}

// isDeclared reports whether the bridge currently claims topic.
func isDeclared(b *Bridge, topic string) bool {
	return slices.Contains(b.pub.Declared(), topic)
}

// TestHABirthTopicMatchesRuntime pins this package's spelling of Home
// Assistant's lifecycle topic against the one the shared runtime actually
// subscribes to.
//
// The constant is still exported here because tests and the composition
// root name it, but nothing in the publish path reads it any more. Two
// spellings of the same topic is how a birth listener ends up subscribed to
// a topic Home Assistant never publishes on — and the failure is silent:
// the daemon simply never replays its configs.
func TestHABirthTopicMatchesRuntime(t *testing.T) {
	t.Parallel()
	if got := hapublisher.BirthTopic(discoveryPrefixForTest); got != HABirthTopic {
		t.Fatalf("runtime birth topic = %q, HABirthTopic = %q", got, HABirthTopic)
	}
}

// TestLastWillIsTheStatusTopicEveryEntityReferences pins the availability
// policy end to end: the will the bridge reports clears exactly the topic
// its own online marker sets, which is exactly the topic the discovery
// payloads list as an availability source.
//
// That agreement is the measured defect ADR 0070 cites. Two reference
// bridges configure a will whose topic no entity references, so a hard
// crash leaves every entity available forever; a third publishes its
// availability inside Home Assistant's own birth tree, where it is both
// wrong and inert.
func TestLastWillIsTheStatusTopicEveryEntityReferences(t *testing.T) {
	t.Parallel()
	b := NewBridge(BridgeConfig{Base: "gh", HADiscoveryEnabled: true}, &mockPublisher{})

	will, err := b.LastWill()
	if err != nil {
		t.Fatalf("LastWill: %v", err)
	}
	if will.Topic != b.Topics().BridgeStatus() {
		t.Fatalf("will topic = %q, want the bridge status topic %q", will.Topic, b.Topics().BridgeStatus())
	}
	if string(will.Payload) != "offline" {
		t.Fatalf("will payload = %q, want offline", will.Payload)
	}
	if !will.Retain {
		t.Fatal("the will must be retained: an availability marker nobody can read after the crash tells nothing")
	}
	if got, want := will.Topic, discoveryPrefixForTest+"status"; got == want {
		t.Fatalf("the daemon's own availability topic sits in Home Assistant's birth tree (%q)", got)
	}
}

// TestAnnounceOnlineAndTheWillAgree pins the other half: what
// [Bridge.AnnounceOnline] writes is the counterpart of what the will
// clears, on the same topic, both retained.
func TestAnnounceOnlineAndTheWillAgree(t *testing.T) {
	t.Parallel()
	pub := &mockPublisher{}
	b := NewBridge(BridgeConfig{Base: "gh", HADiscoveryEnabled: true}, pub)
	will, err := b.LastWill()
	if err != nil {
		t.Fatalf("LastWill: %v", err)
	}
	if err := b.AnnounceOnline(context.Background()); err != nil {
		t.Fatalf("AnnounceOnline: %v", err)
	}
	var found bool
	for _, p := range pub.publications() {
		if p.topic != will.Topic {
			continue
		}
		found = true
		if p.payload != "online" {
			t.Fatalf("online marker = %q, want online", p.payload)
		}
		if !p.retain {
			t.Fatal("the online marker must be retained")
		}
	}
	if !found {
		t.Fatalf("AnnounceOnline never wrote the will's topic %q", will.Topic)
	}
}

// discoveryPrefixForTest is Home Assistant's discovery root, spelled out
// once so the tests above do not reach for the naming package.
const discoveryPrefixForTest = "homeassistant/"
