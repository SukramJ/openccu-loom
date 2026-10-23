// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package light

import (
	"context"

	ccdef "github.com/SukramJ/go-fabric/cluster/spec/colorcontrol"
	levelcontroldef "github.com/SukramJ/go-fabric/cluster/spec/levelcontrol"
	onoffdef "github.com/SukramJ/go-fabric/cluster/spec/onoff"
	fabricdevice "github.com/SukramJ/go-fabric/device"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// matterDispatchPriority is the southbound urgency every Matter-driven
// write and invoke carries. The bridge is a controller-facing
// foreground path — a tap in a Matter app must not queue behind a
// background refresh — so it dispatches at High, and the cluster
// contract no longer negotiates it per call.
//
// Spelled out as a constant rather than left to a variable: the zero
// value of [hmenum.CommandPriority] is Critical, so anything that
// reached these calls defaulted would silently escalate every bridged
// command.
const matterDispatchPriority = hmenum.CommandPriorityHigh

// Compile-time assertion: Light participates in the Matter source
// surface (ADR 0012). Light contributes the OnOff cluster always and
// the LevelControl cluster when [custom.LightCapabilities.Dimmable] is
// set; the cluster servers are exposed via [Light.MatterClusterServers]
// rather than the Light type implementing [interfaces.MatterClusterServer]
// directly because two clusters cannot share a single MatterClusterID().
var (
	_ interfaces.MatterDeviceSource = (*Light)(nil)
	// Light inherits OnMatterValueChanged from the embedded *generic.Float
	// (LEVEL / brightness), so an external CCU-confirmed level change
	// dirty-marks the OnOff / LevelControl attributes for Apple's Subscribe.
	_ interfaces.MatterChangeNotifier = (*Light)(nil)
)

// The two device types this projection advertises and the LevelControl
// bounds the level arithmetic uses. Cluster ids, attribute ids, command
// ids, feature bits and revisions are not declared here: the clusters are
// go-fabric's servers (onoff, levelcontrol, light), whose generated
// definitions carry them. The OnOff half is pinned against the matter.js
// snapshot by TestHmLgtLightOnOffMatchesMatterJS.
const (
	matterDeviceTypeOnOffLight    = uint16(fabricdevice.OnOffLightID)
	matterDeviceTypeDimmableLight = uint16(fabricdevice.DimmableLightID)

	// Groups cluster (0x0004) and ScenesManagement (0x0062) are
	// mandatory on OnOffLight + DimmableLight (matter.js
	// packages/node/src/devices/on-off-light.ts). Light does not supply
	// them: the endpoint assembler mounts both from the node's group
	// state (go-fabric endpoint/materialize.go mountGroups / mountScenes).

	// LT (Lighting) feature-gated OnOff attributes. OnOffLight (0x0100)
	// and DimmableLight (0x0101) mandate the LT feature on the OnOff
	// cluster, which in turn makes these four attributes mandatory.
	// matter.js packages/model/src/standard/elements/on-off.element.ts:30-36:
	//   GlobalSceneControl 0x4000 bool   conformance "LT" access "R V"
	//   OnTime             0x4001 uint16 conformance "LT" access "RW VO"
	//   OffWaitTime        0x4002 uint16 conformance "LT" access "RW VO"
	//   StartUpOnOff       0x4003 enum8  conformance "LT" access "RW VM" quality "X N"

	// LT (Lighting) feature-gated LevelControl attributes. DimmableLight
	// (0x0101) mandates the LT feature on LevelControl, making these two
	// attributes mandatory.
	// matter.js packages/model/src/standard/elements/level-control.element.ts:33,67:
	//   RemainingTime       0x0001 uint16 conformance "LT" access "R V"  quality "Q"
	//   StartUpCurrentLevel 0x4000 uint8  conformance "LT" access "RW VM" quality "X N"

	// LT (Lighting) FeatureMap bits. On the OnOff cluster LT sits at
	// constraint "0" → bit 0 (0x01); matter.js on-off.element.ts:24. On
	// LevelControl LT sits at constraint "1" → bit 1 (0x02) and OO at
	// constraint "0" → bit 0 (0x01); level-control.element.ts:24-25.
	// LevelControl.Options (0x000F) is mandatory per matter.js
	// packages/model/src/standard/elements/level-control.element.ts:65
	// (conformance "M"). LevelControl.OnLevel (0x0011) is mandatory —
	// nullable uint8, null = return to previous level on transition to ON.
	// MinLevel (0x0002) and MaxLevel (0x0003) are optional attributes
	// added in LevelControl cluster revision v7 (Matter 1.5). Default
	// values: MinLevel=0x01, MaxLevel=0xFE (matching the Matter-safe
	// 1–254 range; 0xFF is the TLV null sentinel and is excluded).

	// matterLevelMinDefault is the Matter-defined default for MinLevel
	// (LevelControl cluster rev 7). MaxLevel shares its default with matterLevelMax.
	matterLevelMinDefault     uint8  = 0x01
	matterAttrFeatureMap      uint32 = 0xFFFC
	matterAttrClusterRevision uint32 = 0xFFFD

	// matterLevelMax is the maximum CurrentLevel value Matter
	// LevelControl carries. Matter reserves 0xFF (255) as the null
	// sentinel for nullable uint8 encoding, so HM's 0.0–1.0 float maps
	// to 0–254, *not* 0–255 like [custom.Brightness.Byte].
	matterLevelMax uint8 = 0xFE
)

// MatterDeviceType implements [interfaces.MatterDeviceSource]. Maps
// the dimmable / non-dimmable distinction onto Matter's two basic
// light device types (0x0101 / 0x0100).
func (l *Light) MatterDeviceType() uint16 {
	if l.Capabilities.Dimmable {
		return matterDeviceTypeDimmableLight
	}
	return matterDeviceTypeOnOffLight
}

// MatterDevice implements [interfaces.MatterDeviceSource]: an OnOffLight
// or, dimmable, a DimmableLight built from the light's declarations.
func (l *Light) MatterDevice(opts fabricdevice.Options) (*fabricdevice.Device, error) {
	return l.matterLightDevice(l, l.MatterDeviceType(), opts, nil)
}

// matterLightDevice builds the device of a light once: the OnOff cluster
// declared (Lighting; Sink [Light.TurnOn] / [Light.TurnOff] at
// [matterDispatchPriority]; the observed brightness as the start, false
// while nothing was observed — the OnOff attribute is mandatory and
// non-nullable, on-off.element.ts:29, and Apple Home drops an accessory
// whose first report carries no boolean); on a dimmable light the
// LevelControl cluster declared on the dimmer's own ramp ([lightRamp]);
// and the ColorControl a colour variant declares (color). source is the
// variant that builds the device (any: Light or one of the types that
// embed it), whose change notifier the device carries. A timed-on ends
// through the kit server's countdown (OnOffServer.ts:216-232, whose off
// reaches the Sink) — no CCU-side ON_TIME is written for it.
//
// The LEVEL data point's confirmed-update hook feeds the CCU's state back
// through [generic.SetMatterOnOff], the one path that stores without
// asking the Sink, so a wall switch or a CCU program reaches a subscribed
// controller and a confirmed command does not echo into a second write.
//
// matter.js packages/node/src/devices/on-off-light.ts also lists Groups
// (0x0004) and ScenesManagement (0x0062) as mandatory server clusters.
// Those are stack state, not device state: the endpoint assembler mounts
// both from the node's group state, so the light declares neither.
func (l *Light) matterLightDevice(source any, deviceType uint16, opts fabricdevice.Options, color *colorPlan) (*fabricdevice.Device, error) {
	return l.matterDevice.Device(func() (*fabricdevice.Device, error) {
		on, _ := l.IsOn()
		decls := []fabricdevice.Declaration{
			generic.MatterOnOffDeclaration(true, on, func(ctx context.Context, on bool) error {
				if on {
					return l.TurnOn(ctx, matterDispatchPriority)
				}
				return l.TurnOff(ctx, matterDispatchPriority)
			}),
		}
		if l.Capabilities.Dimmable {
			decls = append(decls, levelcontroldef.Declare{Ramp: lightRamp{l: l}})
		}
		if color != nil {
			decls = append(decls, color.declare)
		}
		d, err := generic.NewMatterDevice(source, deviceType, opts, decls...)
		if err != nil {
			return nil, err
		}
		if l.Float != nil {
			_ = l.OnConfirmedUpdate(func(_, _ float64) {
				on, _ := l.IsOn()
				generic.SetMatterOnOff(d, on)
			})
		}
		// A confirmation that landed between the initial read and the
		// hook registration is not lost: the current value is stored
		// again, which is a no-op when nothing changed.
		if on, observed := l.IsOn(); observed {
			generic.SetMatterOnOff(d, on)
		}
		if color != nil {
			if srv := colorInboundOf(d); srv != nil {
				color.bind(srv)
			}
		}
		return d, nil
	})
}

// matterClusterIDs lists the clusters the light's device serves, for an
// eligibility verdict stated before any device is built: OnOff,
// LevelControl on a dimmable light, and ColorControl on a colour variant.
func (l *Light) matterClusterIDs(color bool) []uint32 {
	ids := []uint32{onoffdef.ClusterID}
	if l.Capabilities.Dimmable {
		ids = append(ids, levelcontroldef.ClusterID)
	}
	if color {
		ids = append(ids, ccdef.ClusterID)
	}
	return ids
}

// brightnessToMatter encodes an HM brightness (0.0–1.0 float) into
// Matter LevelControl's CurrentLevel byte. The HM helper
// [custom.Brightness.Byte] returns 0–255 for HM-internal byte
// serialisation; that range collides with Matter's null-sentinel 255,
// so go-fabric's [levelcontroldef.LevelFromUnit] rounds onto 0..254 instead.
func brightnessToMatter(b custom.Brightness) uint8 {
	return levelcontroldef.LevelFromUnit(b.Level())
}

// matterLevelToHM decodes Matter LevelControl CurrentLevel (0..254) back
// into the 0.0–1.0 HM float range.
func matterLevelToHM(m uint8) float64 {
	return levelcontroldef.UnitFromLevel(m)
}
