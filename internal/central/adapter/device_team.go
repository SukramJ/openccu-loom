// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"fmt"
	"strconv"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// SetChannelTeam assigns a channel to a team channel via the CCU's
// `setTeam` (e.g. smoke-detector teams). An empty teamChannelAddress
// resets the channel to its own default team. Gated to BidCos-RF /
// HmIP-RF; other interfaces answer [backends.ErrUnsupported] before any
// wire call. Multi-CCU safe via the registry scan.
func (a *DeviceAdminDomain) SetChannelTeam(ctx context.Context, deviceAddr string, channelNo int, teamChannelAddress string) error {
	channelAddress := deviceAddr + ":" + strconv.Itoa(channelNo)
	unit, dev, err := a.resolveTeamUnit(deviceAddr)
	if err != nil {
		return err
	}
	if !dev.Interface.SupportsTeams() {
		return fmt.Errorf("set team: interface %s: %w", dev.Interface, backends.ErrUnsupported)
	}
	backend, ok := a.writer.Backend(unit.Name(), hmtypes.ParseWireInterfaceID(dev.InterfaceID))
	if !ok {
		return fmt.Errorf("%w: %s/%s", ErrNoDeviceBackend, unit.Name(), dev.InterfaceID)
	}
	return backend.SetTeam(ctx, channelAddress, teamChannelAddress)
}

// TeamCandidates lists the team channels the given channel may be
// assigned to. The list is filtered to channels sharing the target
// channel's team tag; the currently-assigned team is flagged. Returns an
// empty list (not an error) on interfaces without team support so a
// read stays cheap.
func (a *DeviceAdminDomain) TeamCandidates(ctx context.Context, deviceAddr string, channelNo int) ([]hmapi.TeamCandidate, error) {
	channelAddress := deviceAddr + ":" + strconv.Itoa(channelNo)
	unit, dev, err := a.resolveTeamUnit(deviceAddr)
	if err != nil {
		return nil, err
	}
	if !dev.Interface.SupportsTeams() {
		return []hmapi.TeamCandidate{}, nil
	}
	// The description registry is keyed by the canonical wire id
	// (`<central>-<iface>`) — the callback path and the warm-boot hydration
	// both write under it. Looking the target channel up with the bare
	// interface misses on every named central, which returned an empty
	// candidate list with HTTP 200 and made the team unassignable from the UI.
	target, ok := unit.DescRegistry.Get(hmtypes.ParseWireInterfaceID(dev.InterfaceID), channelAddress)
	if !ok || target.TeamTag == "" {
		return []hmapi.TeamCandidate{}, nil
	}
	backend, ok := a.writer.Backend(unit.Name(), hmtypes.ParseWireInterfaceID(dev.InterfaceID))
	if !ok {
		return nil, fmt.Errorf("%w: %s/%s", ErrNoDeviceBackend, unit.Name(), dev.InterfaceID)
	}
	teams, err := backend.ListTeams(ctx)
	if err != nil {
		return nil, err
	}
	var out []hmapi.TeamCandidate
	for i := range teams {
		t := &teams[i]
		if !t.IsChannel() || t.TeamTag != target.TeamTag {
			continue
		}
		name := t.Address
		if ch := unit.GetChannel(t.Address); ch != nil && ch.Name() != "" {
			name = ch.Name()
		}
		out = append(out, hmapi.TeamCandidate{
			Address: t.Address,
			Name:    name,
			TeamTag: t.TeamTag,
			Current: t.Address == target.Team,
		})
	}
	return out, nil
}

// resolveTeamUnit finds the owning central + device for a device
// address, mirroring the resolution the other channel-scoped admin ops
// use.
func (a *DeviceAdminDomain) resolveTeamUnit(deviceAddr string) (*central.Unit, *device.Device, error) {
	if a.registry == nil || a.writer == nil {
		return nil, nil, ErrNoDeviceBackend
	}
	for _, u := range a.registry.List() {
		dev, ok := u.ModelRegistry.Get(deviceAddr)
		if !ok {
			continue
		}
		return u, dev, nil
	}
	return nil, nil, fmt.Errorf("%w: device %s", ErrNoDeviceBackend, deviceAddr)
}
