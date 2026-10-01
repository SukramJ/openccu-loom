// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"strings"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/group"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// portFor resolves the heating-group port of the central a write targets.
// A write targets exactly one central; an empty name resolves to the sole
// registered central (single-CCU convenience), and with several it is
// ambiguous and rejected with [hmerr.ErrUnknownCentral].
func (a *GroupsDomain) portFor(centralName string) (central.HeatingGroups, error) {
	if a.registry == nil {
		return nil, hmerr.ErrUnknownCentral
	}
	if centralName == "" {
		units := a.registry.List()
		if len(units) != 1 || units[0] == nil {
			return nil, hmerr.ErrUnknownCentral
		}
		centralName = units[0].Name()
	}
	unit, ok := a.registry.Get(centralName)
	if !ok || unit == nil {
		return nil, hmerr.ErrUnknownCentral
	}
	return groupsPortOf(unit)
}

// groupsPortOf returns the central's heating-group port. A central that
// has not come up yet has none installed.
func groupsPortOf(unit *central.Unit) (central.HeatingGroups, error) {
	g := unit.SystemServices().Groups
	if g == nil {
		return nil, errNoSystemServices(unit)
	}
	return g, nil
}

// Types lists the group types a new group can be created as.
func (a *GroupsDomain) Types(ctx context.Context, centralName string) ([]group.Type, error) {
	g, err := a.portFor(centralName)
	if err != nil {
		return nil, err
	}
	return g.Types(ctx)
}

// SuitableMembers returns the devices assignable to a group of the given
// type, enriched from the live device model by the port.
func (a *GroupsDomain) SuitableMembers(ctx context.Context, centralName, typeID string) (group.SuitableMembers, error) {
	g, err := a.portFor(centralName)
	if err != nil {
		return group.SuitableMembers{}, err
	}
	return g.SuitableMembers(ctx, typeID)
}

// Create makes a new group and returns it as the system recorded it.
func (a *GroupsDomain) Create(ctx context.Context, centralName string, in group.CreateInput) (group.Group, error) {
	g, err := a.portFor(centralName)
	if err != nil {
		return group.Group{}, err
	}
	return g.Create(ctx, in)
}

// Update edits an existing group; [hmerr.ErrGroupNotFound] when the system
// does not carry it.
func (a *GroupsDomain) Update(ctx context.Context, centralName string, groupID int, in group.UpdateInput) error {
	g, err := a.portFor(centralName)
	if err != nil {
		return err
	}
	return g.Update(ctx, groupID, in)
}

// Delete removes a group by id; [hmerr.ErrGroupNotFound] when the system
// does not carry it.
func (a *GroupsDomain) Delete(ctx context.Context, centralName string, groupID int) error {
	g, err := a.portFor(centralName)
	if err != nil {
		return err
	}
	return g.Delete(ctx, groupID)
}

// deviceOf strips a channel suffix, yielding the parent device address used
// as the Interface.setMetadata objectId.
func deviceOf(memberAddress string) string {
	if before, _, ok := strings.Cut(memberAddress, ":"); ok {
		return before
	}
	return memberAddress
}

func mapCandidates(unit *central.Unit, in []backends.HeatingGroupMember) []group.MemberCandidate {
	out := make([]group.MemberCandidate, 0, len(in))
	for _, m := range in {
		c := group.MemberCandidate{
			Address:       m.ID,
			Serial:        m.SerialNumber,
			Type:          m.Type,
			DeviceAddress: deviceOf(m.ID),
		}
		enrichCandidate(&c, unit)
		out = append(out, c)
	}
	return out
}

// memberIdentity holds the presentation fields resolved for a group-member
// address from the live device model. Shared by the suitable-members candidate
// enrichment and the group-listing member enrichment.
type memberIdentity struct {
	DeviceAddress string
	DeviceName    string
	DeviceModel   string
	ChannelName   string
	ChannelNo     int
	Rooms         []string
	Functions     []string
	ConfigPending bool
}

// resolveMemberIdentity resolves a member address against the unit's device
// model. A channel address ("<device>:<ch>") resolves the channel for its name,
// number, rooms and functions; a bare device address — or a channel not present
// in the model — falls back to the parent device so the device name still
// resolves instead of leaving the caller to render the raw address. Best-effort:
// a member not in the model (or a nil unit) yields empty fields.
func resolveMemberIdentity(unit *central.Unit, address string) memberIdentity {
	id := memberIdentity{DeviceAddress: deviceOf(address)}
	if unit == nil {
		return id
	}
	if ch := unit.GetChannel(address); ch != nil {
		id.ChannelName = ch.Name()
		id.ChannelNo = ch.Number
		id.Rooms = ch.Rooms()
		id.Functions = ch.Functions()
		fillDeviceIdentity(&id, ch.Device())
		return id
	}
	if dev, ok := unit.ModelRegistry.Get(id.DeviceAddress); ok {
		fillDeviceIdentity(&id, dev)
	}
	return id
}

// fillDeviceIdentity copies device-level fields onto id and backfills
// rooms/functions from the device when the channel carried none.
func fillDeviceIdentity(id *memberIdentity, dev *device.Device) {
	if dev == nil {
		return
	}
	id.DeviceAddress = dev.Address
	id.DeviceName = dev.Name()
	id.DeviceModel = dev.Model
	if av := dev.Availability(); av != nil {
		id.ConfigPending = av.IsConfigPending()
	}
	// A channel often carries no room/function of its own; fall back to the
	// device's assignment so every candidate can still be filtered by room.
	if len(id.Rooms) == 0 {
		id.Rooms = dev.Rooms()
	}
	if len(id.Functions) == 0 {
		id.Functions = dev.Functions()
	}
}

// enrichCandidate fills a candidate's identification fields (device/channel
// name, model, channel number, rooms, functions) from the live device model so
// the SPA can group and filter hundreds of candidates instead of rendering a
// flat address list. Best-effort: a member not yet in the model — or a nil unit
// — leaves the enrichment fields empty and the SPA falls back to the address.
func enrichCandidate(c *group.MemberCandidate, unit *central.Unit) {
	id := resolveMemberIdentity(unit, c.Address)
	c.DeviceAddress = id.DeviceAddress
	c.DeviceName = id.DeviceName
	c.DeviceModel = id.DeviceModel
	c.ChannelName = id.ChannelName
	c.ChannelNo = id.ChannelNo
	c.Rooms = id.Rooms
	c.Functions = id.Functions
	c.ConfigPending = id.ConfigPending
}
