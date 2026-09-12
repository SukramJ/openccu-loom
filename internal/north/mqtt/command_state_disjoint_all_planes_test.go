// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"sort"
	"strings"
	"testing"

	pload "github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// disjointSweepBase is the topic base every plane in the sweep is driven at.
// It has to be the base the plane runners already use ("gh" for four of the
// five reused runners) so the runners can be reused verbatim rather than
// re-implemented; the hub runner takes its base as an argument and is driven
// at the same value.
const disjointSweepBase = "gh"

// commandPlaneAt returns the started [CommandSubscriber] and the filters it
// registered, at one base.
//
// The filters come from a real Start against a recording subscriber, never
// from a literal list. A hand-written copy would make the sweep compare the
// state plane against a second copy of the filter set rather than against the
// subscriptions the daemon really holds — which is the same mistake the plane
// round-trip guards were rewritten to avoid.
// commandPlaneAt returns a started [CommandSubscriber] and the filters it
// registered.
//
// The subscriber itself is returned as well as its filters because the sweep
// now asks the question twice, through two independent oracles: this
// package's own [topicMatchesFilter] walk, and
// [CommandSubscriber.checkDisjoint], which is the shared module's own
// predicate over the routes the router really holds. Two answers to one
// question is worth the duplication here — the local matcher is 15 lines
// that could be wrong in the same direction as the code it checks, and the
// library's is the one the daemon would run at boot.
func commandPlaneAt(t *testing.T, base string) (sub *CommandSubscriber, filters []string) {
	t.Helper()
	rec := newFilterRecorder()
	sub = NewCommandSubscriber(rec, NewTopicBuilder(base), &fakeSink{}, nil)
	if err := sub.Start(context.Background()); err != nil {
		t.Fatalf("base %q: command subscriber start: %v", base, err)
	}
	t.Cleanup(sub.Close)
	filters = rec.recorded()
	if len(filters) == 0 {
		t.Fatalf("base %q: no command filters registered — the sweep would be vacuous", base)
	}
	return sub, filters
}

// TestEveryStatePlaneIsDisjointFromCommandSubscriptions sweeps every topic
// this daemon publishes, on all six of its planes, against every command
// filter it subscribes.
//
// Finding **F4**: this sweep existed for ONE plane of six.
// [TestHubStatePublishesAreDisjointFromCommandSubscriptions] drives four hub
// publish calls — `PublishProgram`, `PublishRoleAvailability`,
// `PublishSysvar`, `PublishInstallMode` — and nothing else. The per-datapoint
// plane, where the two catch-all filters live and where nearly all the
// traffic is, was not swept; nor were alarm, security or `addon_update`.
//
// Why it matters, in the words of the measurement that found the original
// defect: a broker delivers the daemon's own publishes back to it, so any
// overlap between a published topic and an own subscription is a
// self-inflicted CCU write with nothing in the logs. This daemon had exactly
// that. Program state used to be mirrored onto the program's own `…/trigger`
// command topic, and the echo ran `Program.execute` on the CCU on every boot,
// on every hub republish, and once for each freshly discovered program —
// every program, including deactivated ones. That is what the hub-plane sweep
// was written for. The hazard is not specific to the hub plane: a published
// per-DP topic whose last segment happened to be `set` at six or seven
// segments below the base would be the same defect on a plane with three
// orders of magnitude more topics, and nothing would have noticed.
//
// The six planes, each driven through the real publishers against a recording
// broker:
//
//  1. hub — programs, sysvars, install mode, role availability
//     ([runHubPlane]).
//  2. device discovery — every per-device / per-channel entity shape the
//     builder can produce without a full southbound model ([runDevicePlane]).
//  3. alarm — panels, zone state, zone availability, the event stream
//     ([runAlarmPlane]).
//  4. security — aggregates, classes, zones, hazards, fault reports
//     ([runSecurityPlane]).
//  5. addon_update — the one daemon-level self-update entity
//     ([runAddonUpdatePlane]).
//  6. the raw per-datapoint plane — slot state, slot
//     config, custom-DP state, device availability, device info,
//     diagnostics, the non-retained event stream, and a retained-state
//     eviction ([runRawDataPointPlane]). This is the plane the original
//     sweep did not reach, and it is added here rather than reusing a
//     runner because no round-trip guard drives it: the per-DP plane's
//     entities are declared by the device plane and its traffic is produced
//     by the domain layer, so the two halves are in different tests.
//
// Not pinned by this sweep, deliberately: the sweep asserts that no PUBLISHED
// topic is matched by an own SUBSCRIPTION. It does not assert the converse —
// that every subscription has a publisher — which is the plane round-trip
// guards' job, nor that a published topic is declared by some entity, which
// is [planeRoundTrip]'s second direction. And it says nothing about the three
// overlapping filter PAIRS (collision classes A and B): those are two of the
// daemon's own filters against each other, not a publish against a filter,
// and they are pinned by command_collision_class_b_test.go and
// command_filter_set_pin_test.go.
func TestEveryStatePlaneIsDisjointFromCommandSubscriptions(t *testing.T) {
	t.Parallel()

	sub, filters := commandPlaneAt(t, disjointSweepBase)

	planes := []struct {
		name string
		run  func(t *testing.T) *observedPlane
	}{
		{"hub", sweepHubPlane},
		{"device-discovery", sweepDevicePlane},
		{"alarm", sweepAlarmPlane},
		{"security", sweepSecurityPlane},
		{"addon-update", runAddonUpdatePlane},
		{"raw-per-datapoint", runRawDataPointPlane},
	}

	swept := 0
	for _, p := range planes {
		t.Run(p.name, func(t *testing.T) {
			t.Parallel()
			obs := p.run(t)
			published := obs.publishedTopics()
			if len(published) == 0 {
				t.Fatalf("%s: the plane published nothing — the sweep would be vacuous", p.name)
			}
			topics := sortedKeys(published)
			for _, topic := range topics {
				for _, f := range filters {
					if topicMatchesFilter(topic, f) {
						t.Errorf("%s: published topic %q is matched by the daemon's own command "+
							"subscription %q — the broker delivers this publish straight back to the "+
							"handler behind that filter, which turns a state write into a CCU write "+
							"with nothing in the logs", p.name, topic, f)
					}
				}
			}
			// The same sweep through the shared module's own predicate, over
			// the routes the router actually holds. It is the check a boot
			// would run, it reports every collision rather than the first,
			// and it cannot agree with the local matcher by construction
			// because it does not share a line of code with it.
			if err := sub.checkDisjoint(topics...); err != nil {
				t.Errorf("%s: CommandRouter.CheckDisjoint rejected this plane's published topics: %v",
					p.name, err)
			}
		})
		swept++
	}

	// Vacuity guard on the sweep itself: F4 was "one plane of six", so the
	// count is the thing that regressed and the count is asserted.
	if swept != 6 {
		t.Fatalf("swept %d planes, want 6 — a plane was dropped from the sweep, which is exactly "+
			"the shape of finding F4", swept)
	}
}

// sweepHubPlane, sweepDevicePlane, sweepAlarmPlane and sweepSecurityPlane
// adapt the four plane runners that take fixture arguments to the one-argument
// shape the sweep table needs. The fixture values are the ones each plane's
// own round-trip guard uses, so a runner keeps being driven the way it is
// already driven elsewhere.
func sweepHubPlane(t *testing.T) *observedPlane {
	t.Helper()
	return runHubPlane(t, disjointSweepBase, "ccu-01", "PartyMode", "12459")
}

func sweepDevicePlane(t *testing.T) *observedPlane {
	t.Helper()
	return runDevicePlane(t, disjointSweepBase, "ccu-01", "HmIP-RF", "0001ABCD")
}

func sweepAlarmPlane(t *testing.T) *observedPlane {
	t.Helper()
	return runAlarmPlane(t, disjointSweepBase)
}

func sweepSecurityPlane(t *testing.T) *observedPlane {
	t.Helper()
	return runSecurityPlane(t, disjointSweepBase)
}

// runRawDataPointPlane drives the raw per-datapoint plane — the one the
// original sweep did not reach — against a recording broker.
//
// Every write is a real [Bridge] method, at both segment depths the command
// filters care about and on every raw shape the bridge produces: the
// bucket-aware slot state and its `/config` companion, a custom-DP state,
// device availability, device info, diagnostics, the non-retained event
// stream, and a retained-state eviction. A retraction is included because it
// is a publish like any other — an empty payload on a topic a filter matches
// is delivered back and parsed by the handler exactly the same way.
//
// This runner used to wire the `LegacyAlias` mirror onto the private field
// and sweep its second base as a seventh shape, because finding **F8** had
// established that no config key could reach it. The mirror is gone: it was
// unreachable in every build ever produced, and ADR 0006's amendment records
// the measurement. There is no second base to sweep.
func runRawDataPointPlane(t *testing.T) *observedPlane {
	t.Helper()
	ctx := context.Background()
	obs := newObservedPlane()
	b := NewBridge(BridgeConfig{
		Base:        disjointSweepBase,
		CentralName: "ccu-01",
		RawEnabled:  true,
	}, obs)

	const (
		central = "ccu-01"
		iface   = "HmIP-RF"
		addr    = "0001ABCD"
	)

	// The three buckets of the canonical per-DP topology, each with its
	// `/config` companion. `master` and `calculated` are included because the
	// bucket is a literal segment and a filter could collide with any one of
	// them independently.
	for _, slot := range []pload.TopicSlot{
		{Address: addr, Channel: 1, Bucket: pload.BucketValues, Parameter: "STATE"},
		{Address: addr, Channel: 1, Bucket: pload.BucketMaster, Parameter: "TEMPERATURE_MINIMUM"},
		{Address: addr, Channel: 1, Bucket: pload.BucketCalculated, Parameter: "DEW_POINT"},
	} {
		if err := b.PublishSlotState(ctx, central, iface, slot, pload.PerDPState{
			Value: true, Available: true,
		}); err != nil {
			t.Fatalf("publish slot state %v: %v", slot.Bucket, err)
		}
		if err := b.PublishSlotConfig(ctx, central, iface, slot, pload.ConfigPayload(map[string]any{"unit": "°C"})); err != nil {
			t.Fatalf("publish slot config %v: %v", slot.Bucket, err)
		}
	}

	// A custom-DP state, which is the fourth bucket and the one whose
	// parameter segment is a domain kind rather than a wire parameter.
	if err := b.PublishCustomDPState(
		ctx, central, iface,
		pload.TopicSlot{Address: addr, Channel: 1, Bucket: pload.BucketCustom, Parameter: "climate"},
		pload.StatePayload(map[string]any{"value": 21.5}),
	); err != nil {
		t.Fatalf("publish custom dp state: %v", err)
	}

	// [Bridge.PublishState], which after the LegacyAlias deletion puts no
	// state of its own on the wire and only emits a discovery config. Swept
	// anyway: it is on the per-DP path, and a future change that gives it a
	// publish again has to pass this sweep to land.
	if err := b.PublishState(ctx, Event{
		Central:        central,
		Interface:      iface,
		DeviceAddress:  addr,
		ChannelNo:      1,
		ChannelAddress: addr + ":1",
		Parameter:      "STATE",
		Category:       hmenum.DataPointCategorySwitch,
		Value:          true,
	}); err != nil {
		t.Fatalf("publish state: %v", err)
	}

	// Device-level raw shapes.
	if err := b.PublishAvailability(ctx, central, iface, addr, true); err != nil {
		t.Fatalf("publish availability: %v", err)
	}
	if err := b.PublishDeviceInfo(ctx, central, iface, addr, pload.InfoPayload(map[string]any{"model": "HmIP-STH"})); err != nil {
		t.Fatalf("publish device info: %v", err)
	}
	if err := b.PublishDeviceDiagnostics(ctx, central, iface, addr, pload.StatePayload(map[string]any{"rssi": -68})); err != nil {
		t.Fatalf("publish device diagnostics: %v", err)
	}

	// The non-retained event stream. Not retained, so a consumer never
	// replays it — but a broker still delivers it back to the daemon on a
	// matching own subscription, so it belongs in the sweep.
	if err := b.PublishEvent(ctx, central, iface, addr, 1, "keypress", map[string]any{
		"press": "SHORT",
	}); err != nil {
		t.Fatalf("publish event: %v", err)
	}

	// A retraction. An empty payload is still a publish, and a handler behind
	// a matching filter parses it the same way.
	if err := b.EvictState(ctx, central, iface, addr, 1, "STATE"); err != nil {
		t.Fatalf("evict state: %v", err)
	}

	obs.settle(t)
	return obs
}

// TestRawDataPointPlaneSweepReachesEveryBucket is the vacuity guard for the
// plane [runRawDataPointPlane] adds, and the reason the sweep above can be
// trusted at all.
//
// A sweep is only as good as the topics it observes, and the failure mode an
// adversarial review of the shared library found five times over is a test
// whose assertion cannot fail. Here that would look like the raw runner
// publishing nothing under `RawEnabled: false`, or reaching only one bucket:
// every assertion in the sweep would pass and F4 would be half-fixed while
// reading as fixed. So the runner's output is checked for the four bucket
// segments the per-DP topology defines.
func TestRawDataPointPlaneSweepReachesEveryBucket(t *testing.T) {
	t.Parallel()
	obs := runRawDataPointPlane(t)
	topics := sortedKeys(obs.publishedTopics())
	if len(topics) < 10 {
		t.Fatalf("the raw plane published %d distinct topics, want at least 10 — "+
			"the sweep would cover almost nothing:\n%v", len(topics), topics)
	}
	for _, want := range []string{
		"/values/", "/master/", "/calculated/", "/custom/",
	} {
		if !anyTopicContains(topics, want) {
			t.Errorf("no swept topic contains %q — that shape of the raw plane is unobserved, "+
				"so the disjointness sweep says nothing about it:\n%v", want, topics)
		}
	}
	// The two depths the catch-all filters sit at. A published topic at six or
	// seven segments below the base is where a self-inflicted CCU write would
	// come from, so the sweep must actually observe topics at those depths.
	depths := map[int]bool{}
	for _, topic := range topics {
		depths[segmentsBelowBase(topic, disjointSweepBase)] = true
	}
	for _, d := range []int{6, 7} {
		if !depths[d] {
			t.Errorf("no swept topic sits %d segments below the base — the depth the catch-all "+
				"command filters match is unobserved, and the sweep cannot see the hazard it "+
				"exists for. Observed depths: %v", d, sortedIntKeys(depths))
		}
	}
}

func anyTopicContains(topics []string, sub string) bool {
	for _, topic := range topics {
		if strings.Contains(topic, sub) {
			return true
		}
	}
	return false
}

// segmentsBelowBase counts a topic's segments with the base removed, which is
// the depth a command filter matches at — the handlers themselves read the
// route's captured wildcard levels now and count nothing. Returns -1 for a
// topic outside the base, which no command filter can reach.
func segmentsBelowBase(topic, base string) int {
	prefix := base + "/"
	if len(topic) <= len(prefix) || topic[:len(prefix)] != prefix {
		return -1
	}
	return len(strings.Split(topic[len(prefix):], "/"))
}

func sortedIntKeys(m map[int]bool) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}

// TestCheckDisjointReportsARealCommandTopic is the positive control on the
// oracle the sweep above gained.
//
// A disjointness check that answered "fine" unconditionally would make every
// plane in that sweep pass, which is precisely the vacuity this programme has
// been bitten by — and a check delegated to a router that registered no
// routes would do exactly that. So the control feeds it topics that MUST
// collide: one per registered filter, built by substituting a plausible value
// for each `+` level. Every one of them has to come back as an error naming
// its filter.
func TestCheckDisjointReportsARealCommandTopic(t *testing.T) {
	t.Parallel()

	sub, filters := commandPlaneAt(t, disjointSweepBase)
	for _, f := range filters {
		topic := strings.ReplaceAll(f, "+", "x")
		err := sub.checkDisjoint(topic)
		if err == nil {
			t.Errorf("CheckDisjoint(%q) = nil, but that topic is matched by the registered "+
				"filter %q — the oracle the plane sweep relies on reports nothing", topic, f)
			continue
		}
		if !strings.Contains(err.Error(), f) {
			t.Errorf("CheckDisjoint(%q) did not name the colliding filter %q: %v", topic, f, err)
		}
	}
	// And a topic no filter claims must not be reported, or the oracle would
	// fail every plane instead of passing every plane.
	if err := sub.checkDisjoint(disjointSweepBase + "/bridge/status"); err != nil {
		t.Errorf("CheckDisjoint reported the bridge status topic, which no command filter matches: %v", err)
	}
}
