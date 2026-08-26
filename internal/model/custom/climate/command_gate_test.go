// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package climate

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ─── Climate.SetMode gate ────────────────────────────────────────────────────

// TestSetModeSkipsWhenModeUnchanged verifies that SetMode returns nil without
// issuing a wire write when the current mode already equals the requested mode.
func TestSetModeSkipsWhenModeUnchanged(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	r := newRig(t, "x", KindIP, w, custom.ClimateCapabilities{MinTemperature: 4.5, MaxTemperature: 30.5})
	r.climate.OnMode(ModeAuto)

	before := len(w.calls)
	if err := r.climate.SetMode(context.Background(), ModeAuto, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetMode returned unexpected error: %v", err)
	}
	if len(w.calls) != before {
		t.Errorf("SetMode wrote %d time(s) when mode was already AUTO; want 0 writes", len(w.calls)-before)
	}
}

// TestSetModePassesWhenModeChanges verifies that SetMode issues a wire write
// when the requested mode differs from the current mode.
func TestSetModePassesWhenModeChanges(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	r := newRig(t, "x", KindIP, w, custom.ClimateCapabilities{MinTemperature: 4.5, MaxTemperature: 30.5})
	r.climate.OnMode(ModeAuto)

	before := len(w.calls)
	if err := r.climate.SetMode(context.Background(), ModeHeat, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetMode returned unexpected error: %v", err)
	}
	if len(w.calls) == before {
		t.Error("SetMode issued no write when mode changed from AUTO to HEAT; want at least 1 write")
	}
}

// TestSetModePassesWhenUnobserved verifies that SetMode always writes when no
// mode has been received yet (first command must go through).
func TestSetModePassesWhenUnobserved(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	r := newRig(t, "x", KindIP, w, custom.ClimateCapabilities{MinTemperature: 4.5, MaxTemperature: 30.5})

	before := len(w.calls)
	if err := r.climate.SetMode(context.Background(), ModeAuto, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetMode returned unexpected error: %v", err)
	}
	if len(w.calls) == before {
		t.Error("SetMode issued no write when state was unobserved; want at least 1 write")
	}
}

// ─── Climate.SetProfile gate ─────────────────────────────────────────────────

// TestSetProfileSkipsWhenProfileUnchanged verifies that SetProfile returns nil
// without a wire write when the current profile matches the requested one.
func TestSetProfileSkipsWhenProfileUnchanged(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	r := newRig(t, "x", KindIP, w, custom.ClimateCapabilities{SupportsProfile: true})
	r.climate.OnProfile(ProfileWeekProgram1)

	before := len(w.calls)
	if err := r.climate.SetProfile(context.Background(), ProfileWeekProgram1, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetProfile returned unexpected error: %v", err)
	}
	if len(w.calls) != before {
		t.Errorf("SetProfile wrote %d time(s) when profile was already P1; want 0 writes", len(w.calls)-before)
	}
}

// TestSetProfilePassesWhenProfileChanges verifies that SetProfile issues a
// wire write when the requested profile differs from the current profile.
func TestSetProfilePassesWhenProfileChanges(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	r := newRig(t, "x", KindIP, w, custom.ClimateCapabilities{SupportsProfile: true})
	r.climate.OnProfile(ProfileWeekProgram1)

	before := len(w.calls)
	if err := r.climate.SetProfile(context.Background(), ProfileWeekProgram2, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetProfile returned unexpected error: %v", err)
	}
	if len(w.calls) == before {
		t.Error("SetProfile issued no write when profile changed P1→P2; want at least 1 write")
	}
}

// ─── partyModeCode format ─────────────────────────────────────────────────────

// TestPartyModeCodeFormatCSV verifies the PARTY_MODE_SUBMIT format matches the
// CSV reference: temp,start_mod,dd,mm,yy,end_mod,dd,mm,yy
//
// Both ends are built in time.Local because the payload is a bare wall
// clock the CCU reads in its own zone — see TestPartyModeCodeRendersEndInLocalWallClock.
func TestPartyModeCodeFormatCSV(t *testing.T) {
	t.Parallel()

	start := time.Date(2016, 10, 20, 20, 0, 0, 0, time.Local)
	end := time.Date(2016, 10, 20, 23, 0, 0, 0, time.Local)
	got := partyModeCode(start, end, 21.5)
	// 20*60+0 = 1200, 23*60+0 = 1380
	// Expected CSV: "21.5,1200,20,10,16,1380,20,10,16"
	want := "21.5,1200,20,10,16,1380,20,10,16"
	if got != want {
		t.Errorf("partyModeCode = %q, want %q", got, want)
	}
}

// TestPartyModeCodeMidnightCrossing verifies handling of non-round times.
func TestPartyModeCodeMidnightCrossing(t *testing.T) {
	t.Parallel()

	start := time.Date(2016, 1, 5, 8, 30, 0, 0, time.Local)
	end := time.Date(2016, 1, 6, 7, 15, 0, 0, time.Local)
	got := partyModeCode(start, end, 18.0)
	// 8*60+30 = 510, 7*60+15 = 435
	want := "18.0,510,05,01,16,435,06,01,16"
	if got != want {
		t.Errorf("partyModeCode = %q, want %q", got, want)
	}
}

// foreignZone returns a location whose offset differs from the machine's
// by one hour, so a timestamp built in it always has a different wall
// clock than the same instant has locally — whatever zone the test host
// happens to run in.
func foreignZone(t *testing.T) *time.Location {
	t.Helper()
	_, offset := time.Now().Zone()

	return time.FixedZone("TEST", offset+3600)
}

// TestEncodePartyTimeRendersForeignZoneAsLocalWallClock pins the zone
// contract of PARTY_TIME_START/END: the CCU reads both as its own local
// wall clock, so an `until` that arrived as an RFC-3339 timestamp with a
// trailing Z (the canonical form every JSON client emits) must be
// converted, not rendered in the offset it carries. Rendering it verbatim
// put the two ends of one window in two different zones.
func TestEncodePartyTimeRendersForeignZoneAsLocalWallClock(t *testing.T) {
	t.Parallel()

	instant := time.Date(2026, 8, 15, 21, 0, 0, 0, foreignZone(t))
	want := instant.Local().Format("2006_01_02 15:04")
	if got := encodePartyTime(instant); got != want {
		t.Errorf("encodePartyTime(foreign-zone) = %q, want the local wall clock %q", got, want)
	}
}

// TestPartyModeCodeRendersEndInLocalWallClock is the RF half of the same
// contract: PARTY_MODE_SUBMIT carries minute-of-day plus dd,mm,yy fields
// with no zone at all, and the caller's `end` keeps the offset of the
// string it was parsed from. Encoding start locally and end in a foreign
// zone produced a window that ends before it starts once the offset
// exceeds the requested duration, which the CCU drops without an error.
func TestPartyModeCodeRendersEndInLocalWallClock(t *testing.T) {
	t.Parallel()

	zone := foreignZone(t)
	start := time.Date(2026, 8, 15, 20, 0, 0, 0, time.Local)
	end := time.Date(2026, 8, 15, 23, 0, 0, 0, zone)
	local := end.Local()
	want := fmt.Sprintf(
		"21.0,%d,%s,%d,%s",
		start.Hour()*60+start.Minute(),
		start.Format("02,01,06"),
		local.Hour()*60+local.Minute(),
		local.Format("02,01,06"),
	)
	if got := partyModeCode(start, end, 21.0); got != want {
		t.Errorf("partyModeCode(foreign-zone end) = %q, want %q", got, want)
	}
}
