// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"errors"
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

// fakeLinkParamsetReadWriter implements both LinkParamsetReader and
// LinkParamsetWriter so ApplyLinkProfile tests can inspect the exact
// (channel, peer, values) triple the write reaches.
type fakeLinkParamsetReadWriter struct {
	getValues map[string]any

	putCalled  bool
	putChannel string
	putPeer    string
	putValues  map[string]any
}

func (f *fakeLinkParamsetReadWriter) GetLinkParamset(_ context.Context, _, _ string) (map[string]any, error) {
	return f.getValues, nil
}

func (f *fakeLinkParamsetReadWriter) PutLinkParamset(_ context.Context, channelAddress, peerAddress string, values map[string]any) error {
	f.putCalled = true
	f.putChannel = channelAddress
	f.putPeer = peerAddress
	f.putValues = values
	return nil
}

// TestLinkProfilesAdapter_ApplyLinkProfile_ValueSetAndPair is the bite proof
// for ADR 0069's write path. It reads the expected value set out of the
// real embedded archive (ACTOR_WINDOW/SHUTTER_CONTACT profile id=3 — known
// to carry fixed constraints, loose constraints with a default, and one
// loose constraint without one: SHORT_MULTIEXECUTE), never as hardcoded
// literals, and asserts the write lands with the receiver as the channel
// and the sender as the peer — the exact pair ADR 0069 documents getting
// backwards as the reachable defect.
func TestLinkProfilesAdapter_ApplyLinkProfile_ValueSetAndPair(t *testing.T) {
	t.Parallel()
	const (
		receiverType = "ACTOR_WINDOW"
		senderType   = "SHUTTER_CONTACT"
		profileID    = 3
	)

	store := linkprofile.New()
	profile, found := store.GetProfileByID(receiverType, senderType, profileID)
	if !found {
		t.Fatalf("archive fixture missing: GetProfileByID(%s, %s, %d)", receiverType, senderType, profileID)
	}
	wantValues := profile.ApplyValues()
	if len(wantValues) == 0 {
		t.Fatal("archive fixture has no applyable values; pick another profile")
	}
	if _, present := wantValues["SHORT_MULTIEXECUTE"]; present {
		t.Fatal("test fixture assumption broke: SHORT_MULTIEXECUTE (list, no default) must be absent from ApplyValues")
	}

	c, _ := central.New(central.Config{Name: "ccu-01"})
	reg := central.NewRegistry()
	_ = reg.Register(c)

	dReceiver := device.New(device.Config{Address: "VCU1001", Interface: hmenum.InterfaceHmIPRF, InterfaceID: "HmIP-RF"})
	dReceiver.AddChannel("VCU1001:1", 1, receiverType, hmenum.ParamsetKeyValues)
	c.ModelRegistry.Put(dReceiver)

	dSender := device.New(device.Config{Address: "VCU1002", Interface: hmenum.InterfaceHmIPRF, InterfaceID: "HmIP-RF"})
	dSender.AddChannel("VCU1002:1", 1, senderType, hmenum.ParamsetKeyValues)
	c.ModelRegistry.Put(dSender)

	fake := &fakeLinkParamsetReadWriter{}
	a := NewLinkProfilesAdapter(reg, store, fake)

	written, err := a.ApplyLinkProfile(context.Background(), "VCU1001:1", "VCU1002:1", profileID)
	if err != nil {
		t.Fatalf("ApplyLinkProfile: %v", err)
	}
	if written != len(wantValues) {
		t.Fatalf("ApplyLinkProfile: written=%d, want %d", written, len(wantValues))
	}
	if !fake.putCalled {
		t.Fatal("ApplyLinkProfile: PutLinkParamset was never called")
	}
	if fake.putChannel != "VCU1001:1" {
		t.Fatalf("PutLinkParamset channel = %q, want receiver %q", fake.putChannel, "VCU1001:1")
	}
	if fake.putPeer != "VCU1002:1" {
		t.Fatalf("PutLinkParamset peer = %q, want sender %q", fake.putPeer, "VCU1002:1")
	}
	if len(fake.putValues) != len(wantValues) {
		t.Fatalf("PutLinkParamset values count = %d, want %d: got %v", len(fake.putValues), len(wantValues), fake.putValues)
	}
	for k, want := range wantValues {
		got, ok := fake.putValues[k]
		if !ok || got != want {
			t.Fatalf("PutLinkParamset values[%s] = %v (present=%v), want %v", k, got, ok, want)
		}
	}
}

// TestLinkProfilesAdapter_ApplyLinkProfile_UnknownProfile verifies that an
// unknown profile id reports ErrUnsupported and never reaches the writer.
func TestLinkProfilesAdapter_ApplyLinkProfile_UnknownProfile(t *testing.T) {
	t.Parallel()
	fake := &fakeLinkParamsetReadWriter{}
	a := NewLinkProfilesAdapter(nil, linkprofile.New(), fake)
	_, err := a.ApplyLinkProfile(context.Background(), "VCU0001:1", "VCU0002:1", 99999)
	if !errors.Is(err, linkprofile.ErrUnsupported) {
		t.Fatalf("expected ErrUnsupported, got %v", err)
	}
	if fake.putCalled {
		t.Fatal("PutLinkParamset must not be called for an unknown profile")
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
