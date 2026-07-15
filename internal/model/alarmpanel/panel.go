// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package alarmpanel models the alarm-control-panel entity: the
// HA-facing projection of an alarm area (and of the aggregate master
// panel). The projection — state tokens, command mapping, supported
// features — lives here so REST, WebSocket, and MQTT can never
// diverge; the north adapters consume this package instead of
// mapping states themselves.
package alarmpanel

import (
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// MasterAreaID is the pseudo-area identifier of the aggregate panel.
const MasterAreaID = "master"

// Panel is one alarm-control-panel entity.
type Panel struct {
	// UniqueID is the stable entity identifier
	// (openccu-loom_alarm_<areaID>); it doubles as the MQTT discovery
	// unique_id and must never change for an existing area.
	UniqueID string
	// AreaID is the alarm area, or MasterAreaID for the aggregate.
	AreaID string
	// Name is the display name (the area name; the master panel name
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
}

// Category returns the model taxonomy category of the entity.
func (Panel) Category() hmenum.DataPointCategory {
	return hmenum.DataPointCategoryAlarmControlPanel
}

// PanelUniqueID derives the stable unique ID of an area's panel.
func PanelUniqueID(areaID string) string { return "openccu-loom_alarm_" + areaID }
