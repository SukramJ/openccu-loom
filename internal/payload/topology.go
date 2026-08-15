// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package payload

import "github.com/SukramJ/openccu-loom/pkg/hmenum"

// Bucket identifies the paramset / kind segment a [TopicSlot] lives
// under. Mirrors ADR 0011 §"Topic hierarchy" — the four explicit
// segments below the channel level: VALUES paramset, MASTER paramset,
// calculated/synthetic data points, and custom-DP aggregate.
//
// The strings are stable wire identifiers — they appear verbatim in
// MQTT topic paths consumers depend on.
type Bucket string

// Bucket values. The literal string is the topic-segment name.
const (
	// BucketValues is the VALUES paramset bucket — the runtime state
	// of a parameter (the values HA's normal entities consume).
	BucketValues Bucket = "values"

	// BucketMaster is the MASTER paramset bucket — operator-tunable
	// configuration parameters (HA's `entity_category=config`).
	BucketMaster Bucket = "master"

	// BucketCalculated is the calculated / synthetic data-point
	// bucket — derived sensors like DEW_POINT computed from other
	// observed parameters on the channel.
	BucketCalculated Bucket = "calculated"

	// BucketCustom is the custom-DP aggregate bucket — Climate /
	// Cover / Lock / Light / Siren / Switch / Valve / TextDisplay
	// types whose HA discovery references multiple wire parameters.
	// The custom-DP state topic carries derived fields only
	// (`hvac_mode`, `preset_mode`, `action`, …); direct wire values
	// stay under values/<param>/state.
	BucketCustom Bucket = "custom"
)

// TopicSlot identifies a source's address in the MQTT topic tree
// without committing to any particular base / central / interface
// prefix. The bridge stitches the slot together with its own
// configuration to produce the full topic path:
//
//	<base>/<central>/<iface>/<address>/channels/<channel>/<bucket>/<parameter>/state
//
// Sources never see the broker base, central name, or interface id —
// those are bridge-side prefixes. Mirrors ADR 0011 §"Source surface".
type TopicSlot struct {
	// Address is the CCU device address (12-character hex,
	// e.g. "000C9709AEF157"). Unchanged by the bridge.
	Address string

	// Channel is the device channel number (0..N). Unchanged by
	// the bridge.
	Channel int

	// Bucket selects the paramset / kind segment. See [Bucket].
	Bucket Bucket

	// Parameter is the wire-parameter name for [BucketValues] /
	// [BucketMaster] / [BucketCalculated] (e.g. "ACTUAL_TEMPERATURE",
	// "TEMPERATURE_MINIMUM", "DEW_POINT") — and the custom-DP
	// kind for [BucketCustom] ("climate", "lock", "cover",
	// "blind", "garage", "light", "switch", "siren", "smoke_siren",
	// "sound_player", "valve_irrigation", "valve_modulating",
	// "text_display").
	Parameter string
}

// HAEntity is implemented by sources that surface as a Home Assistant
// MQTT-Discovery entity. A source returning the empty string opts out
// of HA discovery — useful for diagnostic-only sources or those
// whose state lands at the wire-parameter level only.
//
// The string is the HA component name verbatim ("sensor",
// "binary_sensor", "climate", "lock", "cover", "light", "switch",
// "valve", "siren", "select", "number", "button", "text", "update").
//
// Sources that implement this typically also implement
// [HADiscoveryPayloadBuilder] so the bridge has a body to publish.
type HAEntity interface {
	// HAComponent returns the HA MQTT-Discovery component name. An
	// empty return opts the source out of HA discovery.
	HAComponent() string
}

// Slotted is implemented by sources that own a topic slot under
// their channel. Sources without a fixed slot — e.g. abstract
// aggregates that only exist at runtime — opt out by simply not
// implementing this interface; the bridge then publishes nothing
// for them.
type Slotted interface {
	// TopicSlot returns the source's slot in the topic tree.
	TopicSlot() TopicSlot
}

// DiscoveryDynamic is implemented by sources whose HA-Discovery
// payload depends on observed state — most prominently custom-DPs
// whose `modes` / `preset_modes` lists are mode- or capability-
// conditional (Climate's `preset_modes` only contains week-program
// slots when the thermostat is in AUTO mode; HEATING_COOLING flipping
// between HEATING and COOLING swaps `modes` from `[auto,heat,off]`
// to `[auto,cool,off]`).
//
// Declarative only: nothing consults this list today. The bridge
// re-renders on every observed data-point change regardless of
// parameter, and suppresses the publish when the rendered JSON matches
// the cached previous one. The list documents which parameters
// actually move the payload. It re-renders via
// [HADiscoveryPayloadBuilder] and re-publishes the retained discovery
// topic when the rendered JSON differs from the cached previous
// version. HA picks up the change automatically (retained discovery
// → entity reconfiguration).
//
// Sources without dynamic capabilities simply omit this interface;
// their discovery is then published once after first
// [HAEntity.HAComponent] hit and re-published on full daemon restart.
type DiscoveryDynamic interface {
	// DiscoveryTriggers returns the wire parameters whose value
	// change can flip the discovery shape. An empty slice means
	// the discovery is static — equivalent to not implementing the
	// interface at all.
	DiscoveryTriggers() []hmenum.Parameter
}
