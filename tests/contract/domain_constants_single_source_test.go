// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/alarm/codes"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/internal/model/weekprofile"
)

// TestDomainConstantsAreNamedNotRestated pins three values that a north plane
// used to spell out beside the domain's own.
//
// None of the three is complicated, which is the point: a trivial constant
// restated in a handler survives every review, agrees for years, and then
// disagrees exactly once — when the domain changes and the copy does not. The
// failure is invisible from either side, because each is internally
// consistent.
func TestDomainConstantsAreNamedNotRestated(t *testing.T) {
	t.Parallel()

	// The pairing window a CCU opens when a request omits a duration.
	if hub.DefaultInstallModeDuration != 60*time.Second {
		t.Errorf("default install-mode duration = %v, want 60s", hub.DefaultInstallModeDuration)
	}

	// The climate-profile index range. Two layers reject an index outside it:
	// the REST write path with 422, the schedule adapter by refusing the copy.
	if weekprofile.MinProfileIndex != 1 || weekprofile.MaxProfileIndex != 6 {
		t.Errorf("profile index range = %d..%d, want 1..6",
			weekprofile.MinProfileIndex, weekprofile.MaxProfileIndex)
	}
	for _, c := range []struct {
		n     int
		valid bool
	}{{0, false}, {1, true}, {6, true}, {7, false}, {-1, false}} {
		if got := weekprofile.ValidProfileIndex(c.n); got != c.valid {
			t.Errorf("ValidProfileIndex(%d) = %v, want %v", c.n, got, c.valid)
		}
	}

	// The alarm-code discriminator. A kind the write path accepts but the
	// facade cannot dispatch is stored and never fires.
	kinds := codes.Kinds()
	if len(kinds) != 3 {
		t.Fatalf("codes.Kinds() has %d entries, want 3 — the guard lost its subject", len(kinds))
	}
	seen := map[codes.Kind]bool{}
	for _, k := range kinds {
		seen[k] = true
	}
	for _, want := range []codes.Kind{codes.KindPIN, codes.KindKeypadSlot, codes.KindRemoteKey} {
		if !seen[want] {
			t.Errorf("codes.Kinds() is missing %q", want)
		}
	}
}
