// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package wiring_pins

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/SukramJ/godevccu/pkg/litefake"

	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/client/transport/occulited"
	"github.com/SukramJ/openccu-loom/internal/model/group"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

const (
	liteGroupType   = "hmip.heating.group"
	liteGroupWindow = "0000000000AA01:1"
	liteGroupRelay  = "0000000000BB02:9"
)

// liteGroupCandidates seeds the channels the box's HmIP heating-group type
// can take, in the shape a box lists them.
func liteGroupCandidates() map[string][]litefake.GroupMember {
	return map[string][]litefake.GroupMember{liteGroupType: {
		{ID: liteGroupWindow, Serial: liteGroupWindow, Type: "SENSOR_WINDOW"},
		{ID: liteGroupRelay, Serial: liteGroupRelay, Type: "SWITCH_ACTUATOR"},
	}}
}

func memberAddresses(ms []group.Member) []string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.Address)
	}
	return out
}

func candidateAddresses(cs []group.MemberCandidate) []string {
	out := make([]string, 0, len(cs))
	for i := range cs {
		out = append(out, cs[i].Address)
	}
	return out
}

// TestLiteGroupsCRUD pins the heating-group port a lite central installs,
// through the domain the REST layer calls, as a full round trip on the
// box: the types and their candidates are listed, a group is created with
// a member, read back with it, given a second member, and deleted — and
// each step is checked on the box itself, not on the port's own answer.
func TestLiteGroupsCRUD(t *testing.T) {
	fake := startFake(t, litefake.Options{GroupCandidates: liteGroupCandidates()})
	c := startLiteSystemCentral(t, fake, litefake.DefaultToken)
	ctx := context.Background()
	d := adapter.NewGroupsDomain(c.reg)

	oc, err := occulited.New(occulited.Config{BaseURL: fake.URL(), Token: litefake.DefaultToken})
	if err != nil {
		t.Fatalf("occulited.New: %v", err)
	}
	boxMembers := func(id int) []string {
		t.Helper()
		detail, err := oc.Group(ctx, id)
		if err != nil {
			t.Fatalf("Group(%d) on the box: %v", id, err)
		}
		out := make([]string, 0, len(detail.Members))
		for _, m := range detail.Members {
			out = append(out, m.ID)
		}
		return out
	}

	boxTypes, err := oc.GroupTypes(ctx)
	if err != nil || len(boxTypes.Types) == 0 {
		t.Fatalf("GroupTypes: %v (%d types)", err, len(boxTypes.Types))
	}
	types, err := d.Types(ctx, "box")
	if err != nil {
		t.Fatalf("Types: %v", err)
	}
	if len(types) != len(boxTypes.Types) || types[0].ID != boxTypes.Types[0].ID || types[0].LabelKey != boxTypes.Types[0].Label {
		t.Errorf("types = %+v, want the box's %+v", types, boxTypes.Types)
	}

	suitable, err := d.SuitableMembers(ctx, "box", liteGroupType)
	if err != nil {
		t.Fatalf("SuitableMembers: %v", err)
	}
	if got := candidateAddresses(suitable.Assignable); !slices.Equal(got, []string{liteGroupWindow, liteGroupRelay}) {
		t.Errorf("assignable = %v, want both seeded channels", got)
	}
	if len(suitable.Assignable) > 0 && suitable.Assignable[0].Type != "SENSOR_WINDOW" {
		t.Errorf("candidate type = %q, want the box's SENSOR_WINDOW", suitable.Assignable[0].Type)
	}

	created, err := d.Create(ctx, "box", group.CreateInput{
		Name: "Obergeschoss", TypeID: liteGroupType, ForbidSingleOperation: true, MemberIDs: []string{liteGroupWindow},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID == 0 || created.Name != "Obergeschoss" || created.TypeID != liteGroupType || !created.ForbidSingleOperation ||
		!slices.Equal(memberAddresses(created.Members), []string{liteGroupWindow}) {
		t.Errorf("created = %+v, want the group with its member", created)
	}
	if got := boxMembers(created.ID); !slices.Equal(got, []string{liteGroupWindow}) {
		t.Fatalf("the box holds members %v after Create, want [%s]", got, liteGroupWindow)
	}

	// A member of a group is no longer assignable; the box lists it as
	// leftover.
	suitable, err = d.SuitableMembers(ctx, "box", liteGroupType)
	if err != nil {
		t.Fatalf("SuitableMembers after Create: %v", err)
	}
	if !slices.Equal(candidateAddresses(suitable.Assignable), []string{liteGroupRelay}) ||
		!slices.Equal(candidateAddresses(suitable.Leftover), []string{liteGroupWindow}) {
		t.Errorf("after Create: assignable %v leftover %v", candidateAddresses(suitable.Assignable), candidateAddresses(suitable.Leftover))
	}

	listed, err := d.List(ctx, "box")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != 1 || !slices.ContainsFunc(listed[0].Groups, func(g group.Group) bool {
		return g.ID == created.ID && g.Name == "Obergeschoss" && g.TypeID == liteGroupType && g.ForbidSingleOperation &&
			slices.Equal(memberAddresses(g.Members), []string{liteGroupWindow})
	}) {
		t.Errorf("listed = %+v, want the box's group %d with its member", listed, created.ID)
	}

	if err := d.Update(ctx, "box", created.ID, group.UpdateInput{
		TypeID: liteGroupType, Name: "Obergeschoss neu", MemberIDs: []string{liteGroupWindow, liteGroupRelay},
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got := boxMembers(created.ID); !slices.Equal(got, []string{liteGroupWindow, liteGroupRelay}) {
		t.Errorf("the box holds members %v after Update, want both", got)
	}
	if detail, _ := oc.Group(ctx, created.ID); detail.Name != "Obergeschoss neu" || detail.ForbidSingleOperation {
		t.Errorf("after Update the box holds name %q forbid %v, want the new name and the flag off", detail.Name, detail.ForbidSingleOperation)
	}
	if err := d.Update(ctx, "box", 4711, group.UpdateInput{TypeID: liteGroupType, Name: "x"}); !errors.Is(err, hmerr.ErrGroupNotFound) {
		t.Errorf("Update of an unknown group err = %v, want ErrGroupNotFound", err)
	}

	if err := d.Delete(ctx, "box", created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := d.Delete(ctx, "box", created.ID); !errors.Is(err, hmerr.ErrGroupNotFound) {
		t.Errorf("second Delete err = %v, want ErrGroupNotFound", err)
	}
}

// TestLiteGroupWriteReportsAMemberTheBoxDropped pins the check the port
// owes its caller: a box answers 200 to a write naming a member its group
// type cannot take and simply does not assign it. The port must not pass
// that on as success — a create leaves no half-made group behind, and an
// update names the member that did not arrive.
func TestLiteGroupWriteReportsAMemberTheBoxDropped(t *testing.T) {
	fake := startFake(t, litefake.Options{GroupCandidates: liteGroupCandidates()})
	c := startLiteSystemCentral(t, fake, litefake.DefaultToken)
	ctx := context.Background()
	d := adapter.NewGroupsDomain(c.reg)
	const stranger = "0000000000FFFF:1"

	if _, err := d.Create(ctx, "box", group.CreateInput{
		Name: "Leer", TypeID: liteGroupType, MemberIDs: []string{liteGroupWindow, stranger},
	}); err == nil || !errors.Is(err, adapter.ErrGroupMembersNotAssigned) {
		t.Fatalf("Create with a member the box drops: err = %v, want ErrGroupMembersNotAssigned", err)
	}
	if left := fake.Groups(); len(left) != 0 {
		t.Errorf("the refused create left %d group(s) on the box: %+v", len(left), left)
	}

	created, err := d.Create(ctx, "box", group.CreateInput{Name: "EG", TypeID: liteGroupType, MemberIDs: []string{liteGroupWindow}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	err = d.Update(ctx, "box", created.ID, group.UpdateInput{TypeID: liteGroupType, Name: "EG", MemberIDs: []string{liteGroupWindow, stranger}})
	if err == nil || !errors.Is(err, adapter.ErrGroupMembersNotAssigned) {
		t.Errorf("Update with a member the box drops: err = %v, want ErrGroupMembersNotAssigned", err)
	}
}
