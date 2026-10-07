// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package cover

import (
	"context"
	"testing"

	clusterwire "github.com/SukramJ/go-fabric/cluster/wire"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
)

// TestGarageMatterStopReachesTheDriveInEveryClosureState pins the
// deliberate divergence recorded in notes/parity/by_design.md
// (BD-Matter-ClosureControl-StopForwarded): go-fabric's
// ClosureControl server ignores a Stop unless it considers the closure
// moving, but the projection's MainState follows the drive's SECTION
// push, which lags the physical motion. A Matter Stop must therefore
// reach the drive (DOOR_COMMAND=STOP) in every state, and exactly once
// when the cluster's own Stop handling also fires.
func TestGarageMatterStopReachesTheDriveInEveryClosureState(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name  string
		setup func(g *Garage)
		want  clusterwire.ClosureMainState
	}{
		{"nothing observed (SetupRequired)", func(*Garage) {}, clusterwire.ClosureMainStateSetupRequired},
		{"resting (Stopped)", func(g *Garage) {
			g.OnState(DoorStateClosed)
		}, clusterwire.ClosureMainStateStopped},
		{"travelling (Moving)", func(g *Garage) {
			g.OnState(DoorStateVentilation)
			g.OnSection(sectionOpening)
		}, clusterwire.ClosureMainStateMoving},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			w := &closurePriorityWriter{}
			g := NewGarage(GarageConfig{
				Channel:      newGarageChannel(t, "MOD0001:1", w),
				Writer:       w,
				Capabilities: custom.CoverCapabilities{SupportsVent: true, SupportsStop: true},
			})
			srv := g.closure.get(g)
			if srv == nil {
				t.Fatal("no closure projection was built")
			}
			c.setup(g)
			if v, _ := srv.MatterRead(clusterwire.ClosureControlAttrMainState); v != uint8(c.want) {
				t.Fatalf("MainState before Stop = %v, want %d — the case no longer tests its state", v, c.want)
			}
			if _, err := srv.MatterInvoke(context.Background(), clusterwire.ClosureControlCmdStop, nil); err != nil {
				t.Fatalf("Stop: %v", err)
			}
			if got := len(w.priorities()); got != 1 {
				t.Fatalf("Stop reached the drive %d times, want exactly 1", got)
			}
		})
	}
}
