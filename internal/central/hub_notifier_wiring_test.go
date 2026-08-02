// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package central

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// TestNewWiresHubModelNotifiers pins the boot wiring between the hub domain
// model and the hub coordinator. The coordinator-level notifier tests drive
// [coordinators.HubCoordinator.SetHubModel] themselves, so they stay green
// even when no production code path ever calls it — which is exactly what
// happened: the hub scan registered programs and system variables on
// Unit.HubModel, their notifier hooks stayed nil, and no activity flip,
// execution, or sysvar value change ever reached the event bus. Every
// bus-driven consumer (the WebSocket broadcasts above all) was silent while
// the REST surface answered correctly from the model.
//
// This test therefore goes through [New] alone and only touches the surfaces
// the real daemon touches: the model the scan writes to and the bus the
// north-bound adapters subscribe on.
func TestNewWiresHubModelNotifiers(t *testing.T) {
	t.Parallel()

	u, err := New(Config{Name: "main"})
	if err != nil {
		t.Fatal(err)
	}

	var programChanges []hmevent.ProgramChangedEvent
	unsubProg := events.Subscribe(u.EventBus, func(e hmevent.ProgramChangedEvent) {
		programChanges = append(programChanges, e)
	})
	defer unsubProg()

	var sysvarChanges []hmevent.SysvarChangedEvent
	unsubSv := events.Subscribe(u.EventBus, func(e hmevent.SysvarChangedEvent) {
		sysvarChanges = append(sysvarChanges, e)
	})
	defer unsubSv()

	// Register exactly like the hub scan does: PutProgram / PutSysvar on the
	// model, then feed observations through OnActive / OnValue.
	prog := hub.NewProgram("main", "4711", "Testprogramm", "", false, nil)
	prog.OnActive(true)
	u.HubModel.PutProgram(prog)
	prog.OnActive(false)

	sv := hub.NewSysvar("main", "TestVar", "", hmenum.HubValueTypeLogic, nil)
	u.HubModel.PutSysvar(sv)
	sv.OnValue(hmtypes.BoolValue(true))

	if len(programChanges) != 1 {
		t.Fatalf("program activity flip on the bus: got %d events, want 1 (%+v)", len(programChanges), programChanges)
	}
	if got := programChanges[0]; got.ProgramID != "4711" || got.Active {
		t.Fatalf("program change = %+v, want ProgramID=4711 Active=false", got)
	}
	if len(sysvarChanges) != 1 {
		t.Fatalf("sysvar value change on the bus: got %d events, want 1 (%+v)", len(sysvarChanges), sysvarChanges)
	}
	if got := sysvarChanges[0]; got.Name != "TestVar" {
		t.Fatalf("sysvar change = %+v, want Name=TestVar", got)
	}
}
