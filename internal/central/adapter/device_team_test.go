// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/registry"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// teamRecordingOperations wraps fakeOperations with controllable
// setTeam / listTeams behaviour.
type teamRecordingOperations struct {
	*fakeOperations
	setCalls [][2]string
	teams    []hmproto.DeviceDescription
}

func (f *teamRecordingOperations) SetTeam(_ context.Context, channelAddress, teamChannelAddress string) error {
	f.setCalls = append(f.setCalls, [2]string{channelAddress, teamChannelAddress})
	return nil
}

func (f *teamRecordingOperations) ListTeams(context.Context) ([]hmproto.DeviceDescription, error) {
	return f.teams, nil
}

// teamDomainWith seeds the registries the way the running wiring does: the
// device carries the canonical wire id (`<central>-<iface>`), and the
// description registry plus the value writer are keyed by it. Registering
// under the bare interface instead would collapse the two identifier spaces
// and hide a key mismatch in the code under test.
func teamDomainWith(t *testing.T, iface hmenum.Interface, fake backends.Operations) *DeviceAdminDomain {
	t.Helper()
	unit, reg := newReplaceUnit(t, "ccu-01")
	wireID := WireInterfaceID("ccu-01", iface)
	dev := device.New(device.Config{
		InterfaceID: wireID, Interface: iface, Address: "SD001", Model: "HM-Sec-SD",
	})
	unit.ModelRegistry.Put(dev)
	unit.DeviceRegistry.Put(registry.DeviceEntry{
		Interface: hmtypes.ParseWireInterfaceID(wireID), Address: "SD001", Model: "HM-Sec-SD",
	})
	// The target channel description carries the team tag the candidate
	// filter matches on.
	unit.DescRegistry.Put(hmtypes.ParseWireInterfaceID(wireID), hmproto.DeviceDescription{
		Address: "SD001:1", Parent: "SD001", TeamTag: "SMOKE", Team: "TEAM:1",
	})
	w := client.NewValueWriter()
	w.Register("ccu-01", hmtypes.ParseWireInterfaceID(wireID), fake)
	return NewDeviceAdminDomain(reg, w)
}

func TestSetChannelTeam_EligibleInterfaceCallsBackend(t *testing.T) {
	t.Parallel()
	fake := &teamRecordingOperations{fakeOperations: &fakeOperations{kind: backends.KindCCU}}
	domain := teamDomainWith(t, hmenum.InterfaceBidCosRF, fake)
	if err := domain.SetChannelTeam(context.Background(), "SD001", 1, "TEAM:2"); err != nil {
		t.Fatalf("SetChannelTeam: %v", err)
	}
	if len(fake.setCalls) != 1 || fake.setCalls[0] != [2]string{"SD001:1", "TEAM:2"} {
		t.Fatalf("setCalls=%v, want [[SD001:1 TEAM:2]]", fake.setCalls)
	}
}

func TestSetChannelTeam_ResetSendsEmptyTeam(t *testing.T) {
	t.Parallel()
	fake := &teamRecordingOperations{fakeOperations: &fakeOperations{kind: backends.KindCCU}}
	domain := teamDomainWith(t, hmenum.InterfaceBidCosRF, fake)
	if err := domain.SetChannelTeam(context.Background(), "SD001", 1, ""); err != nil {
		t.Fatalf("SetChannelTeam: %v", err)
	}
	if len(fake.setCalls) != 1 || fake.setCalls[0][1] != "" {
		t.Fatalf("reset must send empty team, got %v", fake.setCalls)
	}
}

func TestSetChannelTeam_IneligibleInterfaceRejectedBeforeWireCall(t *testing.T) {
	t.Parallel()
	fake := &teamRecordingOperations{fakeOperations: &fakeOperations{kind: backends.KindCCU}}
	domain := teamDomainWith(t, hmenum.InterfaceBidCosWired, fake)
	err := domain.SetChannelTeam(context.Background(), "SD001", 1, "TEAM:2")
	if !errors.Is(err, backends.ErrUnsupported) {
		t.Fatalf("expected ErrUnsupported, got %v", err)
	}
	if len(fake.setCalls) != 0 {
		t.Errorf("wire call must not fire for an unsupported interface, got %v", fake.setCalls)
	}
}

func TestSetChannelTeam_UnknownDeviceReturnsErrNoDeviceBackend(t *testing.T) {
	t.Parallel()
	fake := &teamRecordingOperations{fakeOperations: &fakeOperations{kind: backends.KindCCU}}
	domain := teamDomainWith(t, hmenum.InterfaceBidCosRF, fake)
	if err := domain.SetChannelTeam(context.Background(), "UNKNOWN", 1, "TEAM:2"); !errors.Is(err, ErrNoDeviceBackend) {
		t.Fatalf("expected ErrNoDeviceBackend, got %v", err)
	}
}

func TestSetChannelTeam_NilRegistryReturnsErrNoDeviceBackend(t *testing.T) {
	t.Parallel()
	domain := NewDeviceAdminDomain(nil, client.NewValueWriter())
	if err := domain.SetChannelTeam(context.Background(), "SD001", 1, "TEAM:2"); !errors.Is(err, ErrNoDeviceBackend) {
		t.Fatalf("expected ErrNoDeviceBackend, got %v", err)
	}
}

func TestTeamCandidates_FiltersByTagAndMarksCurrent(t *testing.T) {
	t.Parallel()
	fake := &teamRecordingOperations{
		fakeOperations: &fakeOperations{kind: backends.KindCCU},
		teams: []hmproto.DeviceDescription{
			{Address: "TEAM:1", Parent: "TEAM", TeamTag: "SMOKE"},   // match + current
			{Address: "TEAM:2", Parent: "TEAM", TeamTag: "SMOKE"},   // match
			{Address: "OTHER:1", Parent: "OTHER", TeamTag: "OTHER"}, // wrong tag → filtered
			{Address: "DEV", Parent: "", TeamTag: "SMOKE"},          // device row → filtered
		},
	}
	domain := teamDomainWith(t, hmenum.InterfaceBidCosRF, fake)
	got, err := domain.TeamCandidates(context.Background(), "SD001", 1)
	if err != nil {
		t.Fatalf("TeamCandidates: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d candidates, want 2 (same team tag, channels only): %+v", len(got), got)
	}
	var current int
	for _, c := range got {
		if c.Current {
			current++
			if c.Address != "TEAM:1" {
				t.Errorf("current team = %s, want TEAM:1", c.Address)
			}
		}
	}
	if current != 1 {
		t.Errorf("exactly one candidate must be marked current, got %d", current)
	}
}

func TestTeamCandidates_UnsupportedInterfaceReturnsEmpty(t *testing.T) {
	t.Parallel()
	fake := &teamRecordingOperations{fakeOperations: &fakeOperations{kind: backends.KindCCU}}
	domain := teamDomainWith(t, hmenum.InterfaceBidCosWired, fake)
	got, err := domain.TeamCandidates(context.Background(), "SD001", 1)
	if err != nil {
		t.Fatalf("TeamCandidates: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("unsupported interface must return an empty list, got %+v", got)
	}
}
