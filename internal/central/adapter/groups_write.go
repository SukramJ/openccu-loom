// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/group"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// heatingGroupWriter is the capability a backend exposes for group
// administration. Only the CCU backend implements it (via the HMServer
// jpages proxy, ADR 0055); other backends fail with ErrUnsupported.
type heatingGroupWriter interface {
	heatingGroupLister
	CreateHeatingGroupDraft(ctx context.Context) (int, []backends.HeatingGroupType, error)
	SaveHeatingGroup(ctx context.Context, in backends.HeatingGroupSaveInput) error
	DeleteHeatingGroup(ctx context.Context, groupID int) error
	SuitableHeatingGroupMembers(ctx context.Context, typeID string) (backends.SuitableHeatingGroupMembers, error)
	SetInHeatingGroupMetadata(ctx context.Context, deviceAddress string, inGroup bool) error
	// Per-member "operate only via group" flag (GR04). DeviceRegaID resolves a
	// real member device address to its ReGa id; SetOperateGroupOnly toggles the
	// flag. Both act on member devices, which resolve normally — unlike the
	// group's own virtual device (INT*), which getReGaIDByAddress never resolves.
	DeviceRegaID(ctx context.Context, address string) (string, error)
	SetOperateGroupOnly(ctx context.Context, regaID string, mode bool) error
}

// Poll cadence for the fire-and-poll save path (var so tests can shrink it).
var (
	groupSavePollTimeout  = 60 * time.Second
	groupSavePollInterval = 2 * time.Second
)

// writerFor resolves the group-write capability for a central plus the owning
// unit (used to enrich member candidates from the live device model), or an
// error: ErrUnknownCentral (no such central) / ErrUnsupported (backend can't
// manage groups).
func (a *GroupsDomain) writerFor(centralName string) (heatingGroupWriter, *central.Unit, error) {
	if a.registry == nil || a.writer == nil {
		return nil, nil, hmerr.ErrUnknownCentral
	}
	// A write targets exactly one central. An empty name resolves to the sole
	// registered central (single-CCU convenience); with several it is
	// ambiguous and rejected.
	if centralName == "" {
		units := a.registry.List()
		if len(units) != 1 || units[0] == nil {
			return nil, nil, hmerr.ErrUnknownCentral
		}
		centralName = units[0].Name()
	}
	unit, ok := a.registry.Get(centralName)
	if !ok || unit == nil {
		return nil, nil, hmerr.ErrUnknownCentral
	}
	_, backend, err := primaryBackendOf(unit, a.writer)
	if err != nil {
		return nil, nil, err
	}
	w, ok := backend.(heatingGroupWriter)
	if !ok {
		return nil, nil, backends.ErrUnsupported
	}
	return w, unit, nil
}

// Types lists the group types a new group can be created as. The firmware
// carries them only in the create page, so this issues a `group/create`
// (which allocates a throwaway draft, never persisted) and returns the parsed
// type list.
func (a *GroupsDomain) Types(ctx context.Context, centralName string) ([]group.Type, error) {
	w, _, err := a.writerFor(centralName)
	if err != nil {
		return nil, err
	}
	_, types, err := w.CreateHeatingGroupDraft(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]group.Type, 0, len(types))
	for _, t := range types {
		out = append(out, group.Type{ID: t.ID, LabelKey: t.LabelKey})
	}
	return out, nil
}

// SuitableMembers returns the devices assignable to a group of the given type.
func (a *GroupsDomain) SuitableMembers(ctx context.Context, centralName, typeID string) (group.SuitableMembers, error) {
	w, unit, err := a.writerFor(centralName)
	if err != nil {
		return group.SuitableMembers{}, err
	}
	res, err := w.SuitableHeatingGroupMembers(ctx, typeID)
	if err != nil {
		return group.SuitableMembers{}, err
	}
	return group.SuitableMembers{
		Assignable: mapCandidates(unit, res.Assignable),
		Leftover:   mapCandidates(unit, res.Leftover),
	}, nil
}

// Create makes a new group. It runs the two-step jpages flow (GET create →
// POST save) with the per-member inHeatingGroup preamble, then confirms
// completion by polling the roster for the new group — the save's HTTP
// response is unreliable (it may time out even though the group committed),
// so the roster is the completion signal.
func (a *GroupsDomain) Create(ctx context.Context, centralName string, in group.CreateInput) (group.Group, error) {
	w, _, err := a.writerFor(centralName)
	if err != nil {
		return group.Group{}, err
	}
	before, err := a.rosterIDs(ctx, w)
	if err != nil {
		return group.Group{}, err
	}
	draftID, _, err := w.CreateHeatingGroupDraft(ctx)
	if err != nil {
		return group.Group{}, err
	}
	a.applyMemberPreamble(ctx, w, in.MemberIDs)

	saveErr := w.SaveHeatingGroup(ctx, backends.HeatingGroupSaveInput{
		GroupID:               draftID,
		Name:                  in.Name,
		TypeID:                in.TypeID,
		ForbidSingleOperation: in.ForbidSingleOperation,
		MemberIDs:             in.MemberIDs,
		IsNew:                 true,
	})
	// Fire-and-poll: the group appearing in the roster with a new id and the
	// requested name is the authoritative success signal.
	g, ok, err := a.pollForNewGroup(ctx, w, before, in.Name)
	if err != nil {
		return group.Group{}, err
	}
	if ok {
		a.applyOperateGroupOnly(ctx, w, in.ForbidSingleOperation, in.MemberIDs)
		return g, nil
	}
	if saveErr != nil {
		return group.Group{}, fmt.Errorf("group create: %w", saveErr)
	}
	return group.Group{}, fmt.Errorf("group create: group did not appear in the roster: %w", backends.ErrUnsupported)
}

// Update edits an existing group. The group's type is immutable, so it is
// carried through from the current roster entry.
func (a *GroupsDomain) Update(ctx context.Context, centralName string, groupID int, in group.UpdateInput) error {
	w, _, err := a.writerFor(centralName)
	if err != nil {
		return err
	}
	existing, err := a.findGroup(ctx, w, groupID)
	if err != nil {
		return err
	}
	typeID := in.TypeID
	if typeID == "" {
		typeID = existing.TypeID
	}
	a.applyMemberPreamble(ctx, w, in.MemberIDs)

	saveErr := w.SaveHeatingGroup(ctx, backends.HeatingGroupSaveInput{
		GroupID:               groupID,
		Name:                  in.Name,
		TypeID:                typeID,
		ForbidSingleOperation: in.ForbidSingleOperation,
		MemberIDs:             in.MemberIDs,
		IsNew:                 false,
	})
	// The group already exists; a save timeout means the commit is in flight
	// on the CCU (settle is asynchronous), so it is tolerated as success. A
	// real (non-timeout) error is surfaced.
	if saveErr != nil && !errors.Is(saveErr, context.DeadlineExceeded) {
		return saveErr
	}
	a.applyOperateGroupOnly(ctx, w, in.ForbidSingleOperation, in.MemberIDs)
	return nil
}

// applyOperateGroupOnly runs the GR04 post-save side effect: set each member
// device's "operate only via group" flag from the group's
// forbid_single_operation flag, mirroring the CCU WebUI. It uses the
// operator-supplied value (authoritative) rather than the just-parsed roster,
// which may not reflect the change until the CCU settle completes. Best-effort
// — the group is already created/edited, so a flag failure must not fail the
// operation.
//
// The group's own virtual device (INT<id>) is intentionally NOT renamed here:
// getReGaIDByAddress never resolves virtual-device addresses, and the device
// does not settle into the ReGa model until well after the request returns, so
// a synchronous rename is impossible. Instead SaveHeatingGroup sends the bare
// group name as groupDeviceName, so the CCU labels the virtual device and
// derives its channel names ("<name>:<n>") itself.
func (a *GroupsDomain) applyOperateGroupOnly(ctx context.Context, w heatingGroupWriter, forbidSingle bool, memberIDs []string) {
	seen := make(map[string]bool, len(memberIDs))
	for _, m := range memberIDs {
		dev := deviceOf(m)
		if dev == "" || seen[dev] {
			continue
		}
		seen[dev] = true
		if regaID, err := w.DeviceRegaID(ctx, dev); err == nil && regaID != "" {
			_ = w.SetOperateGroupOnly(ctx, regaID, forbidSingle)
		}
	}
}

// Delete removes a group by id, 404-ing (ErrGroupNotFound) when the roster
// does not carry it.
func (a *GroupsDomain) Delete(ctx context.Context, centralName string, groupID int) error {
	w, _, err := a.writerFor(centralName)
	if err != nil {
		return err
	}
	if _, err := a.findGroup(ctx, w, groupID); err != nil {
		return err
	}
	return w.DeleteHeatingGroup(ctx, groupID)
}

// --- internals --------------------------------------------------------------

func (a *GroupsDomain) applyMemberPreamble(ctx context.Context, w heatingGroupWriter, memberIDs []string) {
	seen := make(map[string]bool, len(memberIDs))
	for _, m := range memberIDs {
		dev := deviceOf(m)
		if dev == "" || seen[dev] {
			continue
		}
		seen[dev] = true
		// Best-effort: mirrors the WebUI's pre-save bookkeeping; a failure
		// here must not block the save.
		_ = w.SetInHeatingGroupMetadata(ctx, dev, true)
	}
}

func (a *GroupsDomain) currentGroups(ctx context.Context, w heatingGroupWriter) ([]group.Group, error) {
	raw, err := w.GetHeatingGroupList(ctx)
	if err != nil {
		return nil, err
	}
	return group.ParseGroupList(raw)
}

func (a *GroupsDomain) rosterIDs(ctx context.Context, w heatingGroupWriter) (map[int]bool, error) {
	groups, err := a.currentGroups(ctx, w)
	if err != nil {
		return nil, err
	}
	ids := make(map[int]bool, len(groups))
	for _, g := range groups {
		ids[g.ID] = true
	}
	return ids, nil
}

func (a *GroupsDomain) findGroup(ctx context.Context, w heatingGroupWriter, groupID int) (group.Group, error) {
	groups, err := a.currentGroups(ctx, w)
	if err != nil {
		return group.Group{}, err
	}
	for _, g := range groups {
		if g.ID == groupID {
			return g, nil
		}
	}
	return group.Group{}, hmerr.ErrGroupNotFound
}

// pollForNewGroup waits for a group with an id absent from `before` and the
// requested name to appear. Returns ok=false when the deadline passes without
// it showing up.
func (a *GroupsDomain) pollForNewGroup(ctx context.Context, w heatingGroupWriter, before map[int]bool, name string) (group.Group, bool, error) {
	deadline := time.Now().Add(groupSavePollTimeout)
	for {
		if groups, err := a.currentGroups(ctx, w); err == nil {
			for _, g := range groups {
				if !before[g.ID] && g.Name == name {
					return g, true, nil
				}
			}
		}
		if time.Now().After(deadline) {
			return group.Group{}, false, nil
		}
		select {
		case <-ctx.Done():
			return group.Group{}, false, ctx.Err()
		case <-time.After(groupSavePollInterval):
		}
	}
}

// deviceOf strips a channel suffix, yielding the parent device address used
// as the Interface.setMetadata objectId.
func deviceOf(memberAddress string) string {
	if i := strings.IndexByte(memberAddress, ':'); i >= 0 {
		return memberAddress[:i]
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
		id.ChannelName = ch.Name
		id.ChannelNo = ch.Number
		id.Rooms = append([]string(nil), ch.Rooms...)
		id.Functions = append([]string(nil), ch.Functions...)
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
	id.DeviceName = dev.Name
	id.DeviceModel = dev.Model
	if av := dev.Availability(); av != nil {
		id.ConfigPending = av.IsConfigPending()
	}
	// A channel often carries no room/function of its own; fall back to the
	// device's assignment so every candidate can still be filtered by room.
	if len(id.Rooms) == 0 {
		id.Rooms = append([]string(nil), dev.Rooms...)
	}
	if len(id.Functions) == 0 {
		id.Functions = append([]string(nil), dev.Functions...)
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
