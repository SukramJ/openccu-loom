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
	// The bridge subscribes the topic when [Source.ServiceMethodNames]
	// includes `method`. ADR 0009.
	ServiceMethodCommandTopic(method string) string

	// WireParameterCommandTopic is the legacy per-parameter command
	// topic. Reserved for HA-platform shapes that cannot be reduced
	// to a single service-method call (cover's payload_open/_close/
	// _stop multiplexing, light's separate brightness vs. color
	// channels). New code SHOULD prefer ServiceMethodCommandTopic.
	WireParameterCommandTopic(parameter string) string

	// WireParameterStateTopic is the legacy per-parameter state
	// topic. Same recommendation as WireParameterCommandTopic —
	// CustomDPStateTopic is the canonical read surface for
	// custom-DP-derived fields.
	WireParameterStateTopic(parameter string) string
}
