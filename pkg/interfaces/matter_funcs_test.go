// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package interfaces_test

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// ---------------------------------------------------------------------------
// MatterEligibilityState.String
// ---------------------------------------------------------------------------

func TestMatterEligibilityStateString(t *testing.T) {
	t.Parallel()
	cases := map[interfaces.MatterEligibilityState]string{
		interfaces.MatterEligibilityMappable:   "mappable",
		interfaces.MatterEligibilityPartial:    "partially_mappable",
		interfaces.MatterEligibilityUnmappable: "unmappable",
		interfaces.MatterEligibilityState(99):  "unknown",
	}
	for s, want := range cases {
		if got := s.String(); got != want {
			t.Errorf("MatterEligibilityState(%d).String() = %q, want %q", s, got, want)
		}
	}
}

// ---------------------------------------------------------------------------
// MatterMeasurementClassDeviceType
// ---------------------------------------------------------------------------

func TestMatterMeasurementClassDeviceType(t *testing.T) {
	t.Parallel()
	cases := []struct {
		class interfaces.MatterMeasurementClass
		want  uint16
	}{
		{interfaces.MatterMeasurementTemperature, 0x0302},
		{interfaces.MatterMeasurementHumidity, 0x0307},
		{interfaces.MatterMeasurementIlluminance, 0x0106},
		{interfaces.MatterMeasurementPressure, 0x0305},
		{interfaces.MatterMeasurementCO2, 0x002C},
		{interfaces.MatterMeasurementPM25, 0x002C},
		{interfaces.MatterMeasurementPM10, 0x002C},
		{interfaces.MatterMeasurementOccupancy, 0x0107},
		{interfaces.MatterMeasurementContact, 0x0015},
		{interfaces.MatterMeasurementLeak, 0x0043},
		{interfaces.MatterMeasurementMomentarySwitch, 0x000F},
		// Battery / Power / Energy → 0 (no standalone device type).
		{interfaces.MatterMeasurementBattery, 0},
		{interfaces.MatterMeasurementPower, 0},
		{interfaces.MatterMeasurementEnergy, 0},
		// None / unknown → 0.
		{interfaces.MatterMeasurementNone, 0},
	}
	for _, tc := range cases {
		got := interfaces.MatterMeasurementClassDeviceType(tc.class)
		if got != tc.want {
			t.Errorf("MatterMeasurementClassDeviceType(%v) = 0x%04X, want 0x%04X",
				tc.class, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// MatterMeasurementClassClusterID
// ---------------------------------------------------------------------------

func TestMatterMeasurementClassClusterID(t *testing.T) {
	t.Parallel()
	cases := []struct {
		class interfaces.MatterMeasurementClass
		want  uint32
	}{
		{interfaces.MatterMeasurementTemperature, 0x0402},
		{interfaces.MatterMeasurementHumidity, 0x0405},
		{interfaces.MatterMeasurementIlluminance, 0x0400},
		{interfaces.MatterMeasurementPressure, 0x0403},
		{interfaces.MatterMeasurementCO2, 0x040D},
		{interfaces.MatterMeasurementPM25, 0x042A},
		{interfaces.MatterMeasurementPM10, 0x042D},
		{interfaces.MatterMeasurementOccupancy, 0x0406},
		{interfaces.MatterMeasurementContact, 0x0045},
		{interfaces.MatterMeasurementLeak, 0x0045},
		{interfaces.MatterMeasurementBattery, 0x002F},
		{interfaces.MatterMeasurementPower, 0x0090},
		{interfaces.MatterMeasurementEnergy, 0x0091},
		{interfaces.MatterMeasurementMomentarySwitch, 0x003B},
		// None / unknown → 0.
		{interfaces.MatterMeasurementNone, 0},
	}
	for _, tc := range cases {
		got := interfaces.MatterMeasurementClassClusterID(tc.class)
		if got != tc.want {
			t.Errorf("MatterMeasurementClassClusterID(%v) = 0x%04X, want 0x%04X",
				tc.class, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// NoopObserver — OnRequestStart / OnRequestEnd (currently 0 %)
// ---------------------------------------------------------------------------

func TestNoopObserver(t *testing.T) {
	t.Parallel()
	var obs interfaces.NoopObserver

	span := obs.OnRequestStart(context.Background(), interfaces.RequestInfo{
		Protocol:  "xml-rpc",
		Method:    "getParamset",
		Host:      "ccu:2001",
		Interface: "HmIP-RF",
	})
	// span may be nil — just must not panic.

	obs.OnRequestEnd(span, interfaces.RequestResult{
		Duration: 5 * time.Millisecond,
		Err:      nil,
	})
}
