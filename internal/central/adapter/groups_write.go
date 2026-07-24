// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/SukramJ/openccu-loom/internal/client/backends"
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
}

// Poll cadence for the fire-and-poll save path (var so tests can shrink it).
var (
	groupSavePollTimeout  = 60 * time.Second
	groupSavePollInterval = 2 * time.Second
)

// writerFor resolves the group-write capability for a central, or an error:
// ErrUnknownCentral (no such central) / ErrUnsupported (backend can't manage
// groups).
func (a *GroupsDomain) writerFor(centralName string) (heatingGroupWriter, error) {
	if a.registry == nil || a.writer == nil {
		return nil, hmerr.ErrUnknownCentral
	}
	// A write targets exactly one central. An empty name resolves to the sole
	// registered central (single-CCU convenience); with several it is
	// ambiguous and rejected.
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
	_, backend, err := primaryBackendOf(unit, a.writer)
	if err != nil {
		return nil, err
	}
	w, ok := backend.(heatingGroupWriter)
	if !ok {
		return nil, backends.ErrUnsupported
	}
	return w, nil
}

// Types lists the group types a new group can be created as. The firmware
// carries them only in the create page, so this issues a `group/create`
// (which allocates a throwaway draft, never persisted) and returns the parsed
// type list.
func (a *GroupsDomain) Types(ctx context.Context, centralName string) ([]group.Type, error) {
	w, err := a.writerFor(centralName)
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
	w, err := a.writerFor(centralName)
	if err != nil {
		return group.SuitableMembers{}, err
	}
	res, err := w.SuitableHeatingGroupMembers(ctx, typeID)
	if err != nil {
		return group.SuitableMembers{}, err
	}
	return group.SuitableMembers{
		Assignable: mapCandidates(res.Assignable),
		Leftover:   mapCandidates(res.Leftover),
	}, nil
}

// Create makes a new group. It runs the two-step jpages flow (GET create →
// POST save) with the per-member inHeatingGroup preamble, then confirms
// completion by polling the roster for the new group — the save's HTTP
// response is unreliable (it may time out even though the group committed),
// so the roster is the completion signal.
func (a *GroupsDomain) Create(ctx context.Context, centralName string, in group.CreateInput) (group.Group, error) {
	w, err := a.writerFor(centralName)
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
	w, err := a.writerFor(centralName)
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
	return nil
}

// Delete removes a group by id, 404-ing (ErrGroupNotFound) when the roster
// does not carry it.
func (a *GroupsDomain) Delete(ctx context.Context, centralName string, groupID int) error {
	w, err := a.writerFor(centralName)
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

func mapCandidates(in []backends.HeatingGroupMember) []group.MemberCandidate {
	out := make([]group.MemberCandidate, 0, len(in))
	for _, m := range in {
		out = append(out, group.MemberCandidate{
			Address: m.ID,
			Serial:  m.SerialNumber,
			Type:    m.Type,
		})
	}
	return out
}
