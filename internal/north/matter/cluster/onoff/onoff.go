// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package onoff carries the OnOff cluster's Matter identity for every
// projection that exposes it.
//
// Four projections declared this block independently — internal/model/generic
// plus the switch, light and siren device profiles — with the matter.js
// citation copied along rather than the values. They had already drifted: the
// siren omitted Toggle while advertising the FeatureMap that makes it
// mandatory, which is the shape a controller can end a commissioning over.
// A revision bump or a newly feature-gated attribute had four edit sites and
// nothing tying them together.
//
// The revision is not restated here either: it is read from the generated
// matter.js snapshot ([schema.ClusterRevisions]), which is the only copy in
// the repository that a regeneration keeps current.
package onoff

import "github.com/SukramJ/openccu-loom/internal/north/matter/schema"

// ClusterID is the OnOff cluster id.
const ClusterID uint32 = 0x0006

// Device types whose projections carry this cluster.
const (
	// DeviceTypeOnOffLight is the OnOffLight device type (0x0100).
	DeviceTypeOnOffLight uint16 = 0x0100
	// DeviceTypeOnOffPlugInUnit is the OnOffPlugInUnit device type (0x010A).
	// It marks the LT feature mandatory on this cluster
	// (matter.js on-off-plug-in-unit.element.ts), which is why the LT-gated
	// attributes and commands below are not optional for it.
	DeviceTypeOnOffPlugInUnit uint16 = 0x010A
)

// Attribute ids. The four 0x40xx attributes carry conformance "LT"
// (matter.js packages/model/src/standard/elements/on-off.element.ts:30-36),
// so they exist exactly while [FeatureLighting] is advertised.
const (
	AttrOnOff              uint32 = 0x0000
	AttrGlobalSceneControl uint32 = 0x4000
	AttrOnTime             uint32 = 0x4001
	AttrOffWaitTime        uint32 = 0x4002
	AttrStartUpOnOff       uint32 = 0x4003
)

// FeatureLighting is the LT (Lighting) FeatureMap bit: constraint "0" → bit 0
// (matter.js on-off.element.ts:24).
const FeatureLighting uint32 = 0x01

// Command ids. Off is mandatory unconditionally; On and Toggle carry
// conformance "!OFFONLY" and so are mandatory unless the cluster advertises
// the OffOnly feature (on-off.element.ts:37-39); the three 0x4x commands
// carry "LT" (:41,:46,:51).
const (
	CmdOff                     uint32 = 0x00
	CmdOn                      uint32 = 0x01
	CmdToggle                  uint32 = 0x02
	CmdOffWithEffect           uint32 = 0x40
	CmdOnWithRecallGlobalScene uint32 = 0x41
	CmdOnWithTimedOff          uint32 = 0x42
)

// Revision returns the cluster revision from the generated matter.js schema
// snapshot. Reading it rather than restating it is the point: a regeneration
// moves this value, and a hand-written copy would not follow.
func Revision() uint16 { return schema.ClusterRevisions[ClusterID] }

// LightingAttributes returns the attribute ids an OnOff cluster advertising
// [FeatureLighting] must expose, in id order.
func LightingAttributes() []uint32 {
	return []uint32{AttrOnOff, AttrGlobalSceneControl, AttrOnTime, AttrOffWaitTime, AttrStartUpOnOff}
}

// LightingCommands returns the accepted command ids for an OnOff cluster
// advertising [FeatureLighting] and not OffOnly, in id order.
func LightingCommands() []uint32 {
	return []uint32{CmdOff, CmdOn, CmdToggle, CmdOffWithEffect, CmdOnWithRecallGlobalScene, CmdOnWithTimedOff}
}
