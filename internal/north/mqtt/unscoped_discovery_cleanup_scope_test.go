// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"encoding/json"
	"sort"
	"testing"
)

// componentDiscoveryPayload renders a retained per-entity discovery config
// with the topic fields the production builder actually writes for that
// component — which is the whole point of the fixture.
//
// The payload-keyed sweep is judged on `origin` and `unique_id` and on
// nothing else, and the fixture set that covered it was sensor/switch/event
// only: three components that all carry a `state_topic`. The cross-repo audit
// of 2026-09 found a sibling bridge whose ownership rule had silently
// collapsed onto a shared namespace prefix for exactly the payloads that
// carry none, and its `sensor`-only fixtures could not see it — a real
// operator's *Open Door* and *Pause Program* buttons were deleted from Home
// Assistant for a release. This repo is immune by construction rather than by
// test, so the fixture set is widened to the two components that would expose
// the regression if it ever stopped being true:
//
//   - `button` has a `command_topic` and NO state topic at all;
//   - `climate` has a `json_attributes_topic` and no `state_topic` either.
//
// Over the fleet golden those two are every payload without a state topic (13
// button, 26 climate). If a future predicate starts keying on one, these
// fixtures go red instead of an operator's entity registry.
func componentDiscoveryPayload(t *testing.T, component, uniqueID, origin string) []byte {
	t.Helper()
	body := map[string]any{
		"name":      "Fixture",
		"unique_id": uniqueID,
		"origin":    map[string]any{"name": origin, "sw_version": "test"},
	}
	switch component {
	case "button":
		// No state topic whatsoever — a button is write-only.
		body["command_topic"] = "gh/ccu/HmIP-RF/0001D3C99C1234/3/values/PRESS_SHORT/set"
	case "climate":
		// Attributes and setpoint commands, still no `state_topic`.
		body["json_attributes_topic"] = "gh/ccu/HmIP-RF/00150001/1/values/SET_POINT_TEMPERATURE/config"
		body["temperature_command_topic"] = "gh/ccu/HmIP-RF/00150001/1/values/SET_POINT_TEMPERATURE/set"
		body["modes"] = []string{"auto", "heat"}
	default:
		body["state_topic"] = "gh/ccu/HmIP-RF/00150001/1/values/ACTUAL_TEMPERATURE"
	}
	buf, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal %s payload: %v", component, err)
	}
	return buf
}

// retainedBundleComponent is one entry of a device document, as
// `components.<key>` in the payload.
type retainedBundleComponent struct {
	key      string
	platform string
	uniqueID string
}

// bundleDiscoveryPayload renders a device bundle
// (`homeassistant/device/<node_id>/config`) the way go-hamqtt's
// `discovery.Bundle` marshals one: a top-level `device` and `origin`, and
// per-component ids nested under `components`, with NO top-level
// `unique_id` — a device document has none to have.
func bundleDiscoveryPayload(t *testing.T, origin string, components ...retainedBundleComponent) []byte {
	t.Helper()
	cmps := make(map[string]any, len(components))
	for _, c := range components {
		entry := map[string]any{"platform": c.platform, "unique_id": c.uniqueID}
		if c.platform == "button" {
			entry["command_topic"] = "gh/ccu/HmIP-RF/0001D3C99C1234/3/values/PRESS_SHORT/set"
		}
		cmps[c.key] = entry
	}
	body := map[string]any{
		"device":     map[string]any{"identifiers": []string{"openccu-loom_00150001"}},
		"origin":     map[string]any{"name": origin, "sw_version": "test"},
		"components": cmps,
	}
	buf, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal bundle payload: %v", err)
	}
	return buf
}

// clearedTopics lists the topics the client was asked to clear with an
// empty retained payload, sorted for a stable diff.
func clearedTopics(mc *mockRetainClient) []string {
	mc.mu.Lock()
	defer mc.mu.Unlock()
	var out []string
	for _, p := range mc.published {
		if p.retain && len(p.payload) == 0 {
			out = append(out, p.topic)
		}
	}
	sort.Strings(out)
	return out
}

// TestUnscopedDiscoveryCleanupLeavesASiblingDaemonsConfigsAlone drives the
// payload-keyed sweep against a SECOND loom daemon's retained configs and
// pins that it takes none of them.
//
// It is a regression test for a live defect, not a hypothetical. The pass
// subscribed to the whole `homeassistant/#` tree and decided ownership from
// `origin.name` plus the shape of `unique_id`. Neither names a daemon:
// `originName` is a compile-time constant identical in every loom build, and
// an empty CCU-serial slot (`loom__…`) is what EVERY daemon wrote before the
// slot was filled. So a daemon swept a sibling's configs at a wholly foreign
// node id, on every boot, and the pass ran unconditionally — it was not
// behind `north.mqtt.discovery_retract_unscoped`, which only ever gated the
// node-id SPELLING. Home Assistant deletes the entity when the retained
// config is cleared, and a device-registry row goes with its last entity.
//
// The fixture deliberately makes the two daemons' entities as confusable as
// they are in the field: the same device address, the same object ids, the
// same origin block, byte-identical unique_ids. The ONLY thing that differs
// is the central in the node id — which is exactly the discriminator the
// pass was throwing away.
func TestUnscopedDiscoveryCleanupLeavesASiblingDaemonsConfigsAlone(t *testing.T) {
	t.Parallel()

	const (
		ownCentral     = "ccu"
		siblingCentral = "sibling-ccu"
	)
	ownNode := discoveryNodeID(ownCentral, "0001D3C99C1234")
	ownClimateNode := discoveryNodeID(ownCentral, "00150001")
	siblingNode := discoveryNodeID(siblingCentral, "0001D3C99C1234")
	siblingClimateNode := discoveryNodeID(siblingCentral, "00150001")

	mc := &mockRetainClient{}
	b := NewBridge(BridgeConfig{
		Base:               "openccu-loom",
		HADiscoveryEnabled: true,
		CentralName:        ownCentral,
	}, mc)

	var (
		ownButton      = b.Topics().DiscoveryConfig("button", ownNode, "3_press_short")
		ownClimate     = b.Topics().DiscoveryConfig("climate", ownClimateNode, "1_set_point_temperature")
		ownHealthy     = b.Topics().DiscoveryConfig("climate", ownClimateNode, "1_actual_temperature")
		siblingButton  = b.Topics().DiscoveryConfig("button", siblingNode, "3_press_short")
		siblingClimate = b.Topics().DiscoveryConfig("climate", siblingClimateNode, "1_set_point_temperature")
	)
	mc.retained = []retainedMsg{
		// Ours, from a build that left the CCU-serial slot empty.
		{topic: ownButton, payload: componentDiscoveryPayload(t, "button", "loom__0001d3c99c1234_3_press_short", originName)},
		{topic: ownClimate, payload: componentDiscoveryPayload(t, "climate", "loom__00150001_1_set_point_temperature", originName)},
		// Ours and already correct — must survive.
		{topic: ownHealthy, payload: componentDiscoveryPayload(t, "climate", "loom_4993d962_00150001_1_actual_temperature", originName)},
		// A live sibling daemon's, byte-identical ids and origin, foreign
		// central. Deleting these is unrecoverable at the device-registry
		// level, so the count below has to stay at two.
		{topic: siblingButton, payload: componentDiscoveryPayload(t, "button", "loom__0001d3c99c1234_3_press_short", originName)},
		{topic: siblingClimate, payload: componentDiscoveryPayload(t, "climate", "loom__00150001_1_set_point_temperature", originName)},
	}

	cleared, err := b.RunUnscopedDiscoveryCleanupOnce(context.Background(), 50)
	if err != nil {
		t.Fatalf("RunUnscopedDiscoveryCleanupOnce: %v", err)
	}

	got := clearedTopics(mc)
	for _, want := range []string{ownButton, ownClimate} {
		if !contains(got, want) {
			t.Errorf("%s was not cleared — its ambiguous identity survives and the corrected payload "+
				"adds a duplicate entity beside it; cleared=%v", want, got)
		}
	}
	for _, keep := range []string{siblingButton, siblingClimate} {
		if contains(got, keep) {
			t.Errorf("%s belongs to a SECOND loom daemon at a foreign node id and was cleared — Home "+
				"Assistant deletes that entity and its device-registry row goes with it; cleared=%v", keep, got)
		}
	}
	if contains(got, ownHealthy) {
		t.Errorf("%s carries an identity this build still emits and must not be cleared; cleared=%v", ownHealthy, got)
	}
	if cleared != 2 {
		t.Errorf("cleared %d configs, want exactly this daemon's own two; cleared=%v", cleared, got)
	}
}

// TestUnscopedDiscoveryCleanupWithoutACentralClaimsNothing pins the degenerate
// case the node-id scope introduces.
//
// A bridge with no configured central has no node-id namespace to claim, and
// the origin block alone is what deleted a sibling's entities. So the pass
// declines rather than falling back to claiming everything — the same guard
// the bundle sweep already makes for the same reason.
func TestUnscopedDiscoveryCleanupWithoutACentralClaimsNothing(t *testing.T) {
	t.Parallel()

	mc := &mockRetainClient{
		retained: []retainedMsg{
			{
				topic:   "homeassistant/button/ccu_0001d3c99c1234/3_press_short/config",
				payload: componentDiscoveryPayload(t, "button", "loom__0001d3c99c1234_3_press_short", originName),
			},
		},
	}
	b := NewBridge(BridgeConfig{Base: "openccu-loom", HADiscoveryEnabled: true}, mc)

	cleared, err := b.RunUnscopedDiscoveryCleanupOnce(context.Background(), 50)
	if err != nil {
		t.Fatalf("RunUnscopedDiscoveryCleanupOnce: %v", err)
	}
	if cleared != 0 || len(clearedTopics(mc)) != 0 {
		t.Errorf("a bridge with no central cleared %d configs (%v); with nothing to scope by it must claim nothing",
			cleared, clearedTopics(mc))
	}
}

// TestUnscopedDiscoveryCleanupReachesABundlesComponentIdentities pins that a
// device document is judged on the ids it actually carries.
//
// A bundle has no top-level `unique_id` — its ids nest one per component
// under `components.<key>.unique_id` — so a top-level-only decode read every
// bundle as "no identity, nothing to do". Driven against a fixture that also
// held the per-entity configs, the per-entity ones were cleared and the
// bundle survived: under-cleanup, and it defeats the pass entirely for a
// deployment on `north.mqtt.discovery_bundles`, because Home Assistant keys
// its registry on the component ids and the stale document keeps the
// unaddressable ones alive.
//
// The three cases together are the contract: any condemned component
// condemns the document (it cannot be partially cleared), an all-current
// document survives, and a sibling daemon's document is out of scope
// whatever it carries.
func TestUnscopedDiscoveryCleanupReachesABundlesComponentIdentities(t *testing.T) {
	t.Parallel()

	const (
		ownCentral     = "ccu"
		siblingCentral = "sibling-ccu"
	)
	mc := &mockRetainClient{}
	b := NewBridge(BridgeConfig{
		Base:               "openccu-loom",
		HADiscoveryEnabled: true,
		CentralName:        ownCentral,
	}, mc)

	var (
		staleBundle   = "homeassistant/device/" + discoveryNodeID(ownCentral, "00150001") + "/config"
		healthyBundle = "homeassistant/device/" + discoveryNodeID(ownCentral, "000B0001") + "/config"
		siblingBundle = "homeassistant/device/" + discoveryNodeID(siblingCentral, "00150001") + "/config"
	)
	// One current component beside one stale one, because the interesting
	// half is that a mixed document still goes: leaving it would keep the
	// stale identity, and there is no way to clear half a document.
	staleComponents := []retainedBundleComponent{
		{key: "1_actual_temperature", platform: "sensor", uniqueID: "loom_4993d962_00150001_1_actual_temperature"},
		{key: "3_press_short", platform: "button", uniqueID: "loom__00150001_3_press_short"},
	}
	mc.retained = []retainedMsg{
		{topic: staleBundle, payload: bundleDiscoveryPayload(t, originName, staleComponents...)},
		{topic: healthyBundle, payload: bundleDiscoveryPayload(t, originName, retainedBundleComponent{
			key: "1_state", platform: "switch", uniqueID: "loom_4993d962_000b0001_1_state",
		})},
		// A sibling daemon's document, condemned ids and all.
		{topic: siblingBundle, payload: bundleDiscoveryPayload(t, originName, staleComponents...)},
	}

	cleared, err := b.RunUnscopedDiscoveryCleanupOnce(context.Background(), 50)
	if err != nil {
		t.Fatalf("RunUnscopedDiscoveryCleanupOnce: %v", err)
	}
	got := clearedTopics(mc)
	if !contains(got, staleBundle) {
		t.Errorf("the device document %s carries a component id this build can no longer emit and was not "+
			"cleared — the stale identity outlives the corrected document and Home Assistant shows both; cleared=%v",
			staleBundle, got)
	}
	if contains(got, healthyBundle) {
		t.Errorf("%s carries only identities this build still emits and must not be cleared; cleared=%v", healthyBundle, got)
	}
	if contains(got, siblingBundle) {
		t.Errorf("%s is a SECOND loom daemon's device document at a foreign node id and was cleared; cleared=%v",
			siblingBundle, got)
	}
	if cleared != 1 {
		t.Errorf("cleared %d documents, want exactly the one of ours carrying a stale component id; cleared=%v", cleared, got)
	}
}

// TestUnscopedDiscoveryCleanupCoversEverySecondaryCentral pins that the
// node-id scope is the union over every central this daemon serves, not the
// default one alone.
//
// The per-central sweeps are handed one central and scope to it. This pass
// has no central argument — it runs once at boot, before the snapshot — so a
// scope built from [BridgeConfig.CentralName] alone would silently leave
// every secondary CCU's stale identities unreachable forever, which is
// indistinguishable from a correctly scoped sweep: both clear something and
// report a number. Scoping is a fix that can fail closed, and failing closed
// here means the duplicate-entity defect stays live on every CCU but the
// first.
func TestUnscopedDiscoveryCleanupCoversEverySecondaryCentral(t *testing.T) {
	t.Parallel()

	mc := &mockRetainClient{}
	b := NewBridge(BridgeConfig{
		Base:               "openccu-loom",
		HADiscoveryEnabled: true,
		CentralName:        "ccu-01",
		CentralNames:       []string{"ccu-01", "ccu-02"},
	}, mc)

	primary := b.Topics().DiscoveryConfig("button", discoveryNodeID("ccu-01", "0001D3C99C1234"), "3_press_short")
	secondary := b.Topics().DiscoveryConfig("climate", discoveryNodeID("ccu-02", "00150001"), "1_set_point_temperature")
	mc.retained = []retainedMsg{
		{topic: primary, payload: componentDiscoveryPayload(t, "button", "loom__0001d3c99c1234_3_press_short", originName)},
		{topic: secondary, payload: componentDiscoveryPayload(t, "climate", "loom__00150001_1_set_point_temperature", originName)},
	}

	cleared, err := b.RunUnscopedDiscoveryCleanupOnce(context.Background(), 50)
	if err != nil {
		t.Fatalf("RunUnscopedDiscoveryCleanupOnce: %v", err)
	}
	got := clearedTopics(mc)
	if !contains(got, secondary) {
		t.Errorf("the second CCU's stale config %s was not reached — its ambiguous identities are unreachable "+
			"for the life of the deployment; cleared=%v", secondary, got)
	}
	if !contains(got, primary) {
		t.Errorf("the default central's stale config %s was not reached; cleared=%v", primary, got)
	}
	if cleared != 2 {
		t.Errorf("cleared %d configs, want one per configured central; cleared=%v", cleared, got)
	}
}

// TestUnscopedDiscoveryCleanupHonoursTheUnscopedOptIn pins the one half of
// this pass where `north.mqtt.discovery_retract_unscoped` still governs.
//
// The node-id scope introduced by #817 means a daemon on a non-default
// `topic_base` wrote its OWN pre-scope configs under the bare
// `<central-slug>_` spelling — and that spelling is byte-for-byte what a
// default-base sibling publishes live. #826's finding is that the two cannot
// be told apart by any predicate, which is why claiming them is the
// operator's decision and not the daemon's.
//
// That reasoning survives intact here and is threaded through
// [discoveryNodePrefixes] unchanged: by default this pass declines the
// unscoped spelling, and with the flag on it reaches it. Scoping by node id
// is NOT a substitute for the flag, and the flag is not a substitute for
// scoping — this test and
// [TestUnscopedDiscoveryCleanupLeavesASiblingDaemonsConfigsAlone] are the
// pair, and a change that satisfied one by breaking the other would be the
// shape of both the original defect and the over-correction.
func TestUnscopedDiscoveryCleanupHonoursTheUnscopedOptIn(t *testing.T) {
	t.Parallel()

	const central = "ccu"
	// `homeassistant/climate/ccu_00150001/…` — what this daemon wrote before
	// the base scope existed, and what a default-base sibling writes now.
	preScope := "homeassistant/climate/" + discoveryNodeID(central, "00150001") + "/1_set_point_temperature/config"
	payload := componentDiscoveryPayload(t, "climate", "loom__00150001_1_set_point_temperature", originName)

	for _, tc := range []struct {
		name     string
		optIn    bool
		wantGone bool
	}{
		{name: "default declines the ambiguous spelling", optIn: false, wantGone: false},
		{name: "opted in reaches it", optIn: true, wantGone: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mc := &mockRetainClient{retained: []retainedMsg{{topic: preScope, payload: payload}}}
			b := NewBridge(BridgeConfig{
				Base:                     "gh",
				HADiscoveryEnabled:       true,
				CentralName:              central,
				RetractUnscopedDiscovery: tc.optIn,
			}, mc)
			if scope := b.Topics().DiscoveryNodeScope(); scope == "" {
				t.Fatal("the fixture needs a non-default topic base; `gh` produced no node-id scope")
			}

			if _, err := b.RunUnscopedDiscoveryCleanupOnce(context.Background(), 50); err != nil {
				t.Fatalf("RunUnscopedDiscoveryCleanupOnce: %v", err)
			}
			if gone := contains(clearedTopics(mc), preScope); gone != tc.wantGone {
				t.Errorf("cleared=%t for %s with discovery_retract_unscoped=%t, want %t — off, that topic may "+
					"be a live default-base sibling's; on, it is this daemon's own pre-scope config",
					gone, preScope, tc.optIn, tc.wantGone)
			}
		})
	}
}
