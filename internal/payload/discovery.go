// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package payload

// HADiscoveryPayloadBuilder is the optional [Source] extension that
// custom-DP types implement when they want to drive Home Assistant's
// MQTT auto-discovery. The bridge attaches the platform-agnostic
// availability / device / origin block; the builder fills in the
// platform-specific fields (state-topic templates, mode lists,
// command-topic references, …).
//
// Returned `body` is the HA-Discovery payload skeleton. The bridge
// overlays the shared base fields (`name`, `unique_id`,
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
	// Replaces the retired `<addr>/<ch>/state` channel-aggregate
	// form; both used to coexist and carry identical content, which
	// doubled broker traffic and split state and config across two
	// sub-trees. The slot-style topic puts state and its `…/config`
	// companion under the same `/custom/<kind>/` prefix.
	CustomDPStateTopic() string

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

	// WireParameterStateTopicOn is [WireParameterStateTopic] for a
	// parameter that lives on a named channel of the device rather
	// than on the custom DP's own channel.
	//
	// A custom DP may compose a field from a sibling channel — the
	// classic HM-CC-TC keeps its setpoint on the regulator channel
	// while the thermostat entity is materialised on the weather
	// channel. The per-DP slot state is published under the channel
	// the parameter actually lives on, so a payload that declares the
	// topic under its own channel names a topic nothing ever writes
	// and the field stays empty forever. Pass the resolved slot's
	// channel address (`<device>:<n>`) to name the published topic.
	//
	// An address without a parsable channel suffix falls back to the
	// event's own channel, which is what [WireParameterStateTopic]
	// would have returned.
	WireParameterStateTopicOn(channelAddress, parameter string) string
}
