// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package payload

import (
	"context"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// HA entity categories a combined projection can place itself in.
// Declared here rather than imported from the MQTT adapter so the
// dependency keeps pointing model → payload, never model → adapter.
// The values are Home Assistant's own vocabulary; the MQTT adapter's
// EntityCategoryConfig / EntityCategoryDiagnostic are constant
// aliases of these two, so there is one declaration, not a pair that
// has to be kept in step by hand.
const (
	CombinedEntityCategoryConfig     = "config"
	CombinedEntityCategoryDiagnostic = "diagnostic"
)

// CombinedProjection is the north-bound contract a combined data point
// exposes so adapters can surface it without knowing its concrete type.
//
// It exists because the alternative does not survive a new combined DP.
// The event bridge used to type-switch over `*combined.Timer`,
// `*combined.LevelCombined` and `*combined.HSColor` with no default
// branch, so a fourth combined type compiled, attached to its channel,
// published nothing, and looked exactly like a working one. The Matter
// assembler already dispatches combined DPs through a capability
// interface ([interfaces.MatterEndpointSource]); this is the same seam
// for the MQTT plane, and [TestCombinedProjectionCoversEveryCombinedType]
// is what keeps a new type from silently missing it.
//
// A projection describes only the data-point-specific half of the
// discovery body. The bridge owns the frame every combined entity
// shares — unique_id, availability, device, origin — because those
// derive from the (central, interface, device, channel) tuple the model
// layer deliberately does not know.
//
// loom:reachable:reason="asserted against in EventBridge.publishCombinedDPSnapshot and MQTTCommandSink.SetCombinedValue, which every combined data point reaches through; a type used only behind an interface assertion, which the analyzer's callgraph does not resolve"
type CombinedProjection interface {
	// CombinedKind is the stable topic segment and object_id suffix
	// identifying this projection ("duration", "hs_color",
	// "door_mode"). It is part of the retained MQTT topic, so it must
	// stay stable across releases: changing it orphans every retained
	// message under the old name.
	CombinedKind() string

	// HACombinedDiscovery returns the HA component the projection maps
	// onto ("number", "sensor", "select", …) and the data-point-specific
	// discovery keys. Returning an empty component suppresses discovery
	// for this data point while leaving its state publication intact.
	HACombinedDiscovery(ctx CombinedDiscoveryContext) (component string, body map[string]any)

	// CombinedStatePayload renders the current value for the retained
	// state topic. observed is false before the first value arrives, and
	// the bridge then publishes nothing rather than a zero value — an
	// unobserved combined DP must not present as a real reading.
	CombinedStatePayload() (payload string, observed bool)

	// OnCombinedChange registers fn for every subsequent value change
	// and returns the unsubscribe. The callback carries no value: the
	// bridge re-reads through CombinedStatePayload, which keeps this
	// interface free of the per-type value generics that made the
	// type-switch necessary in the first place.
	OnCombinedChange(fn func()) (unsubscribe func())
}

// CombinedWritable is the optional write half of a projection. A
// combined DP that omits it is read-only, and the command subscriber
// rejects writes to its topic rather than dropping them silently.
//
// raw is the MQTT payload verbatim. The implementation parses and
// validates it — a malformed payload is the implementation's error to
// report, not the transport's to guess at.
//
// loom:reachable:reason="asserted against in MQTTCommandSink.SetCombinedValue before it dispatches an MQTT write, and satisfied by combined.Timer and combined.EnumSelect; reached only through that interface assertion"
type CombinedWritable interface {
	WriteCombined(ctx context.Context, raw string, priority hmenum.CommandPriority) error
}

// CombinedDiscoveryContext carries the per-channel topics and label
// lookups a projection needs, mirroring [HADiscoveryContext] for custom
// data points. The bridge builds one per call so the model layer never
// handles the central name, interface, address or channel number.
//
// loom:reachable:reason="implemented by adapter.combinedDiscoveryContext and passed into every CombinedProjection.HACombinedDiscovery call the event bridge makes; the analyzer resolves neither the implementation nor the interface parameter"
type CombinedDiscoveryContext interface {
	// CombinedStateTopic is the retained topic carrying
	// [CombinedProjection.CombinedStatePayload].
	CombinedStateTopic() string

	// CombinedCommandTopic is the topic a write arrives on. A read-only
	// projection simply does not reference it.
	CombinedCommandTopic() string

	// Translate resolves a key from the daemon's own catalogues
	// (internal/i18n) into the operator's locale.
	Translate(key string) string

	// ParameterLabel resolves the CCU's own translation for a wire
	// parameter on this channel's type, as the OCCU catalogue carries
	// it. ok is false when the catalogue has no entry, which is the
	// normal case for synthetic combined parameters — callers fall back
	// to Translate.
	ParameterLabel(parameter hmenum.Parameter) (label string, ok bool)
}
