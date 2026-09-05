// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package matteradapter (internal white-box tests for unexported helpers).
package matteradapter

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/store"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// =============================================================================
// SetExposureChecker
// =============================================================================

// denyAllChecker is an ExposureChecker that rejects everything.
type denyAllChecker struct{}

func (denyAllChecker) IsExposed(_ context.Context, _ store.EndpointKey) (bool, error) {
	return false, nil
}

// TestSetExposureChecker_Nil verifies that passing nil resets to allow-all.
func TestSetExposureChecker_Nil(t *testing.T) {
	t.Parallel()
	a := &Assembler{exposures: allowAllExposureChecker{}}
	// Setting nil must revert to allow-all without panic.
	a.SetExposureChecker(nil)
	_, ok := a.exposures.(allowAllExposureChecker)
	if !ok {
		t.Error("exposures should be allowAllExposureChecker after SetExposureChecker(nil)")
	}
}

// TestSetExposureChecker_NonNil verifies that a non-nil checker is stored.
func TestSetExposureChecker_NonNil(t *testing.T) {
	t.Parallel()
	a := &Assembler{exposures: allowAllExposureChecker{}}
	a.SetExposureChecker(denyAllChecker{})
	if _, ok := a.exposures.(denyAllChecker); !ok {
		t.Error("exposures should be denyAllChecker after SetExposureChecker")
	}
}

// =============================================================================
// genericDPKeyForMeasurement
// =============================================================================

// dpKeyDP implements DataPointKey() for genericDPKeyForMeasurement tests.
type dpKeyDP struct {
	param string
}

func (d *dpKeyDP) DataPointKey() hmtypes.DataPointKey {
	return hmtypes.DataPointKey{Parameter: d.param}
}

// TestGenericDPKeyForMeasurement_HasKey verifies that a DP with a non-empty
// Parameter is returned verbatim.
func TestGenericDPKeyForMeasurement_HasKey(t *testing.T) {
	t.Parallel()
	dp := &dpKeyDP{param: "TEMPERATURE"}
	got := genericDPKeyForMeasurement(dp)
	if got != "TEMPERATURE" {
		t.Errorf("got %q, want %q", got, "TEMPERATURE")
	}
}

// TestGenericDPKeyForMeasurement_Empty verifies that a DP with empty Parameter
// returns an empty string.
func TestGenericDPKeyForMeasurement_Empty(t *testing.T) {
	t.Parallel()
	dp := &dpKeyDP{param: ""}
	got := genericDPKeyForMeasurement(dp)
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

// TestGenericDPKeyForMeasurement_NoInterface verifies that a value without
// the DataPointKey interface returns an empty string.
func TestGenericDPKeyForMeasurement_NoInterface(t *testing.T) {
	t.Parallel()
	got := genericDPKeyForMeasurement("not-a-dp")
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}
