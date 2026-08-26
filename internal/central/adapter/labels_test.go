// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/ccudata"
)

// TestParameterLabelAdapter_ChannelTypedValueLabel_KnownValue verifies that a
// matching param=value entry in the translation table is returned.
func TestParameterLabelAdapter_ChannelTypedValueLabel_KnownValue(t *testing.T) {
	t.Parallel()
	tr := ccudata.Empty()
	tr.ParameterValues["de"] = map[string]string{
		"state=true": "Ein",
	}
	a := NewParameterLabelAdapter(tr, "de")
	got := a.ChannelTypedValueLabel("", "STATE", "true")
	if got != "Ein" {
		t.Fatalf("ChannelTypedValueLabel = %q, want %q", got, "Ein")
	}
}

// TestParameterLabelAdapter_ChannelTypedValueLabel_UnknownValue verifies that
// the raw value is returned verbatim when no translation entry exists.
func TestParameterLabelAdapter_ChannelTypedValueLabel_UnknownValue(t *testing.T) {
	t.Parallel()
	tr := ccudata.Empty()
	tr.ParameterValues["de"] = map[string]string{
		"state=true": "Ein",
	}
	a := NewParameterLabelAdapter(tr, "de")
	got := a.ChannelTypedValueLabel("", "STATE", "false")
	if got != "false" {
		t.Fatalf("ChannelTypedValueLabel = %q, want %q", got, "false")
	}
}

// TestParameterLabelAdapter_ChannelTypedValueLabel_NilAdapter verifies that a
// nil receiver returns the raw value rather than panicking.
func TestParameterLabelAdapter_ChannelTypedValueLabel_NilAdapter(t *testing.T) {
	t.Parallel()
	var a *ParameterLabelAdapter
	got := a.ChannelTypedValueLabel("", "STATE", "true")
	if got != "true" {
		t.Fatalf("nil adapter: got %q, want %q", got, "true")
	}
}

// TestParameterLabelAdapter_ChannelTypedValueLabel_ChannelTyped verifies that a
// channel-type-prefixed entry (channeltype|param=value) takes precedence.
func TestParameterLabelAdapter_ChannelTypedValueLabel_ChannelTyped(t *testing.T) {
	t.Parallel()
	tr := ccudata.Empty()
	tr.ParameterValues["de"] = map[string]string{
		"switch_transmitter|state=true": "An",
	}
	a := NewParameterLabelAdapter(tr, "de")
	got := a.ChannelTypedValueLabel("SWITCH_TRANSMITTER", "STATE", "true")
	if got != "An" {
		t.Fatalf("ChannelTypedValueLabel with channel type = %q, want %q", got, "An")
	}
}
