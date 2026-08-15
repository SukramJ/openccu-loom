// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package payload

// LocalisableSelection declares one discovery-body list whose entries
// are CCU VALUE_LIST tokens an operator reads on screen.
//
// One declaration drives both directions, which is the point: the
// forward half replaces the tokens with labels in the discovery payload,
// the reverse half turns a label the operator picked back into the token
// the device speaks. Declaring them apart is how the two drift, and a
// drifted pair is a control that looks translated and does nothing.
//
// loom:reachable:reason="constructed inside the LocalisableSelections implementations of the siren, sound-player and effect-light custom data points, which production reaches through a payload.LocalisableSelections interface assertion in the event bridge and the MQTT command sink; the analyzer's callgraph does not resolve values produced behind an interface assertion"
type LocalisableSelection struct {
	// BodyKey is the Home Assistant discovery field carrying the list
	// (e.g. "available_tones", "effect_list").
	BodyKey string
	// ArgKey is the service-method argument the choice comes back in
	// (e.g. "tone", "effect").
	ArgKey string
	// Parameter is the wire parameter whose VALUE_LIST defines the
	// entries (e.g. "ACOUSTIC_ALARM_SELECTION", "PROGRAM").
	Parameter string
}

// LocalisableSelections is implemented by a custom data point whose
// discovery payload carries lists of the shape above.
//
// Home Assistant renders an MQTT entity's lists verbatim: unlike a
// native integration there is no translation file behind a discovered
// entity, so a raw "FREQUENCY_RISING" stays on screen forever.
//
// loom:reachable:reason="asserted against in EventBridge.selectionLabelsFor and MQTTCommandSink.resolveSelectionLabels before either localises a list or resolves a label back to its wire token, and the siren, sound-player and effect-light custom data points satisfy it; a type reached only through an interface assertion, which the analyzer's callgraph does not resolve"
type LocalisableSelections interface {
	LocalisableSelections() []LocalisableSelection
}
