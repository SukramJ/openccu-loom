// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/naming"
)

// observedPlane is the fake broker every `*PlaneTopicsRoundTrip` guard
// runs its plane against. It answers every write and every subscription
// and remembers what it was asked to carry.
//
// The guards exist to catch one thing: the publisher writing a topic the
// discovery config does not declare, or the other way round. A "published"
// set assembled by calling the same topic helpers the declaration calls
// cannot express that — both halves move together by construction, and a
// rename in the real publisher leaves the guard green. Every published or
// subscribed topic in this file therefore comes from a real run of the
// production code against this recorder; nothing is restated.
type observedPlane struct {
	mu        sync.Mutex
	published []publishRecord
	filters   []string
}

func newObservedPlane() *observedPlane { return &observedPlane{} }

func (o *observedPlane) Publish(_ context.Context, topic string, payload []byte, qos QoS, retain bool, _ ...PublishOption) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.published = append(o.published, publishRecord{topic: topic, payload: string(payload), qos: qos, retain: retain})
	return nil
}

func (o *observedPlane) Subscribe(_ context.Context, filter string, _ QoS, _ MessageHandler, _ ...SubscribeOption) (SubscribeResult, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.filters = append(o.filters, filter)
	return SubscribeResult{}, nil
}

func (o *observedPlane) Unsubscribe(context.Context, string) error { return nil }

// records returns a snapshot of every write the plane performed.
func (o *observedPlane) records() []publishRecord {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]publishRecord(nil), o.published...)
}

// settle waits until the plane stops writing.
//
// The publishers hand their writes to a worker goroutine so a domain's bus
// is never blocked on the broker, which means the last state topic of a
// reconcile can land after the call that triggered it returned. Waiting for
// a specific topic would be circular here — the topic set is what the guard
// is trying to observe — so this waits for quiescence instead.
func (o *observedPlane) settle(t *testing.T) {
	t.Helper()
	const (
		quiet    = 60 * time.Millisecond
		deadline = 5 * time.Second
		step     = 5 * time.Millisecond
	)
	start := time.Now()
	last := -1
	stable := time.Now()
	for time.Since(start) < deadline {
		o.mu.Lock()
		n := len(o.published)
		o.mu.Unlock()
		if n != last {
			last = n
			stable = time.Now()
		} else if time.Since(stable) >= quiet {
			return
		}
		time.Sleep(step)
	}
	t.Fatalf("the plane never stopped publishing (%d writes in %s); the guard cannot observe a stable topic set", last, deadline)
}

// declaredTopics extracts every topic named inside the discovery configs
// the plane really published: the top-level topic fields plus every
// availability source.
//
// An availability source counts as a declared topic like any other. With
// `availability_mode: "all"` a source nothing writes to is strictly worse
// than a missing state topic, because it takes the whole entity down
// rather than one value.
func (o *observedPlane) declaredTopics(t *testing.T) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, rec := range o.records() {
		if !isDiscoveryConfigTopic(rec.topic) || rec.payload == "" {
			// An empty payload is a retraction, not a declaration.
			continue
		}
		collectDeclaredTopicsFromPayload(t, out, rec.topic, []byte(rec.payload))
	}
	return out
}

// publishedTopics is every topic the plane wrote a state to — the
// discovery configs themselves excluded, since they carry the
// declarations rather than being declared.
func (o *observedPlane) publishedTopics() map[string]bool {
	out := map[string]bool{}
	for _, rec := range o.records() {
		if isDiscoveryConfigTopic(rec.topic) {
			continue
		}
		out[rec.topic] = true
	}
	return out
}

// subscribedFilters is every filter the plane really registered.
func (o *observedPlane) subscribedFilters() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.filters...)
}

// isDiscoveryConfigTopic reports whether topic is an HA Discovery config
// topic, using the model layer's own format rather than a literal prefix.
func isDiscoveryConfigTopic(topic string) bool {
	prefix := strings.SplitN(naming.DiscoveryConfigTopic("c", "n", "o"), "/", 2)[0]
	return strings.HasPrefix(topic, prefix+"/") && strings.HasSuffix(topic, "/config")
}

// collectDeclaredTopicsFromPayload adds every topic one discovery payload
// names to out.
func collectDeclaredTopicsFromPayload(t *testing.T, out map[string]bool, label string, payload []byte) {
	t.Helper()
	// The payload is decoded generically because it is compared as wire
	// JSON, not as a Go struct.
	var body map[string]any
	if err := json.Unmarshal(payload, &body); err != nil {
		t.Fatalf("discovery payload on %q is not JSON: %v", label, err)
	}
	for _, field := range []string{"state_topic", "command_topic", "json_attributes_topic", "latest_version_topic"} {
		if v, ok := body[field].(string); ok && v != "" {
			out[v] = true
		}
	}
	list, ok := body["availability"].([]any)
	if !ok {
		return
	}
	for _, entry := range list {
		src, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if v, ok := src["topic"].(string); ok && v != "" {
			out[v] = true
		}
	}
}

// planeRoundTrip compares what a plane declared against what it really
// carried, and is the shared body of every `*PlaneTopicsRoundTrip` guard.
//
// The comparison is one-directional on purpose: a declared topic nobody
// writes and nobody subscribes is the defect — a consumer creates the
// entity from the retained config and it stays unavailable forever, or its
// commands vanish silently. A topic carried without a declaration is a
// lesser problem (an operator simply gets no entity for it) and is
// reported rather than failed on.
//
// byDesign names the declared topics that are deliberately carried by
// something outside this run; an entry that is no longer needed fails, so
// the list cannot rot into a blanket exemption.
func planeRoundTrip(t *testing.T, label string, declared, published map[string]bool, filters []string, byDesign map[string]string) {
	t.Helper()
	if len(declared) == 0 {
		t.Fatalf("%s: no topics declared; the run produced no discovery payloads and the comparison would be vacuous", label)
	}
	if len(published) == 0 && len(filters) == 0 {
		t.Fatalf("%s: the plane wrote and subscribed nothing; the comparison would be vacuous", label)
	}
	carried := func(topic string) bool {
		if published[topic] {
			return true
		}
		for _, f := range filters {
			if topicMatchesFilter(topic, f) {
				return true
			}
		}
		return false
	}
	for _, topic := range sortedKeys(declared) {
		switch {
		case carried(topic):
			if reason, ok := byDesign[topic]; ok {
				t.Errorf("%s: %q is listed as carried elsewhere (%q) but this run carries it — "+
					"drop the entry so the list keeps meaning what it says", label, topic, reason)
			}
		case byDesign[topic] != "":
			continue
		default:
			t.Errorf("%s: declared but neither published nor subscribed: %q — a consumer creates this entity "+
				"and it either stays unavailable forever (state) or its commands vanish silently (command)", label, topic)
		}
	}
	for _, topic := range sortedKeys(published) {
		if !declared[topic] {
			t.Logf("%s: published but not declared: %q (no entity is created for it)", label, topic)
		}
	}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// topicMatchesFilter implements MQTT topic-filter matching (MQTT 3.1.1
// §4.7): `+` matches exactly one level, `#` matches the remainder.
//
// A command topic is "carried" when a real subscription filter matches it,
// which is the only sense in which a daemon can be said to listen on it.
// Comparing against a hand-written copy of the filter instead would make
// the declaration agree with a second copy of itself.
func topicMatchesFilter(topic, filter string) bool {
	tParts := strings.Split(topic, "/")
	fParts := strings.Split(filter, "/")
	for i, f := range fParts {
		if f == "#" {
			// `#` must be the last level and matches the rest, including
			// the parent level itself.
			return i == len(fParts)-1 && i <= len(tParts)
		}
		if i >= len(tParts) {
			return false
		}
		if f != "+" && f != tParts[i] {
			return false
		}
	}
	return len(tParts) == len(fParts)
}
