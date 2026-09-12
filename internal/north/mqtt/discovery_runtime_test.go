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

// TestRuntimeQoSDistinguishesUnsetFromMostOnce pins the one translation
// that a cast would get wrong.
//
// The two types' zero values mean opposite things: [QoS]'s is QoS 0, an
// actual guarantee an operator can choose, while [hapublisher.QoS]'s is
// "unset" and resolves to QoS 1. A cast therefore turns a configured
// most-once into an at-least-once — on every publish, with nothing on
// the wire or in a log saying so. go-hamqtt v0.27.0 added
// QoSAtMostOnce, which sits outside the wire range, so the two stop
// being the same value.
func TestRuntimeQoSDistinguishesUnsetFromMostOnce(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   QoS
		want hapublisher.QoS
		wire byte
	}{
		{"most once survives", QoS0, hapublisher.QoSAtMostOnce, 0},
		{"at least once", QoS1, hapublisher.QoSAtLeastOnce, 1},
		{"exactly once", QoS2, hapublisher.QoSExactlyOnce, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := runtimeQoS(c.in)
			if got != c.want {
				t.Fatalf("runtimeQoS(%v) = %v, want %v", c.in, got, c.want)
			}
			// And the wire byte, because that is what a broker sees and
			// what the naming of the constants deliberately hides.
			wire, ok := got.Wire()
			if !ok {
				t.Fatalf("runtimeQoS(%v) produced an unresolvable QoS", c.in)
			}
			if wire != c.wire {
				t.Errorf("wire byte = %d, want %d", wire, c.wire)
			}
		})
	}

	// The negative control: a plain cast is what this function exists to
	// avoid, so assert it really would be wrong.
	if cast := hapublisher.QoS(QoS0); cast != hapublisher.QoSUnset {
		t.Errorf("hapublisher.QoS(QoS0) = %v, want QoSUnset — "+
			"if this changed, runtimeQoS may no longer be needed", cast)
	}
}
