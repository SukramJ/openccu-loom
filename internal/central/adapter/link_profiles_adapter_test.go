// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/store/linkprofile"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestLinkProfilesAdapter_NilStore verifies that GetLinkProfiles returns nil
// and no error when the store is nil.
func TestLinkProfilesAdapter_NilStore(t *testing.T) {
	t.Parallel()
	a := NewLinkProfilesAdapter(nil, nil, nil)
	profs, activeID, err := a.GetLinkProfiles(context.Background(), "VCU0001:1", "VCU0002:1", "en")
	if err != nil {
		t.Fatalf("GetLinkProfiles: unexpected error: %v", err)
	}
	if profs != nil {
		t.Fatalf("expected nil profiles, got %v", profs)
	}
	if activeID != 0 {
		t.Fatalf("expected active id 0, got %d", activeID)
	}
}

// TestLinkProfilesAdapter_EmptyStore verifies that GetLinkProfiles returns nil
// when the store has no profiles registered for the given pair.
func TestLinkProfilesAdapter_EmptyStore(t *testing.T) {
	t.Parallel()
	a := NewLinkProfilesAdapter(nil, linkprofile.New(), nil)
	profs, _, err := a.GetLinkProfiles(context.Background(), "VCU0001:1", "VCU0002:1", "en")
	if err != nil {
		t.Fatalf("GetLinkProfiles: unexpected error: %v", err)
	}
	if profs != nil {
		t.Fatalf("expected nil for empty store, got %v", profs)
	}
}

// TestLinkProfilesAdapter_ReturnsProfilesAsMapSlice verifies that the adapter
// serialises []linkprofile.Profile into []map[string]any correctly.
func TestLinkProfilesAdapter_ReturnsProfilesAsMapSlice(t *testing.T) {
	t.Parallel()
	const (
		receiverType = "KEY_TRANSCEIVER"
		senderType   = "MOTION_DETECTOR"
	)

	v1 := float64(1.0)
	v2 := float64(2.0)
	store := linkprofile.New()
	store.Register(receiverType, senderType, []linkprofile.Profile{
		{
			ID:   1,
			Name: map[string]string{"en": "Standard", "de": "Standard"},
			Params: map[string]linkprofile.ParamConstraint{
				"SHORT_ON_TIME": {ConstraintType: "fixed", Value: &v1},
			},
		},
		{
			ID:   2,
			Name: map[string]string{"en": "Night"},
			Params: map[string]linkprofile.ParamConstraint{
				"SHORT_ON_TIME": {ConstraintType: "fixed", Value: &v2},
			},
		},
	})

	// Build a registry where both channels are known with their types.
	c, _ := central.New(central.Config{Name: "ccu-01"})
	reg := central.NewRegistry()
	_ = reg.Register(c)

	// Receiver device: VCU0001:1 → KEY_TRANSCEIVER
	dReceiver := device.New(device.Config{
		Address: "VCU0001", Interface: hmenum.InterfaceHmIPRF, InterfaceID: "HmIP-RF",
	})
	dReceiver.AddChannel("VCU0001:1", 1, receiverType, hmenum.ParamsetKeyValues)
	c.ModelRegistry.Put(dReceiver)

	// Sender device: VCU0002:1 → MOTION_DETECTOR
	dSender := device.New(device.Config{
		Address: "VCU0002", Interface: hmenum.InterfaceHmIPRF, InterfaceID: "HmIP-RF",
	})
	dSender.AddChannel("VCU0002:1", 1, senderType, hmenum.ParamsetKeyValues)
	c.ModelRegistry.Put(dSender)

	a := NewLinkProfilesAdapter(reg, store, nil)
	profs, _, err := a.GetLinkProfiles(context.Background(), "VCU0001:1", "VCU0002:1", "en")
	if err != nil {
		t.Fatalf("GetLinkProfiles: %v", err)
	}
	if len(profs) != 2 {
		t.Fatalf("expected 2 profiles, got %d: %v", len(profs), profs)
	}
	// Each profile must have an "id" key from the JSON serialisation.
	for i, p := range profs {
		if _, ok := p["id"]; !ok {
			t.Errorf("profile[%d] missing 'id' key", i)
		}
	}
}

// TestLinkProfilesAdapter_TestLinkProfile_Unsupported verifies that
// TestLinkProfile returns a non-error map with unsupported=true when the
// store's stub returns ErrUnsupported.
func TestLinkProfilesAdapter_TestLinkProfile_Unsupported(t *testing.T) {
	t.Parallel()
	a := NewLinkProfilesAdapter(nil, linkprofile.New(), nil)
	result, err := a.TestLinkProfile(context.Background(), "HmIP-RF", "VCU0001:1", "VCU0002:1", 1)
	if err != nil {
		t.Fatalf("TestLinkProfile: unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("TestLinkProfile: expected non-nil result map")
	}
	unsupported, ok := result["unsupported"]
	if !ok {
		t.Fatal("TestLinkProfile: result missing 'unsupported' key")
	}
	if unsupported != true {
		t.Fatalf("TestLinkProfile: unsupported = %v; want true", unsupported)
	}
}

// fakeLinkParamsetReader returns a fixed values map regardless of the
// channel/peer addresses requested, so the test controls exactly what
// activeProfileID sees without touching a real transport.
type fakeLinkParamsetReader struct {
	values map[string]any
}

func (f *fakeLinkParamsetReader) GetLinkParamset(_ context.Context, _, _ string) (map[string]any, error) {
	return f.values, nil
}

// TestLinkProfilesAdapter_ActiveProfileID_DerivedFromLinkParamset drives the
// active-profile derivation through the real composition path: a real
// linkprofile.New() archive store, a real central.Registry with real
// devices/channels, and NewLinkProfilesAdapter — never seeding the active id
// directly. The expected id and the fixture values are both read from the
// archive via GetProfileByID, never hard-coded, so this fails if the
// archive's profile 5 for this pair ever changes shape.
func TestLinkProfilesAdapter_ActiveProfileID_DerivedFromLinkParamset(t *testing.T) {
	t.Parallel()
	const (
		receiverType = "ACOUSTIC_SIGNAL_VIRTUAL_RECEIVER"
		senderType   = "KEY_TRANSCEIVER"
		profileID    = 5
	)

	store := linkprofile.New()
	p, ok := store.GetProfileByID(receiverType, senderType, profileID)
	if !ok {
		t.Fatalf("GetProfileByID(%s, %s, %d): not found in embedded archive", receiverType, senderType, profileID)
	}
	fixed := p.FixedParams()
	if len(fixed) == 0 {
		t.Fatalf("profile %d has no fixed params to derive a match from", p.ID)
	}

	buildAdapter := func(reader LinkParamsetReader) *LinkProfilesAdapter {
		c, err := central.New(central.Config{Name: "ccu-link-active"})
		if err != nil {
			t.Fatalf("central.New: %v", err)
		}
		reg := central.NewRegistry()
		_ = reg.Register(c)

		dReceiver := device.New(device.Config{
			Address: "VCU1001", Interface: hmenum.InterfaceHmIPRF, InterfaceID: "HmIP-RF",
		})
		dReceiver.AddChannel("VCU1001:1", 1, receiverType, hmenum.ParamsetKeyValues)
		c.ModelRegistry.Put(dReceiver)

		dSender := device.New(device.Config{
			Address: "VCU1002", Interface: hmenum.InterfaceHmIPRF, InterfaceID: "HmIP-RF",
		})
		dSender.AddChannel("VCU1002:1", 1, senderType, hmenum.ParamsetKeyValues)
		c.ModelRegistry.Put(dSender)

		return NewLinkProfilesAdapter(reg, store, reader)
	}

	// Arm 1: reader returns the profile's own fixed values → the store
	// must match them back to the same profile id.
	values := make(map[string]any, len(fixed))
	for k, v := range fixed {
		values[k] = v
	}
	a := buildAdapter(&fakeLinkParamsetReader{values: values})
	_, activeID, err := a.GetLinkProfiles(context.Background(), "VCU1001:1", "VCU1002:1", "en")
	if err != nil {
		t.Fatalf("GetLinkProfiles: %v", err)
	}
	if activeID != p.ID {
		t.Fatalf("active id = %d, want %d (from GetProfileByID)", activeID, p.ID)
	}

	// Arm 2: reader returns values matching no profile's constraint set.
	noMatch := map[string]any{}
	for k := range fixed {
		noMatch[k] = float64(999)
	}
	a = buildAdapter(&fakeLinkParamsetReader{values: noMatch})
	_, activeID, err = a.GetLinkProfiles(context.Background(), "VCU1001:1", "VCU1002:1", "en")
	if err != nil {
		t.Fatalf("GetLinkProfiles: %v", err)
	}
	if activeID != 0 {
		t.Fatalf("active id for non-matching values = %d, want 0", activeID)
	}

	// Arm 3: nil reader is the documented nil-tolerant path — no values
	// can be read, so the active id must be reported as none.
	a = buildAdapter(nil)
	_, activeID, err = a.GetLinkProfiles(context.Background(), "VCU1001:1", "VCU1002:1", "en")
	if err != nil {
		t.Fatalf("GetLinkProfiles: %v", err)
	}
	if activeID != 0 {
		t.Fatalf("active id with nil reader = %d, want 0", activeID)
	}
}

// ============================================================
// LinkProfilesAdapter.resolveChannelType
// ============================================================

func TestResolveChannelTypeNilRegistry(t *testing.T) {
	t.Parallel()
	a := NewLinkProfilesAdapter(nil, nil, nil)
	got := a.resolveChannelType("DEV001:1")
	if got != "" {
		t.Errorf("nil registry = %q, want empty", got)
	}
}

func TestResolveChannelTypeEmptyAddr(t *testing.T) {
	t.Parallel()
	reg := central.NewRegistry()
	a := NewLinkProfilesAdapter(reg, nil, nil)
	got := a.resolveChannelType("")
	if got != "" {
		t.Errorf("empty addr = %q, want empty", got)
	}
}

func TestResolveChannelTypeDeviceNotFound(t *testing.T) {
	t.Parallel()
	reg := central.NewRegistry()
	a := NewLinkProfilesAdapter(reg, nil, nil)
	got := a.resolveChannelType("NOSUCHDEV:1")
	if got != "" {
		t.Errorf("device not found = %q, want empty", got)
	}
}

func TestResolveChannelTypeBareDeviceAddr(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-lp"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	_ = reg.Register(c)
	dev := device.New(device.Config{Address: "LPDEV001", InterfaceID: "HmIP-RF", Model: "HmIP-PS"})
	c.ModelRegistry.Put(dev)

	a := NewLinkProfilesAdapter(reg, nil, nil)
	// Bare device address (no colon) → returns device.Model
	got := a.resolveChannelType("LPDEV001")
	if got != "HmIP-PS" {
		t.Errorf("bare addr = %q, want HmIP-PS", got)
	}
}

func TestResolveChannelTypeChannelFound(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-lp2"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	_ = reg.Register(c)
	dev := device.New(device.Config{Address: "LPDEV002", InterfaceID: "HmIP-RF", Model: "HmIP-PS"})
	dev.AddChannel("LPDEV002:1", 1, "SWITCH_VIRTUAL_RECEIVER", hmenum.ParamsetKeyValues)
	c.ModelRegistry.Put(dev)

	a := NewLinkProfilesAdapter(reg, nil, nil)
	got := a.resolveChannelType("LPDEV002:1")
	if got != "SWITCH_VIRTUAL_RECEIVER" {
		t.Errorf("channel found = %q, want SWITCH_VIRTUAL_RECEIVER", got)
	}
}

func TestResolveChannelTypeChannelNotFound(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-lp3"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	_ = reg.Register(c)
	dev := device.New(device.Config{Address: "LPDEV003", InterfaceID: "HmIP-RF", Model: "HmIP-PS"})
	// No channel added
	c.ModelRegistry.Put(dev)

	a := NewLinkProfilesAdapter(reg, nil, nil)
	// Channel suffix :9 doesn't exist → returns ""
	got := a.resolveChannelType("LPDEV003:9")
	if got != "" {
		t.Errorf("channel not found = %q, want empty", got)
	}
}

// ============================================================
// DevicePipeline.suppressUndefinedGenericDataPoints
// ============================================================

func TestSuppressUndefinedGenericDataPointsNilCentral(t *testing.T) {
	t.Parallel()
	p := NewDevicePipeline(nil)
	// nil central → early return, no panic
	p.suppressUndefinedGenericDataPoints("HmIP-RF")
}

func TestSuppressUndefinedGenericDataPointsDeviceOnMatchingInterface(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-suppress"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	dev := device.New(device.Config{Address: "SDEV001", InterfaceID: "HmIP-RF", Model: "HmIP-STH"})
	c.ModelRegistry.Put(dev)

	p := NewDevicePipeline(c)
	// Device is on HmIP-RF → processed, must not panic
	p.suppressUndefinedGenericDataPoints("HmIP-RF")
}

func TestSuppressUndefinedGenericDataPointsDeviceOnWrongInterface(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-suppress2"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	dev := device.New(device.Config{Address: "SDEV002", InterfaceID: "BidCos-RF", Model: "HM-CC-RT-DN"})
	c.ModelRegistry.Put(dev)

	p := NewDevicePipeline(c)
	// Device is on BidCos-RF, calling with HmIP-RF → skipped, no panic
	p.suppressUndefinedGenericDataPoints("HmIP-RF")
}
