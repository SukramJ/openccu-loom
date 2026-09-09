// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"encoding/json"
	"errors"
	"fmt"

	hacatalog "github.com/SukramJ/go-ha-catalog"
	hadiscovery "github.com/SukramJ/go-hamqtt/discovery"
)

// discoveryKeysHomeAssistantIgnores are keys this daemon publishes on purpose
// even though Home Assistant's MQTT discovery schema does not declare them and
// therefore drops them on receipt.
//
// There is exactly one, and it earns its place: `translation_key` is what the
// cross-stack parity tooling compares openccu-loom against the Python
// integration with (script/discovery_snapshot_diff.py, and the assertions in
// hub_singletons_parity_test.go). It is inert on the wire — the comment in
// discovery_aggregate.go's aggregateChannel says so — and removing it would
// cost the parity signal to fix nothing.
//
// The list is deliberately a declaration rather than a tolerance: a key that
// is not on it and not in the schema is a defect, because the alternative is
// that Home Assistant discards it in silence and nobody ever learns.
var discoveryKeysHomeAssistantIgnores = map[string]bool{
	"translation_key": true,
}

// errEmptyDiscoveryComponent is what a caller gets for a body it cannot name a
// platform for. It is an error rather than a silent pass: "no component" must
// never read as "no problems found".
var errEmptyDiscoveryComponent = errors.New("discovery validation: empty component")

// ValidateDiscoveryBody checks one marshalled discovery config against the
// Home Assistant schema for its component.
//
// It returns a blocking error only for what Home Assistant refuses or silently
// drops; things it accepts and rewrites come back as an advisory, matched by
// errors.Is(err, hadiscovery.ErrAdvisory). A caller deciding whether to
// publish should test for hadiscovery.ErrInvalidBundle, which advisories do
// not match.
func ValidateDiscoveryBody(component string, payload []byte) error {
	if component == "" {
		return errEmptyDiscoveryComponent
	}
	var body map[string]any
	if err := json.Unmarshal(payload, &body); err != nil {
		return fmt.Errorf("discovery validation: %s: %w", component, err)
	}
	for key := range discoveryKeysHomeAssistantIgnores {
		delete(body, key)
	}
	return hadiscovery.ValidateBody(hacatalog.Platform(component), body)
}
