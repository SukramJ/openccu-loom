// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// ---- fakes ----------------------------------------------------------------

// enrichedDP is a minimal device.ParameterDataPoint that also implements
// additionalInfoProvider. Used for the "returns metadata" and "empty map
// yields nil" test cases.
type enrichedDP struct {
	info map[string]any
}

func (d *enrichedDP) DataPointKey() hmtypes.DataPointKey        { return hmtypes.DataPointKey{} }
func (d *enrichedDP) Parameter() hmenum.Parameter               { return "OPERATING_VOLTAGE" }
func (d *enrichedDP) ParameterData() hmproto.ParameterData      { return hmproto.ParameterData{} }
func (d *enrichedDP) RawValue() (any, bool)                     { return nil, false }
func (d *enrichedDP) ModifiedAt() time.Time                     { return time.Time{} }
func (d *enrichedDP) OnAnyUpdate(fn func(old, next any)) func() { return func() {} }

// AdditionalInformation satisfies additionalInfoProvider.
func (d *enrichedDP) AdditionalInformation() map[string]any { return d.info }

// plainDP is a minimal device.ParameterDataPoint that does NOT implement
// additionalInfoProvider at all.
type plainDP struct{}

func (d *plainDP) DataPointKey() hmtypes.DataPointKey        { return hmtypes.DataPointKey{} }
func (d *plainDP) Parameter() hmenum.Parameter               { return "LEVEL" }
func (d *plainDP) ParameterData() hmproto.ParameterData      { return hmproto.ParameterData{} }
func (d *plainDP) RawValue() (any, bool)                     { return nil, false }
func (d *plainDP) ModifiedAt() time.Time                     { return time.Time{} }
func (d *plainDP) OnAnyUpdate(fn func(old, next any)) func() { return func() {} }

// ---- tests ----------------------------------------------------------------

// TestDpAdditionalInformationReturnsMetadata verifies that a DP implementing
// additionalInfoProvider with a non-empty map returns exactly that map.
func TestDpAdditionalInformationReturnsMetadata(t *testing.T) {
	t.Parallel()
	want := map[string]any{
		"Battery Type": "LR03",
		"Battery Qty":  2,
	}
	dp := &enrichedDP{info: want}
	got := dpAdditionalInformation(dp)
	if got == nil {
		t.Fatal("expected non-nil map, got nil")
	}
	if got["Battery Type"] != "LR03" {
		t.Errorf("Battery Type = %v, want LR03", got["Battery Type"])
	}
	if got["Battery Qty"] != 2 {
		t.Errorf("Battery Qty = %v, want 2", got["Battery Qty"])
	}
}

// TestDpAdditionalInformationEmptyMapYieldsNil verifies that a DP whose
// AdditionalInformation() returns an empty (non-nil) map causes the helper to
// return nil, keeping the per-DP state payload byte-identical to the plain case.
func TestDpAdditionalInformationEmptyMapYieldsNil(t *testing.T) {
	t.Parallel()
	dp := &enrichedDP{info: map[string]any{}}
	got := dpAdditionalInformation(dp)
	if got != nil {
		t.Errorf("empty AdditionalInformation map must yield nil, got %v", got)
	}
}

// TestDpAdditionalInformationNonProviderYieldsNil verifies that a plain
// device.ParameterDataPoint that does not implement additionalInfoProvider
// causes the helper to return nil.
func TestDpAdditionalInformationNonProviderYieldsNil(t *testing.T) {
	t.Parallel()
	dp := &plainDP{}
	got := dpAdditionalInformation(dp)
	if got != nil {
		t.Errorf("DP without additionalInfoProvider must yield nil, got %v", got)
	}
}

// TestDpAdditionalInformationNilDataPoint verifies that passing a nil DP does
// not panic and returns nil.
func TestDpAdditionalInformationNilDataPoint(t *testing.T) {
	t.Parallel()
	got := dpAdditionalInformation(nil)
	if got != nil {
		t.Errorf("nil DP must yield nil, got %v", got)
	}
}
