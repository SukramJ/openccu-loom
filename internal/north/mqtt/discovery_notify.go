// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"encoding/json"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// textDisplaySource is the narrow read-side contract used to recognise a
// text-display custom-DP (HmIP-WRCD) on the discovery path. The
// custom-DP reports DataPointCategoryTextDisplay via its Category method.
type textDisplaySource interface {
	Category() hmenum.DataPointCategory
}

// isTextDisplayEvent reports whether ev carries a text-display custom-DP
// as its Source.
func isTextDisplayEvent(ev Event) bool {
	if ev.Source == nil {
		return false
	}
	tds, ok := ev.Source.(textDisplaySource)
	return ok && tds.Category() == hmenum.DataPointCategoryTextDisplay
}

// BuildTextDisplayNotify emits the HA `notify` discovery payload for a
// text-display custom-DP (HmIP-WRCD). This is the SOLE entity the
// reference stack creates for a TEXT_DISPLAY custom-DP.
//
// The reference stack maps a TEXT_DISPLAY custom-DP onto a `notify`
// entity only (the integration's notify.py spawns one
// HmipTextDisplayNotifyEntity per CustomDpTextDisplay; no `text` entity
// is registered). The notify surface lets HA automations and the notify
// service push a message to the display. The aggregate `text` entity is
// suppressed in [DefaultDiscoveryBuilder.aggregateChannel].
//
// HA's notify component publishes the raw message string to the command
// topic. The `command_template` wraps it into the `{"id":1,"text":...}`
// payload the custom-DP's `write` service method expects (display row 1).
//
// Returns the zero DiscoveryItem (OK=false) when ev does not carry a
// text-display custom-DP.
func (d *DefaultDiscoveryBuilder) BuildTextDisplayNotify(ev Event) DiscoveryItem {
	if !isTextDisplayEvent(ev) {
		return DiscoveryItem{}
	}
	objectID := d.channelObjectID(ev, "notify")
	uniqueID, scoped := d.channelUniqueID(ev, "notify")
	if !scoped {
		return DiscoveryItem{}
	}
	nodeID := discoveryNodeID(d.centralFor(ev), ev.DeviceAddress)
	commandTopic := d.discoveryContext(ev).ServiceMethodCommandTopic("write")
	if commandTopic == "" {
		return DiscoveryItem{}
	}
	// Reuse the channel scaffolding (availability, device block, origin)
	// so the notify entity groups under the same HA device card as the
	// text entity. The name mirrors the text entity (empty → HA falls
	// back to the device name) so the friendly_name reads as the display
	// name alone, matching the reference notify entity.
	base := d.channelBaseBody(ev, displayChannelName(ev), uniqueID)
	body := map[string]any{
		"command_topic": commandTopic,
		// HA publishes the raw message; wrap it into the write payload
		// (display row 1). tojson quotes/escapes the message safely.
		"command_template": `{"id": 1, "text": {{ value | tojson }}}`,
	}
	for k, v := range base {
		body[k] = v
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return DiscoveryItem{}
	}
	return DiscoveryItem{Component: string(HAComponentNotify), NodeID: nodeID, ObjectID: objectID, Payload: buf, OK: true}
}
