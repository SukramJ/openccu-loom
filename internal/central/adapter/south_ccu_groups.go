// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/model/group"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// heatingGroupLister is the narrow capability a backend exposes when it
// can read its CCU's heating-group roster. Only the CCU backend
// implements it (it reads /etc/config/groups.gson via the
// CCU.getHeatingGroupList JSON-RPC method); CUxD / Homegear backends do
// not, so a request routed to one surfaces as unsupported.
type heatingGroupLister interface {
	GetHeatingGroupList(ctx context.Context) (string, error)
}

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

// ccuHeatingGroups is a CCU central's heating-group port: reads through
// the CCU.getHeatingGroupList JSON-RPC method, writes through the HMServer
// jpages proxy (docs/adr/0055-groups-jpages-proxy.md). Every call resolves
// the central's primary backend at call time and narrows it to the
// capability; a backend without it (CUxD, Homegear) answers
// [backends.ErrUnsupported].
type ccuHeatingGroups struct {
	unit   *central.Unit
	writer *client.ValueWriter
}

func (g *ccuHeatingGroups) groupWriter() (heatingGroupWriter, error) {
	_, backend, err := primaryBackendOf(g.unit, g.writer)
	if err != nil {
		return nil, err
	}
	w, ok := backend.(heatingGroupWriter)
	if !ok {
		return nil, backends.ErrUnsupported
	}
	return w, nil
}

// List implements [central.HeatingGroups].
func (g *ccuHeatingGroups) List(ctx context.Context) ([]group.Group, error) {
	_, backend, err := primaryBackendOf(g.unit, g.writer)
	if err != nil {
		return nil, err
	}
	lister, ok := backend.(heatingGroupLister)
	if !ok {
		return nil, backends.ErrUnsupported
	}
	raw, err := lister.GetHeatingGroupList(ctx)
	if err != nil {
		return nil, err
	}
	return group.ParseGroupList(raw)
}

// SuitableMembers implements [central.HeatingGroups].
func (g *ccuHeatingGroups) SuitableMembers(ctx context.Context, typeID string) (group.SuitableMembers, error) {
	w, err := g.groupWriter()
	if err != nil {
		return group.SuitableMembers{}, err
	}
	res, err := w.SuitableHeatingGroupMembers(ctx, typeID)
	if err != nil {
		return group.SuitableMembers{}, err
	}
	return group.SuitableMembers{
		Assignable: mapCandidates(g.unit, res.Assignable),
		Leftover:   mapCandidates(g.unit, res.Leftover),
	}, nil
}

// Types implements [central.HeatingGroups]: the group types a new group
// can be created as. The firmware carries them only in the create page, so
// this issues a `group/create` (which allocates a throwaway draft, never
// persisted) and returns the parsed type list.
func (g *ccuHeatingGroups) Types(ctx context.Context) ([]group.Type, error) {
	w, err := g.groupWriter()
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

// Create makes a new group. It runs the two-step jpages flow (GET create →
// POST save) with the per-member inHeatingGroup preamble, then confirms
// completion by polling the roster for the new group — the save's HTTP
// response is unreliable (it may time out even though the group committed),
// so the roster is the completion signal.
func (g *ccuHeatingGroups) Create(ctx context.Context, in group.CreateInput) (group.Group, error) {
	w, err := g.groupWriter()
	if err != nil {
		return group.Group{}, err
	}
	before, err := g.rosterIDs(ctx, w)
	if err != nil {
		return group.Group{}, err
	}
	draftID, _, err := w.CreateHeatingGroupDraft(ctx)
	if err != nil {
		return group.Group{}, err
	}
	g.applyMemberPreamble(ctx, w, in.MemberIDs)

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
	created, ok, err := g.pollForNewGroup(ctx, w, before, in.Name)
	if err != nil {
		return group.Group{}, err
	}
	if ok {
		g.applyOperateGroupOnly(ctx, w, in.ForbidSingleOperation, in.MemberIDs)
		return created, nil
	}
	if saveErr != nil {
		return group.Group{}, fmt.Errorf("group create: %w", saveErr)
	}
	return group.Group{}, fmt.Errorf("group create: group did not appear in the roster: %w", backends.ErrUnsupported)
}

// Update edits an existing group. The group's type is immutable, so it is
// carried through from the current roster entry.
func (g *ccuHeatingGroups) Update(ctx context.Context, groupID int, in group.UpdateInput) error {
	w, err := g.groupWriter()
	if err != nil {
		return err
	}
	existing, err := g.findGroup(ctx, w, groupID)
	if err != nil {
		return err
	}
	typeID := in.TypeID
	if typeID == "" {
		typeID = existing.TypeID
	}
	g.applyMemberPreamble(ctx, w, in.MemberIDs)

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
	g.applyOperateGroupOnly(ctx, w, in.ForbidSingleOperation, in.MemberIDs)
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
func (g *ccuHeatingGroups) applyOperateGroupOnly(ctx context.Context, w heatingGroupWriter, forbidSingle bool, memberIDs []string) {
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
func (g *ccuHeatingGroups) Delete(ctx context.Context, groupID int) error {
	w, err := g.groupWriter()
	if err != nil {
		return err
	}
	if _, err := g.findGroup(ctx, w, groupID); err != nil {
		return err
	}
	return w.DeleteHeatingGroup(ctx, groupID)
}

// --- internals --------------------------------------------------------------

func (g *ccuHeatingGroups) applyMemberPreamble(ctx context.Context, w heatingGroupWriter, memberIDs []string) {
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

func (g *ccuHeatingGroups) currentGroups(ctx context.Context, w heatingGroupWriter) ([]group.Group, error) {
	raw, err := w.GetHeatingGroupList(ctx)
	if err != nil {
		return nil, err
	}
	return group.ParseGroupList(raw)
}

func (g *ccuHeatingGroups) rosterIDs(ctx context.Context, w heatingGroupWriter) (map[int]bool, error) {
	groups, err := g.currentGroups(ctx, w)
	if err != nil {
		return nil, err
	}
	ids := make(map[int]bool, len(groups))
	for _, g := range groups {
		ids[g.ID] = true
	}
	return ids, nil
}

func (g *ccuHeatingGroups) findGroup(ctx context.Context, w heatingGroupWriter, groupID int) (group.Group, error) {
	groups, err := g.currentGroups(ctx, w)
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
func (g *ccuHeatingGroups) pollForNewGroup(ctx context.Context, w heatingGroupWriter, before map[int]bool, name string) (group.Group, bool, error) {
	deadline := time.Now().Add(groupSavePollTimeout)
	for {
		if groups, err := g.currentGroups(ctx, w); err == nil {
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
