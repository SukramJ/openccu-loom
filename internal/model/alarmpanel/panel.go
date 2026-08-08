// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package alarmpanel models the alarm-control-panel entity: the
// HA-facing projection of an alarm zone (and of the aggregate master
// panel). The projection — state tokens, command mapping, supported
// features — lives here so REST, WebSocket, and MQTT can never
// diverge; the north adapters consume this package instead of
// mapping states themselves.
package alarmpanel

import (
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// MasterZoneID is the pseudo-zone identifier of the aggregate panel.
const MasterZoneID = "master"

// Panel is one alarm-control-panel entity.
type Panel struct {
	// UniqueID is the stable entity identifier
	// (openccu-loom_alarm_<zoneID>); it doubles as the MQTT discovery
	// unique_id and must never change for an existing zone.
	UniqueID string
	// ZoneID is the alarm zone, or MasterZoneID for the aggregate.
	ZoneID string
	// Name is the display name (the zone name; the master panel name
	// comes from the i18n catalogs).
	Name string
	// Modes lists the armable protection levels.
	Modes []hmenum.AlarmMode
	// State is the HA state token (see StateToken).
	State string
	// Available reports the alarm-health verdict for the entity.
	Available bool
	// Master marks the aggregate panel.
	Master bool
	// CodeArmRequired / CodeDisarmRequired carry the zone's effective
	// per-verb code policy (notes/concepts/alarm-concept.md §11/§13.3): the
	// zone-config policy half AND the "an applicable enabled pin code
	// exists" half, exactly as the engine will enforce them — so a
	// client prompts for a code precisely when one is needed. The
	// master aggregate carries the any-zone-requires union: a client
	// driving the aggregate fans the entered code out to the per-zone
	// verbs.
	CodeArmRequired    bool
	CodeDisarmRequired bool
}

// Category returns the model taxonomy category of the entity.
func (Panel) Category() hmenum.DataPointCategory {
	return hmenum.DataPointCategoryAlarmControlPanel
}

// PanelUniqueID derives the stable unique ID of an zone's panel.
func PanelUniqueID(zoneID string) string { return "openccu-loom_alarm_" + zoneID }
