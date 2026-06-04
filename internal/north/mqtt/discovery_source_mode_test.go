// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"context"
	"encoding/json"
	"maps"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// stubSource is a minimal SourceLike fake the source-mode tests use
// to drive aggregateChannel through the HADiscoveryPayloadBuilder fast
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

// stubBuilder extends stubSource with HADiscoveryPayloadBuilder so that
// aggregateChannel takes the ADR 0010 fast path (builder dispatch) instead
// of the deleted legacy buildX path. Tests populate component and body
// directly; the aggregator merges the base body fields on top.
type stubBuilder struct {
	stubSource
	component string
	body      map[string]any
}

func (s *stubBuilder) HADiscoveryPayload(_ payload.HADiscoveryContext) (component string, body map[string]any) {
	if s.body == nil {
		return s.component, nil
	}
	return s.component, cloneMap(s.body)
}

// TestAggregatorPassesThroughBuilderBody verifies that aggregateChannel
// dispatches to the HADiscoveryPayloadBuilder fast path (ADR 0010) and
// merges the base body fields on top of the builder's returned body.
// The builder owns all platform-specific payload fields; the aggregator
// only adds name / unique_id / availability / device / origin.
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

// TestPublishSourceStateOptInOnly verifies that PublishSourceState is
// still callable directly (kept for benchmarks and callers that want
// to opt back into the legacy aggregate shape during migration), but
// is NOT auto-triggered from PublishState.
func TestPublishSourceStateOptInOnly(t *testing.T) {
	t.Parallel()
	pub := &recordingPublisher{}
	b := NewBridge(BridgeConfig{
		Base:               "gh",
		CentralName:        "ccu",
		RawEnabled:         false, // raw plane disabled → no publish
		HADiscoveryEnabled: false,
	}, pub)

	src := &stubSource{state: map[string]any{"hvac_mode": "heat"}}
	if err := b.PublishState(context.Background(), Event{
		Source:        src,
		Interface:     "HmIP-RF",
		DeviceAddress: "BWTH004",
		ChannelNo:     1,
		Parameter:     "ACTUAL_TEMPERATURE",
		Value:         21.5,
	}); err != nil {
		t.Fatalf("PublishState: %v", err)
	}

	pub.mu.Lock()
	defer pub.mu.Unlock()
	for _, p := range pub.recs {
		if strings.HasSuffix(p.topic, "/1/state") {
			t.Errorf("raw-disabled bridge published aggregated topic %q", p.topic)
		}
	}
}

var _ = hmenum.ParameterActualTemperature // import keep

// TestPublishSourceStateAlwaysPublishes pins the post-ADR-0011-phase-2
// contract: the aggregate state topic carries derived-only fields,
// so there is no longer a "wait until every constituent DP is
// observed" condition. The previous payload.Observable gate (commit
// a7e1f0a) was a workaround for the staggered-DP-publish problem
// that ADR 0011 retired structurally — direct wire values now have
// their own per-DP topics each with an independent availability lane.
func TestPublishSourceStateAlwaysPublishes(t *testing.T) {
	t.Parallel()
	pub := &recordingPublisher{}
	b := NewBridge(BridgeConfig{
		Base:        "gh",
		CentralName: "ccu",
		RawEnabled:  true,
	}, pub)

	src := &stubSource{state: map[string]any{"hvac_mode": "heat"}}
	if err := b.PublishSourceState(context.Background(), "ccu", "HmIP-RF", "BWTH001", 1, src); err != nil {
		t.Fatalf("PublishSourceState: %v", err)
	}
	pub.mu.Lock()
	defer pub.mu.Unlock()
	matches := 0
	for _, p := range pub.recs {
		if strings.HasSuffix(p.topic, "/1/state") {
			matches++
		}
	}
	if matches != 1 {
		t.Fatalf("expected 1 state publish, got %d", matches)
	}
}
