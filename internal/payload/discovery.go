// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package payload

// HADiscoveryPayloadBuilder is the optional [Source] extension that
// custom-DP types implement when they want to drive Home Assistant's
// MQTT auto-discovery. The bridge attaches the platform-agnostic
// availability / device / origin block; the builder fills in the
// platform-specific fields (state-topic templates, mode lists,
// command-topic references, …).
//
// Returned `body` is the HA-Discovery payload skeleton. The bridge
// overlays the shared base fields (`name`, `unique_id`, `object_id`,
// `availability`, `device`, `origin`) and the `state_topic` reference
// to the channel's aggregated state topic. Builders SHOULD NOT
// populate those — overlap is silently overwritten by the bridge.
//
// `command_topic` references that point at service-method-specific
// topics use the helpers exposed via [TopicBuildContext]. Builders
// that need them request the context-aware variant of the method
// (see [HADiscoveryPayloadContextBuilder]).
//
// A type that does not implement this interface falls through to the
// per-parameter classifyComponent path in
// [internal/north/mqtt/discovery.go] — same as today's
// `ev.Source == nil` fallback.
//
// ADR 0010 introduces the contract; ADR 0009 introduces the
// service-method command topics that builders reference.
type HADiscoveryPayloadBuilder interface {
	HADiscoveryPayload(ctx HADiscoveryContext) (component string, body map[string]any)
}

// HADiscoveryContext carries the per-channel topic builders the
// custom-DP needs to assemble its HA-Discovery payload.
//
// The bridge constructs and passes a context per buildX call; the
// model layer uses the helpers without knowing the central name,
// interface, address, or channel number.
type HADiscoveryContext interface {
	// CustomDPStateTopic is the channel's custom-DP slot state topic
	// (`<addr>/<ch>/custom/<kind>`). It carries the curated
	// [Source.StatePayload] JSON — the canonical read surface for
	// HA-Discovery `*_state_topic` references with a
	// `value_template: "{{ value_json.<field> }}"` filter.
	//
	// Replaces the older [AggregatedStateTopic] form
	// (`<addr>/<ch>/state`); both used to coexist and carry
	// identical content, which doubled broker traffic and split
	// state and config across two sub-trees. The slot-style topic
	// puts state and `…/config` companion under the same `/custom/
	// <kind>/` prefix.
	CustomDPStateTopic() string

	// AggregatedStateTopic is the older channel-aggregate state
	// topic (`<addr>/<ch>/state`). Retained as a thin alias so
	// callers in transition compile; new code SHOULD prefer
	// [CustomDPStateTopic]. The bridge no longer publishes content
	// to this topic — subscribers will see the retained payload
	// from the previous build until the boot-time stale-cleanup
	// pass evicts it.
	//
	// Deprecated: use CustomDPStateTopic.
	AggregatedStateTopic() string

	// ServiceMethodCommandTopic returns the topic HA should write to
	// in order to invoke `method` on the channel's custom DP.
	// The bridge subscribes one wildcard covering every service-method
	// topic rather than consulting [Source.ServiceMethodNames], so an
	// unknown method is rejected at dispatch rather than never
	// subscribed. ADR 0009.
	ServiceMethodCommandTopic(method string) string

	// WireParameterCommandTopic is the legacy per-parameter command
	// topic. Reserved for HA-platform shapes whose multiplexed payloads
	// happen to be valid values of one real wire parameter — the lock's
	// payload_lock/_unlock on LOCK_TARGET_LEVEL, the garage's
	// payload_open/_close/_stop on DOOR_COMMAND. A shape whose payloads
	// span several parameters needs a service method that multiplexes
	// them instead; pointing the topic at one of the parameters makes
	// the other payloads write nonsense to it. New code SHOULD prefer
	// ServiceMethodCommandTopic.
	WireParameterCommandTopic(parameter string) string

	// WireParameterStateTopic is the legacy per-parameter state
	// topic. Same recommendation as WireParameterCommandTopic —
	// CustomDPStateTopic is the canonical read surface for
	// custom-DP-derived fields.
	WireParameterStateTopic(parameter string) string
}
