// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package wire

import (
	"encoding/json"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/generic"
	matterparity "github.com/SukramJ/openccu-loom/internal/north/matter/parity"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// matter.js's MatterDefinition is the wire-level reference. The wire
// package implements Switch / GenericSwitch / AdministratorCommissioning
// / Schedules cluster servers; we pin every cluster ID + revision
// against matter.js HEAD here so a stale revision does not pass review.

type matterCluster struct {
	ID       uint32 `json:"id"`
	Name     string `json:"name"`
	Revision uint16 `json:"revision"`
}

type matterSchema struct {
	Clusters []matterCluster `json:"clusters"`
}

func loadMatterSchemaT(t *testing.T) *matterSchema {
	t.Helper()
	var s matterSchema
	if err := json.Unmarshal(matterparity.SchemaJSON(), &s); err != nil {
		t.Fatalf("unmarshal embedded matter-schema-snapshot.json: %v", err)
	}
	return &s
}

func clusterByID(s *matterSchema, id uint32) (matterCluster, bool) {
	for _, c := range s.Clusters {
		if c.ID == id {
			return c, true
		}
	}
	return matterCluster{}, false
}

// TestParityMatterJS_WireClusterRevisions asserts every wire-package
// cluster server pins the same revision matter.js HEAD ships.
//
// The Schedules cluster (0x0024) is intentionally not in the list:
// matter.js does not define it (verified against @matter/model 0.16.11),
// and exposing it makes Apple Home's HAP
// service mapper reject the endpoint. The Schedules code stays in
// the package for revival once the Matter spec ships a canonical
// schedule cluster — but the bridge no longer attaches the server.
func TestParityMatterJS_WireClusterRevisions(t *testing.T) {
	t.Parallel()
	schema := loadMatterSchemaT(t)
	cases := []struct {
		id           uint32
		name         string
		codeRevision uint16
	}{
		{matterClusterAdminCommissioning, "AdministratorCommissioning", admCommClusterRevision},
		{matterClusterGenericSwitch, "Switch", switchClusterRevision},
		// Groups (0x0004) and ScenesManagement (0x0062) are mandatory stubs
		// on every OnOff device type. Their revisions are pinned here so a
		// matter.js HEAD bump is caught automatically on the next schema
		// regeneration run.
		// matter.js packages/model/src/standard/elements/groups.element.ts
		// matter.js packages/model/src/standard/elements/scenes-management.element.ts
		{groupsClusterID, "Groups", groupsClusterRevision},
		{scenesManagementClusterID, "ScenesManagement", scenesManagementClusterRevision},
	}
	for _, c := range cases {
		js, ok := clusterByID(schema, c.id)
		if !ok {
			t.Errorf("matter.js schema has no cluster 0x%04X (%s)", c.id, c.name)
			continue
		}
		t.Run(js.Name, func(t *testing.T) {
			t.Parallel()
			if c.codeRevision != js.Revision {
				t.Errorf("code revision %d != matter.js %d for %s (0x%04X)",
					c.codeRevision, js.Revision, js.Name, js.ID)
			}
		})
	}
}

// pressCycleRecorder records the event IDs the GenericSwitch emits, in
// order, so the parity cases below can assert the full gesture sequence.
type pressCycleRecorder struct {
	events []uint32
}

func (r *pressCycleRecorder) MatterEmitEvent(_ uint16, _, event uint32, _ any, _ interfaces.MatterEventPriority) {
	r.events = append(r.events, event)
}

// TestParityMatterJS_GenericSwitchPressCycleSequences pins the
// end-to-end press-cycle event ordering (model button group → wire
// cluster → emitter) against matter.js
// packages/node/src/behaviors/switch/SwitchServer.ts:
//
//   - a short press yields InitialPress then ShortRelease
//     (#handleSwitchPositionChange: initialPress on the move away from
//     momentaryNeutralPosition; shortRelease on return while the
//     longPress timer is still running);
//   - a hold yields InitialPress, then exactly ONE LongPress
//     (#handleLongPress fires once per hold), then LongRelease on the
//     return to neutral — device-side repeats (PRESS_CONT frames)
//     produce no extra events;
//   - LongRelease is only ever emitted after a LongPress since the
//     previous InitialPress (the `currentIsLongPress` gate), so a
//     button without a release parameter must complete the full
//     sequence per long press.
func TestParityMatterJS_GenericSwitchPressCycleSequences(t *testing.T) {
	t.Parallel()

	pressDP := func(p hmenum.Parameter) *generic.Button {
		return generic.NewButton(generic.Spec{
			Key: hmtypes.DataPointKey{
				InterfaceID:    "iface",
				ChannelAddress: "BTN0001:1",
				ParamsetKey:    hmenum.ParamsetKeyValues,
				Parameter:      string(p),
			},
			Descriptor: hmproto.ParameterData{
				Type:       hmenum.ParameterTypeAction,
				Operations: hmenum.OperationsEvent,
			},
		})
	}
	assertSeq := func(t *testing.T, rec *pressCycleRecorder, want ...uint32) {
		t.Helper()
		if len(rec.events) != len(want) {
			t.Fatalf("event id sequence = %v, want %v", rec.events, want)
		}
		for i := range want {
			if rec.events[i] != want[i] {
				t.Fatalf("event id sequence = %v, want %v", rec.events, want)
			}
		}
	}
	wireUp := func(t *testing.T, buttons ...*generic.Button) *pressCycleRecorder {
		t.Helper()
		srcs := make([]generic.PressEventSource, 0, len(buttons))
		for _, b := range buttons {
			srcs = append(srcs, b)
		}
		group := generic.NewButtonGroup(srcs...)
		if group == nil {
			t.Fatal("button group did not construct")
		}
		gs := NewGenericSwitch(7, group)
		rec := &pressCycleRecorder{}
		gs.SetMatterEventEmitter(rec)
		t.Cleanup(group.WireMatterSwitchHandler(gs))
		return rec
	}

	t.Run("short_press", func(t *testing.T) {
		t.Parallel()
		short := pressDP(hmenum.ParameterPressShort)
		rec := wireUp(t, short, pressDP(hmenum.ParameterPressLong))
		short.OnEvent(true)
		assertSeq(t, rec, MatterEventInitialPress, MatterEventShortRelease)
	})

	t.Run("hold_with_release", func(t *testing.T) {
		t.Parallel()
		long := pressDP(hmenum.ParameterPressLong)
		cont := pressDP(hmenum.ParameterPressCont)
		release := pressDP(hmenum.ParameterPressLongRelease)
		rec := wireUp(t, long, cont, release)
		long.OnEvent(true)
		cont.OnEvent(true) // ~300 ms continuation repeats → suppressed
		cont.OnEvent(true)
		release.OnEvent(true)
		assertSeq(t, rec, MatterEventInitialPress, MatterEventLongPress, MatterEventLongRelease)
	})

	t.Run("hold_without_release_parameter", func(t *testing.T) {
		t.Parallel()
		long := pressDP(hmenum.ParameterPressLong)
		rec := wireUp(t, pressDP(hmenum.ParameterPressShort), long)
		long.OnEvent(true)
		assertSeq(t, rec, MatterEventInitialPress, MatterEventLongPress, MatterEventLongRelease)
	})
}

// TestParityMatterJS_SchedulesClusterIsNotShipped guards the decision
// to remove the Schedules cluster (0x0024) from the bridge surface
// (see [climate.Climate.MatterClusterServers]). matter.js does not
// define a cluster with that ID; advertising it causes Apple Home
// pair-abort. If matter.js ever ships a
// Schedules cluster at 0x0024, this test fires so the bridge can
// re-attach the server.
func TestParityMatterJS_SchedulesClusterIsNotShipped(t *testing.T) {
	t.Parallel()
	schema := loadMatterSchemaT(t)
	if _, ok := clusterByID(schema, SchedulesClusterID); ok {
		t.Errorf("matter.js HEAD now defines a cluster at 0x%04X — re-attach the Schedules server in climate.MatterClusterServers and update this test",
			SchedulesClusterID)
	}
}
