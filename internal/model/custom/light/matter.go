// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package light

import (
	"context"

	"github.com/SukramJ/go-fabric/cluster/onoff"

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

// MatterDataVersion implements [interfaces.MatterClusterDataVersion].
// Shared across all cluster servers that project this Light (OnOff,
// LevelControl, ColorControl). Bumped on every successful write /
// invoke so DataVersionFilter evaluation correctly detects cluster changes.
func (l *Light) MatterDataVersion() uint32 { return l.dataVersion.Current() }

// Compile-time assertion: Light participates in the Matter source
// surface (ADR 0012). Light contributes the OnOff cluster always and
// the LevelControl cluster when [custom.LightCapabilities.Dimmable] is
// set; the cluster servers are exposed via [Light.MatterClusterServers]
// rather than the Light type implementing [interfaces.MatterClusterServer]
// directly because two clusters cannot share a single MatterClusterID().
var (
	_ interfaces.MatterEndpointSource     = (*Light)(nil)
	_ interfaces.MatterClusterDataVersion = (*Light)(nil)
	// Light inherits OnMatterValueChanged from the embedded *generic.Float
	// (LEVEL / brightness), so an external CCU-confirmed level change
	// dirty-marks the OnOff / LevelControl attributes for Apple's Subscribe.
	_ interfaces.MatterChangeNotifier = (*Light)(nil)
)

// Matter Device Type IDs and cluster IDs follow the Matter 1.5.1
// Application Cluster Specification. The OnOff command ids come from
// go-fabric's cluster/wire, which owns the wire contract; the
// device types, cluster ids, LT attribute ids, the LT feature bit and the
// cluster revisions are declared here next to the projection that
// advertises them. The OnOff half is pinned against the matter.js
// snapshot by TestHmLgtLightOnOffMatchesMatterJS, so the ids are not
// reviewed by eye against a second hand-written list.
const (
	matterDeviceTypeOnOffLight    uint16 = 0x0100
	matterDeviceTypeDimmableLight uint16 = 0x0101

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

	// The six OnOff command IDs are the wire contract, so they are read
	// from the package that owns it —
	// go-fabric cluster/wire/onoff.go — instead of being
	// transcribed a second time here. matter.js
	// packages/model/src/standard/elements/on-off.element.ts marks Off "M",
	// On and Toggle "!OFFONLY", and the three 0x4x commands "LT": all six
	// are mandatory for the FeatureMap this projection advertises.

	// matterLevelControlClusterRevision pinned to matter.js HEAD
	// (@matter/model 0.16.11). Matter 1.5 bumped the revision from 6 to 7
	// with additional level-control refinements (OnTransitionTime /
	// OffTransitionTime were in 1.4 at rev 6; rev 7 is the 1.5 update).

	// matterLevelMax is the maximum CurrentLevel value Matter
	// LevelControl carries. Matter reserves 0xFF (255) as the null
	// sentinel for nullable uint8 encoding, so HM's 0.0–1.0 float maps
	// to 0–254, *not* 0–255 like [custom.Brightness.Byte].
	matterLevelMax uint8 = 0xFE
)

// MatterDeviceType implements [interfaces.MatterEndpointSource]. Maps
// the dimmable / non-dimmable distinction onto Matter's two basic
// light device types (0x0101 / 0x0100).
func (l *Light) MatterDeviceType() uint16 {
	if l.Capabilities.Dimmable {
		return matterDeviceTypeDimmableLight
	}
	return matterDeviceTypeOnOffLight
}

// MatterClusterServers implements [interfaces.MatterEndpointSource].
// A non-dimmable light contributes OnOff; a dimmable light
// additionally contributes LevelControl.
// matter.js packages/node/src/devices/on-off-light.ts also lists Groups
// (0x0004) and ScenesManagement (0x0062) as mandatory server clusters.
// Those are stack state, not device state: the endpoint assembler mounts
// both from the node's group state (go-fabric endpoint/materialize.go
// mountGroups / mountScenes), so the source supplies neither.
func (l *Light) MatterClusterServers() []interfaces.MatterClusterServer {
	if l.Capabilities.Dimmable {
		return []interfaces.MatterClusterServer{
			l.matterOnOffServer(),
			l.matterLevelServer(),
		}
	}
	return []interfaces.MatterClusterServer{
		l.matterOnOffServer(),
	}
}

// matterOnOffServer builds the light's OnOff server once (ADR 0084 phase
// 2): go-fabric's [onoff.Server] owns the attributes, the six commands,
// the OnTime / OffWaitTime countdowns and the scene paths. The Sink is
// [Light.TurnOn] / [Light.TurnOff] at [matterDispatchPriority]; the LEVEL
// data point's confirmed-update hook feeds the CCU's state back through
// [onoff.Server.Set], the one path that stores without asking the Sink,
// so a wall switch or a CCU program reaches a subscribed controller and a
// confirmed command does not echo into a second write. A timed-on ends
// through the stack's countdown (OnOffServer.ts:102, whose off reaches
// the Sink) — no CCU-side ON_TIME is written for it.
//
// The server starts from the observed brightness, false while nothing was
// observed: the OnOff attribute is mandatory and non-nullable
// (on-off.element.ts:29), and Apple Home drops an accessory whose first
// report carries no boolean.
func (l *Light) matterOnOffServer() *onoff.Server {
	return l.matterOnOff.Server(func() *onoff.Server {
		on, _ := l.IsOn()
		srv := generic.MustMatterOnOffServer(generic.MatterOnOffConfig{
			Lighting:    true,
			DataVersion: &l.dataVersion,
			Initial:     on,
			Sink: func(ctx context.Context, on bool) error {
				if on {
					return l.TurnOn(ctx, matterDispatchPriority)
				}
				return l.TurnOff(ctx, matterDispatchPriority)
			},
		})
		if l.Float != nil {
			_ = l.OnConfirmedUpdate(func(_, _ float64) {
				on, _ := l.IsOn()
				srv.Set(on)
			})
		}
		// A confirmation that landed between the initial read and the
		// hook registration is not lost: the current value is stored
		// again, which is a no-op when nothing changed.
		if on, observed := l.IsOn(); observed {
			srv.Set(on)
		}
		return srv
	})
}

// brightnessToMatter encodes an HM brightness (0.0–1.0 float) into
// Matter LevelControl's CurrentLevel byte. The HM helper
// [custom.Brightness.Byte] returns 0–255 for HM-internal byte
// serialisation; that range collides with Matter's null-sentinel 255,
// so this function clamps to [matterLevelMax] instead.
func brightnessToMatter(b custom.Brightness) uint8 {
	v := b.Level() * float64(matterLevelMax)
	if v < 0 {
		return 0
	}
	if v > float64(matterLevelMax) {
		return matterLevelMax
	}
	return uint8(v + 0.5)
}

// matterLevelToHM decodes Matter LevelControl CurrentLevel back into
// the 0.0–1.0 HM float range. Values ≥ matterLevelMax saturate to 1.0.
func matterLevelToHM(m uint8) float64 {
	if m >= matterLevelMax {
		return 1.0
	}
	return float64(m) / float64(matterLevelMax)
}
