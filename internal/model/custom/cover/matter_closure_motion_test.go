// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package cover

import (
	"testing"

	clusterwire "github.com/SukramJ/openccu-loom/internal/north/matter/cluster/wire"
)

// TestClosureMainStateFollowsTheModelsMotion pins the Matter MainState to the
// model's own motion predicates rather than to a position reading.
//
// The cluster server used to infer it: a null CurrentPosition meant MOVING.
// DOOR_STATE has no travelling value at all, so an UNKNOWN door state produced
// a null position and therefore a permanently moving door in Apple and Google
// Home — with no push able to correct it, because SECTION (the drive's actual
// motion signal) never reached the projection.
//
// The sibling window-covering projection has always consulted IsOpening /
// IsClosing through motionForOpeningClosing; this is the same rule for the
// closure.
func TestClosureMainStateFollowsTheModelsMotion(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		what             string
		opening, closing bool
		want             clusterwire.ClosureMainState
	}{
		{"opening", true, false, clusterwire.ClosureMainStateMoving},
		{"closing", false, true, clusterwire.ClosureMainStateMoving},
		{"stationary", false, false, clusterwire.ClosureMainStateStopped},
	} {
		if got := closureMainStateFor(c.opening, c.closing); got != c.want {
			t.Errorf("%s: MainState = %v, want %v", c.what, got, c.want)
		}
	}
}

// TestGarageSectionReachesTheClusterServer pins the whole push path: a SECTION
// value arriving from the CCU must land on the cluster's MainState attribute.
//
// Two things had to be true for that and neither was. OnState fed the Matter
// projection and OnSection did not, so the only signal reaching the cluster
// was one that carries no motion; and the cluster derived MainState from the
// position instead of being told. A door that started moving therefore stayed
// reported as stopped until it arrived — and an UNKNOWN door state reported
// motion that was never happening.
//
// Asserted through the cluster server rather than through the helper, because
// a helper test passes while the call that uses it is missing.
func TestGarageSectionReachesTheClusterServer(t *testing.T) {
	t.Parallel()
	g := &Garage{}
	// The projection reads DoorState first and returns early when nothing has
	// been observed, so the drive has to have reported once.
	g.OnState(DoorStateClosed)
	srv := g.closure.get(g)
	if srv == nil {
		t.Fatal("no closure projection was built — the guard lost its subject")
	}

	g.OnSection(sectionOpening)
	if got, _ := srv.srv.MatterRead(clusterwire.ClosureControlAttrMainState); got != uint8(clusterwire.ClosureMainStateMoving) {
		t.Errorf("MainState = %v after SECTION reported opening, want Moving", got)
	}

	g.OnSection(0) // any code that is neither opening (2) nor closing (5)
	if got, _ := srv.srv.MatterRead(clusterwire.ClosureControlAttrMainState); got != uint8(clusterwire.ClosureMainStateStopped) {
		t.Errorf("MainState = %v after SECTION reported a stop, want Stopped", got)
	}
}
