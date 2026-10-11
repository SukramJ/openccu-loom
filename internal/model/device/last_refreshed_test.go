// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package device

import (
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestLastRefreshedTracksObservationsNotChanges pins the difference between
// the two device-level aggregates: LastUpdated follows value changes,
// LastRefreshed follows observations. A repeated identical reading moves
// the latter and leaves the former alone, and both take the latest stamp
// across every channel.
func TestLastRefreshedTracksObservationsNotChanges(t *testing.T) {
	d := newTestDevice(t)
	if !d.LastRefreshed().IsZero() || !d.LastUpdated().IsZero() {
		t.Fatal("fresh device must report zero timestamps")
	}

	lowbat := newBoolDP("0001ABCD:0", hmenum.ParameterLowBat)
	d.Channel("0001ABCD:0").Put(lowbat)
	temp := newFloatDP("0001ABCD:1", hmenum.ParameterActualTemperature)
	d.Channel("0001ABCD:1").Put(temp)

	t1 := time.Date(2030, 3, 1, 8, 0, 0, 0, time.UTC)
	t2 := t1.Add(5 * time.Minute)
	t3 := t2.Add(5 * time.Minute)
	lowbat.OnEventAt(false, t1)
	temp.OnEventAt(21.5, t2)
	temp.OnEventAt(21.5, t3) // sweep re-read, value unchanged

	if got := d.LastUpdated(); !got.Equal(t2) {
		t.Fatalf("LastUpdated = %v, want %v (last value change)", got, t2)
	}
	if got := d.LastRefreshed(); !got.Equal(t3) {
		t.Fatalf("LastRefreshed = %v, want %v (last observation)", got, t3)
	}
	info := d.AvailabilityInfo()
	if !info.LastRefreshed.Equal(t3) || !info.LastUpdated.Equal(t2) {
		t.Fatalf("AvailabilityInfo = LastUpdated %v / LastRefreshed %v, want %v / %v",
			info.LastUpdated, info.LastRefreshed, t2, t3)
	}
}
