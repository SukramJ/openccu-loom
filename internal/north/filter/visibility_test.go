// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package filter_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/filter"
	"github.com/SukramJ/openccu-loom/internal/store/visibility"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestAdapterNilRegistryAllowsAll verifies that a nil underlying registry
// makes Visible return true for every input (no filter = everything visible).
func TestAdapterNilRegistryAllowsAll(t *testing.T) {
	t.Parallel()
	a := filter.NewAdapter(nil)
	if !a.Visible("HmIP-STH", "TRANSCEIVER", hmenum.ParamsetKeyValues, hmenum.ParameterState) {
		t.Error("nil registry must make Visible return true (no filter)")
	}
}

// TestAdapterNilReceiverAllowsAll verifies that a nil *Adapter receiver also
// returns true so callers do not have to nil-check before calling Visible.
func TestAdapterNilReceiverAllowsAll(t *testing.T) {
	t.Parallel()
	var a *filter.Adapter // nil receiver
	if !a.Visible("any", "any", hmenum.ParamsetKeyValues, hmenum.ParameterLevel) {
		t.Error("nil receiver must make Visible return true")
	}
}

// TestAdapterDelegatesIsAllowedToRegistry verifies that a non-nil registry
// is consulted and that globally-hidden parameters return false while
// visible ones return true.
func TestAdapterDelegatesIsAllowedToRegistry(t *testing.T) {
	t.Parallel()
	reg := visibility.NewRegistry()
	// ON_TIME_LIST_1 is a built-in globally hidden parameter.
	hiddenParam := hmenum.ParameterOnTimeList1
	visibleParam := hmenum.ParameterState

	a := filter.NewAdapter(reg)

	if a.Visible("HmIP-BS2", "SWITCH", hmenum.ParamsetKeyValues, hiddenParam) {
		t.Errorf("globally hidden parameter %q must return false from Visible", hiddenParam)
	}
	if !a.Visible("HmIP-BS2", "SWITCH", hmenum.ParamsetKeyValues, visibleParam) {
		t.Errorf("visible parameter %q must return true from Visible", visibleParam)
	}
}

// TestAdapterVisibleForChannelNilRegistryAllowsAll verifies that a nil
// registry makes VisibleForChannel return true.
func TestAdapterVisibleForChannelNilRegistryAllowsAll(t *testing.T) {
	t.Parallel()
	a := filter.NewAdapter(nil)
	if !a.VisibleForChannel("HmIP-STH", "TRANSCEIVER", 0, hmenum.ParamsetKeyValues, hmenum.ParameterState) {
		t.Error("nil registry must make VisibleForChannel return true (no filter)")
	}
}

// TestAdapterVisibleForChannelNilReceiverAllowsAll verifies that a nil
// *Adapter receiver also returns true from VisibleForChannel.
func TestAdapterVisibleForChannelNilReceiverAllowsAll(t *testing.T) {
	t.Parallel()
	var a *filter.Adapter
	if !a.VisibleForChannel("any", "any", 1, hmenum.ParamsetKeyValues, hmenum.ParameterLevel) {
		t.Error("nil receiver must make VisibleForChannel return true")
	}
}

// TestAdapterVisibleForChannelDelegatesRegistry verifies that VisibleForChannel
// consults the underlying registry with the channel number.
func TestAdapterVisibleForChannelDelegatesRegistry(t *testing.T) {
	t.Parallel()
	reg := visibility.NewRegistry()
	hiddenParam := hmenum.ParameterOnTimeList1
	visibleParam := hmenum.ParameterState
	a := filter.NewAdapter(reg)

	if a.VisibleForChannel("HmIP-BS2", "SWITCH", 1, hmenum.ParamsetKeyValues, hiddenParam) {
		t.Errorf("globally hidden parameter %q must return false from VisibleForChannel", hiddenParam)
	}
	if !a.VisibleForChannel("HmIP-BS2", "SWITCH", 1, hmenum.ParamsetKeyValues, visibleParam) {
		t.Errorf("visible parameter %q must return true from VisibleForChannel", visibleParam)
	}
}

// TestAdapterUnIgnoreExtendsVisibleSet verifies that an UnIgnore entry for a
// globally hidden parameter makes Visible return true for the matching model
// after loading the override.
func TestAdapterUnIgnoreExtendsVisibleSet(t *testing.T) {
	t.Parallel()
	reg := visibility.NewRegistry()
	p := hmenum.ParameterOnTimeList1

	a := filter.NewAdapter(reg)

	// Before un-ignore: hidden.
	if a.Visible("HmIP-BS2", "SWITCH", hmenum.ParamsetKeyValues, p) {
		t.Fatal("parameter must be hidden before un-ignore")
	}

	// Load un-ignore for the specific model.
	reg.Parameter().LoadUnIgnore([]visibility.UnIgnoreEntry{
		{Parameter: p, Model: "HmIP-BS2"},
	})

	// After un-ignore: visible for the specified model.
	if !a.Visible("HmIP-BS2", "SWITCH", hmenum.ParamsetKeyValues, p) {
		t.Error("un-ignored parameter must be visible after LoadUnIgnore")
	}
	// Other models still hidden.
	if a.Visible("HmIP-RGBW", "SWITCH", hmenum.ParamsetKeyValues, p) {
		t.Error("un-ignore must not leak to other models")
	}
}
