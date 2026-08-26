// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// dpWithAdditionalInfo wraps minimalDP and adds the optional
// AdditionalInformation() capability checked inside toDataPointSummary.
type dpWithAdditionalInfo struct {
	minimalDP
	info map[string]any
}

func (d *dpWithAdditionalInfo) AdditionalInformation() map[string]any { return d.info }

// TestDataPointSummaryCarriesAdditionalInformation verifies that a DP whose
// AdditionalInformation() returns a non-empty map has the field populated on
// the summary struct and serialised into JSON under "additional_information".
func TestDataPointSummaryCarriesAdditionalInformation(t *testing.T) {
	t.Parallel()

	want := map[string]any{"Battery Type": "LR03", "Battery Qty": 2}
	dp := &dpWithAdditionalInfo{
		minimalDP: minimalDP{param: hmenum.ParameterState},
		info:      want,
	}

	ch := &device.Channel{Type: "SWITCH"}
	s := toDataPointSummary(dp, nil, ch, "")

	if s.AdditionalInformation == nil {
		t.Fatal("AdditionalInformation must not be nil when the DP returns a non-empty map")
	}
	if bt, ok := s.AdditionalInformation["Battery Type"]; !ok || bt != "LR03" {
		t.Errorf("Battery Type: got %v, want %q", s.AdditionalInformation["Battery Type"], "LR03")
	}
	if bq, ok := s.AdditionalInformation["Battery Qty"]; !ok || bq != 2 {
		t.Errorf("Battery Qty: got %v, want 2", s.AdditionalInformation["Battery Qty"])
	}

	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if !strings.Contains(string(b), `"additional_information"`) {
		t.Errorf("marshaled JSON must contain additional_information; got: %s", b)
	}
}

// TestDataPointSummaryAdditionalInformationOmittedWhenAbsent verifies that a
// plain DP (no AdditionalInformation method) leaves the field nil and the key
// is absent from the marshaled JSON (omitempty guarantee).
func TestDataPointSummaryAdditionalInformationOmittedWhenAbsent(t *testing.T) {
	t.Parallel()

	dp := &minimalDP{param: hmenum.ParameterState}
	ch := &device.Channel{Type: "SWITCH"}
	s := toDataPointSummary(dp, nil, ch, "")

	if s.AdditionalInformation != nil {
		t.Errorf("AdditionalInformation must be nil for a plain DP; got %v", s.AdditionalInformation)
	}

	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if strings.Contains(string(b), "additional_information") {
		t.Errorf("marshaled JSON must not contain additional_information; got: %s", b)
	}
}

// TestDataPointSummaryAdditionalInformationOmittedWhenEmpty verifies that a DP
// whose AdditionalInformation() returns a non-nil but empty map leaves the
// field absent — only a populated map is propagated.
func TestDataPointSummaryAdditionalInformationOmittedWhenEmpty(t *testing.T) {
	t.Parallel()

	dp := &dpWithAdditionalInfo{
		minimalDP: minimalDP{param: hmenum.ParameterState},
		info:      map[string]any{}, // empty map
	}
	ch := &device.Channel{Type: "SWITCH"}
	s := toDataPointSummary(dp, nil, ch, "")

	if s.AdditionalInformation != nil {
		t.Errorf("AdditionalInformation must be nil for an empty map; got %v", s.AdditionalInformation)
	}

	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if strings.Contains(string(b), "additional_information") {
		t.Errorf("marshaled JSON must not contain additional_information for empty map; got: %s", b)
	}
}
