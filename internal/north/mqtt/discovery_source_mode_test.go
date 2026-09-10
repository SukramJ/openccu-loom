// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"encoding/json"
	"maps"
	"testing"

	hacatalog "github.com/SukramJ/go-ha-catalog"
	hadiscovery "github.com/SukramJ/go-hamqtt/discovery"

	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// stubSource is a minimal SourceLike fake the source-mode tests use
// to drive aggregateChannel through the HADiscoveryComponentBuilder fast
// path. Each method returns a fresh map so callers can mutate without
// affecting later calls.
type stubSource struct {
	state  map[string]any
	config map[string]any
	info   map[string]any
}

func (s *stubSource) State() payload.StatePayload   { return cloneMap(s.state) }
func (s *stubSource) Config() payload.ConfigPayload { return cloneMap(s.config) }
func (s *stubSource) Info() payload.InfoPayload     { return cloneMap(s.info) }
func (s *stubSource) ServiceMethodNames() []string  { return nil }
func (s *stubSource) Invoke(_ context.Context, _ string, _ map[string]any, _ hmenum.CommandPriority) error {
	return nil
}

func cloneMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	maps.Copy(out, m)
	return out
}

// stubBuilder extends stubSource with HADiscoveryComponentBuilder so that
// aggregateChannel takes the ADR 0010 fast path (builder dispatch) instead
// of the deleted legacy buildX path. Tests populate component and body
// directly; the aggregator fills in the base body fields the builder left
// unset.
//
// The keys go into Component.Extra rather than a typed Fields struct on
// purpose: these tests exercise the aggregator, and several of them feed it
// bodies no platform would accept. Extra is the escape hatch for exactly that.
type stubBuilder struct {
	stubSource
	component string
	body      map[string]any
	// fields carries a real platform Fields struct for the tests that need
	// one — the localisers and the schema checks read those, not Extra.
	fields any
}

func (s *stubBuilder) HADiscoveryComponent(_ payload.HADiscoveryContext) hadiscovery.Component {
	if s.body == nil && s.fields == nil {
		// A builder with nothing to say produces no component, which is what
		// the untyped form signalled with a nil body.
		return hadiscovery.Component{}
	}
	return hadiscovery.Component{
		Platform: hacatalog.Platform(s.component),
		Fields:   s.fields,
		Extra:    cloneMap(s.body),
	}
}

// TestAggregatorPassesThroughBuilderBody verifies that aggregateChannel
// dispatches to the HADiscoveryComponentBuilder fast path (ADR 0010) and
// fills in the base body fields the builder left unset. The builder owns all
// platform-specific payload fields; the aggregator adds name / unique_id /
// availability / device / origin where the builder said nothing.
func TestAggregatorPassesThroughBuilderBody(t *testing.T) {
	t.Parallel()

	src := &stubBuilder{
		component: "climate",
		body: map[string]any{
			"mode_state_topic":          "gh/ccu/HmIP-RF/BWTH001/1/state",
			"current_temperature_topic": "gh/ccu/HmIP-RF/BWTH001/1/state",
			"mode_state_template":       "{{ value_json.hvac_mode }}",
			"preset_modes":              []string{"none", "boost"},
		},
	}

	db := NewDefaultDiscoveryBuilder(NewTopicBuilder("gh"), "ccu")

	ev := Event{
		Source:        src,
		Interface:     "HmIP-RF",
		DeviceAddress: "BWTH001",
		ChannelNo:     1,
		ChannelType:   "CLIMATECONTROL_RT_TRANSCEIVER",
	}
	comp, _, _, buf, ok := db.Build(ev)
	if !ok {
		t.Fatal("aggregateChannel did not return ok for stubBuilder")
	}
	if comp != "climate" {
		t.Fatalf("component=%q want climate", comp)
	}

	var body map[string]any
	if err := json.Unmarshal(buf, &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Platform-specific fields must pass through from the builder.
	wantStateTopic := "gh/ccu/HmIP-RF/BWTH001/1/state"
	if got, _ := body["mode_state_topic"].(string); got != wantStateTopic {
		t.Errorf("mode_state_topic = %q, want %q", got, wantStateTopic)
	}
	if got, _ := body["current_temperature_topic"].(string); got != wantStateTopic {
		t.Errorf("current_temperature_topic = %q, want %q", got, wantStateTopic)
	}

	wantTemplate := "{{ value_json.hvac_mode }}"
	if got, _ := body["mode_state_template"].(string); got != wantTemplate {
		t.Errorf("mode_state_template = %q, want %q", got, wantTemplate)
	}

	// preset_modes pass through verbatim from the builder.
	if presets, ok := body["preset_modes"].([]any); ok {
		if len(presets) != 2 || presets[1] != "boost" {
			t.Errorf("preset_modes = %v, want [none boost]", presets)
		}
	} else {
		t.Errorf("preset_modes missing or wrong type: %T %v", body["preset_modes"], body["preset_modes"])
	}

	// Base fields must be overlaid by the aggregator.
	if _, present := body["unique_id"]; !present {
		t.Error("aggregator must overlay unique_id onto the builder body")
	}
	if _, present := body["availability"]; !present {
		t.Error("aggregator must overlay availability onto the builder body")
	}
}

// TestBuildClimateNilSourceReturnsNotOK verifies that buildClimate
// returns ok=false when ev.Source is nil — the aggregated topology
// is the only path after ADR 0008 step B; callers without a Source
// fall through to classifyComponent. Replaces the legacy-mode test.
func TestBuildClimateNilSourceReturnsNotOK(t *testing.T) {
	t.Parallel()
	db := NewDefaultDiscoveryBuilder(NewTopicBuilder("gh"), "ccu")

	ev := Event{
		Interface:     "HmIP-RF",
		DeviceAddress: "BWTH002",
		ChannelNo:     1,
		ChannelType:   "CLIMATECONTROL_RT_TRANSCEIVER",
		// Source intentionally nil — aggregator must return ok=false.
	}
	_, _, _, _, ok := db.Build(ev)
	if ok {
		t.Fatal("buildClimate must return ok=false when ev.Source == nil (ADR 0008 step B)")
	}
}

// TestPublishStateNoLongerAutoPublishesAggregate pins the post-
// /custom/<kind>-consolidation contract: PublishState does NOT
// publish the legacy `<addr>/<ch>/state` aggregate any more, even
// when Event.Source is set. The custom-DP slot publish
// `<addr>/<ch>/custom/<kind>` is owned by EventBridge.publishCustomDPState
// and is the only retained custom-DP state topic going forward —
// the bare-`/state` shape doubled broker traffic and split state
// from its `/config` companion across two sub-trees.
func TestPublishStateNoLongerAutoPublishesAggregate(t *testing.T) {
	t.Parallel()
	pub := &recordingPublisher{}
	b := NewBridge(BridgeConfig{
		Base:               "gh",
		CentralName:        "ccu",
		RawEnabled:         true,
		HADiscoveryEnabled: false,
	}, pub)

	src := &stubSource{state: map[string]any{"hvac_mode": "heat"}}
	err := b.PublishState(context.Background(), Event{
		Source:        src,
		Interface:     "HmIP-RF",
		DeviceAddress: "BWTH003",
		ChannelNo:     1,
		Parameter:     "ACTUAL_TEMPERATURE",
		Value:         21.5,
	})
	if err != nil {
		t.Fatalf("PublishState: %v", err)
	}

	legacyAgg := "gh/ccu/HmIP-RF/BWTH003/1/state"
	pub.mu.Lock()
	defer pub.mu.Unlock()
	for _, p := range pub.recs {
		if p.topic == legacyAgg {
			t.Errorf("PublishState must not publish legacy aggregate %q; got topic with payload %q",
				legacyAgg, p.payload)
		}
	}
}

var _ = hmenum.ParameterActualTemperature // import keep

// TestBuilderOutranksTheFrame pins the single precedence rule for the whole
// discovery pipeline: later wins, and the builder runs after the frame.
//
// This seam used to do the opposite — `maps.Copy(body, base)`, the frame
// overwriting the builder — while the combined seam next door already let the
// projection win. Two seams with opposite rules is a coin flip for anyone
// adding a key, and it is the inconsistency ADR 0070 collapses.
//
// No builder in the tree needs this today: the frame sets exactly five
// top-level keys and none of the ten implementations writes one. The test is
// here so the next one that does gets an answer rather than silence.
func TestBuilderOutranksTheFrame(t *testing.T) {
	t.Parallel()

	src := &stubBuilder{
		component: "climate",
		body: map[string]any{
			"mode_state_topic": "gh/ccu/HmIP-RF/BWTH001/1/state",
			// A frame key, deliberately: an entity that must stay usable while
			// one of its sources is down wants "any", not the frame's "all".
			"availability_mode": "any",
		},
	}

	db := NewDefaultDiscoveryBuilder(NewTopicBuilder("gh"), "ccu")
	ev := Event{
		Source:        src,
		Interface:     "HmIP-RF",
		DeviceAddress: "BWTH001",
		ChannelNo:     1,
		ChannelType:   "CLIMATECONTROL_RT_TRANSCEIVER",
	}
	_, _, _, buf, ok := db.Build(ev)
	if !ok {
		t.Fatal("aggregateChannel did not return ok")
	}

	var body map[string]any
	if err := json.Unmarshal(buf, &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got, _ := body["availability_mode"].(string); got != "any" {
		t.Errorf("availability_mode = %q, want %q — the frame overwrote the builder", got, "any")
	}
	// The frame keys the builder said nothing about must still be filled in.
	for _, key := range []string{"unique_id", "availability", "device", "origin"} {
		if _, present := body[key]; !present {
			t.Errorf("frame key %q missing — the merge stopped filling gaps", key)
		}
	}
}
