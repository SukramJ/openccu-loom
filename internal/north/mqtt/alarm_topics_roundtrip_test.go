// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"encoding/json"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestAlarmPlaneTopicsRoundTrip asserts that every topic the alarm
// plane declares in a panel's discovery payload is a topic the plane
// actually writes to (state) or actually subscribes to (command).
//
// It exists for the same reason as [TestSecurityPlaneTopicsRoundTrip]:
// the two halves of a plane can each pass their own tests while
// disagreeing with each other. Here the risk is concrete —
// [BuildAlarmPanelDiscovery] derives `state_topic`/`command_topic` from
// [alarmStateTopic]/[alarmCommandTopic], while [AlarmMQTTPublisher.reconcile]
// writes the state through the very same [alarmStateTopic] helper and
// [CommandSubscriber.Start] subscribes the command plane through its own,
// independently written wildcard (`<base>/alarm/+/set`). A future edit to
// either helper's topic shape without a matching edit on the other side
// would leave a panel's state forever unavailable, or its arm/disarm
// button silently unheard by the daemon.
//
// The comparison is one-directional: a declared topic nobody
// writes/subscribes is the defect. A topic written without a
// declaration (the non-retained `<base>/alarm/<zone>/event` stream,
// which HA reaches through the raw plane rather than through a
// discovered entity) is reported via t.Logf only.
//
// The table runs twice, once with the trailing slash an operator may
// configure on `topic_base`. Every panel names the bridge-status topic
// as its first availability source while the bridge publishes that
// status through its topic builder, which trims the base — one slash of
// disagreement takes every panel down rather than one value, because
// `availability_mode` is "all".
func TestAlarmPlaneTopicsRoundTrip(t *testing.T) {
	t.Parallel()
	for _, base := range []string{"gh", "gh/"} {
		alarmPlaneTopicsRoundTrip(t, base)
	}
}

func alarmPlaneTopicsRoundTrip(t *testing.T, base string) {
	t.Helper()
	zones := []string{"eg", "og"}

	declared := map[string]bool{}
	for _, zone := range zones {
		item := BuildAlarmPanelDiscovery(base, zone, "Zone "+zone,
			[]hmenum.AlarmMode{hmenum.AlarmModeFull}, false, false, false)
		collectAlarmDeclaredTopics(t, item, declared)
	}
	// The reset button and the latched-detector count are entities of
	// the same plane and are declared from the same walk. The button
	// rides the panel's command topic; the sensor introduces a state
	// topic of its own, which is exactly the shape that can be declared
	// and then never written.
	for _, zone := range zones {
		collectAlarmDeclaredTopics(t, BuildAlarmMotionResetDiscovery(base, zone, "Zone "+zone, "Reset motion", false), declared)
		collectAlarmDeclaredTopics(t, BuildAlarmTriggeredMotionDiscovery(base, zone, "Zone "+zone, "Triggered motion detectors", false), declared)
	}
	// The aggregate master panel — same builder, master=true.
	masterItem := BuildAlarmPanelDiscovery(base, "ignored", "Alarm system",
		[]hmenum.AlarmMode{hmenum.AlarmModeFull}, true, false, false)
	collectAlarmDeclaredTopics(t, masterItem, declared)
	collectAlarmDeclaredTopics(t, BuildAlarmMotionResetDiscovery(base, "ignored", "Alarm system", "Reset motion", true), declared)
	collectAlarmDeclaredTopics(t, BuildAlarmTriggeredMotionDiscovery(base, "ignored", "Alarm system", "Triggered motion detectors", true), declared)

	if len(declared) == 0 {
		t.Fatal("no topics declared; the walk found no discovery payloads and would pass vacuously")
	}

	published := alarmPublishedTopics(base, zones)
	if len(published) == 0 {
		t.Fatal("no topics published/subscribed; the walk found nothing and would pass vacuously")
	}

	for topic := range declared {
		if !published[topic] {
			t.Errorf("base %q: declared but never published/subscribed: %q — a consumer creates this entity "+
				"and it either stays unavailable forever (state) or its commands vanish silently (command)", base, topic)
		}
	}
	for topic := range published {
		if !declared[topic] {
			t.Logf("base %q: published but not declared: %q (no entity is created for it)", base, topic)
		}
	}
}

// collectAlarmDeclaredTopics extracts the top-level state_topic and
// command_topic fields plus every availability source from one
// alarm-panel discovery payload into out.
func collectAlarmDeclaredTopics(t *testing.T, item DiscoveryItem, out map[string]bool) {
	t.Helper()
	if !item.OK {
		t.Fatalf("a discovery builder returned OK=false for a valid entity")
	}
	var body map[string]any
	if err := json.Unmarshal(item.Payload, &body); err != nil {
		t.Fatalf("discovery payload for %q is not JSON: %v", item.ObjectID, err)
	}
	for _, field := range []string{"state_topic", "command_topic"} {
		if v, ok := body[field].(string); ok && v != "" {
			out[v] = true
		}
	}
	collectAvailabilityTopics(t, body, out)
}

// alarmPublishedTopics is the set of topics the alarm plane actually
// writes to or subscribes on, for the given zones plus the reserved
// master segment.
//
// The state half is derived from [alarmStateTopic] — the same helper
// [AlarmMQTTPublisher.reconcile] calls at the real publish call site
// (alarm_publisher.go). The command half is NOT derived by calling
// [alarmCommandTopic] again (that would only prove the discovery
// builder agrees with itself); it reproduces, independently,
// [CommandSubscriber.Start]'s own literal wildcard registration
// (`<base>/alarm/+/set`) with the wildcard segment substituted by the
// concrete zone — so a drift in either helper's topic shape, without a
// matching edit on the other side, is what makes this test fail.
func alarmPublishedTopics(base string, zones []string) map[string]bool {
	out := map[string]bool{
		base + "/alarm/" + alarmMasterZone + "/state":        true,
		base + "/alarm/" + alarmMasterZone + "/set":          true,
		base + "/alarm/" + alarmMasterZone + "/availability": true,
		// The bridge writes its retained status through the topic
		// builder, so the published side is spelled the way the bridge
		// spells it — not the way the plane's own helper does.
		NewTopicBuilder(base).BridgeStatus(): true,
		// Restated literally, not via alarmTriggeredMotionTopic: calling
		// the same helper on both sides would move them in lockstep and
		// the comparison could never fail. Mirrors what
		// AlarmMQTTPublisher.publishMotionEntities writes.
		base + "/alarm/" + alarmMasterZone + "/triggered-motion": true,
	}
	for _, zone := range zones {
		out[alarmStateTopic(base, zone)] = true
		// Mirrors CommandSubscriber.Start's `base+"/alarm/+/set"` wildcard.
		out[base+"/alarm/"+zone+"/set"] = true
		out[base+"/alarm/"+zone+"/triggered-motion"] = true
		// Mirrors AlarmMQTTPublisher.reconcile's per-zone availability
		// write, restated literally for the same reason as the command
		// half above.
		out[base+"/alarm/"+zone+"/availability"] = true
	}
	return out
}
