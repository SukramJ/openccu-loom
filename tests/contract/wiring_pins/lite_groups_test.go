// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package wiring_pins

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/client/transport/occulited"
	"github.com/SukramJ/openccu-loom/internal/model/group"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
	"github.com/SukramJ/openccu-loom/tests/harness/litefake"
)

// TestLiteGroupsCRUD pins the heating-group port a lite central installs,
// through the domain the REST layer calls: the box's types and groups are
// listed, a group is deleted on the box, deleting it again is a not-found,
// and the operations that carry members are refused rather than guessed.
func TestLiteGroupsCRUD(t *testing.T) {
	fake := startFake(t, litefake.Options{})
	c := startLiteSystemCentral(t, fake, litefake.DefaultToken)
	ctx := context.Background()
	d := adapter.NewGroupsDomain(c.reg)

	oc, err := occulited.New(occulited.Config{BaseURL: fake.URL(), Token: litefake.DefaultToken})
	if err != nil {
		t.Fatalf("occulited.New: %v", err)
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

	created, err := oc.CreateGroup(ctx, occulited.GroupCreate{Name: "Obergeschoss", Type: boxTypes.Types[0].ID})
	if err != nil {
		t.Fatalf("CreateGroup on the box: %v", err)
	}
	id, err := strconv.Atoi(created.ID)
	if err != nil {
		t.Fatalf("the fake's group id %q is not numeric: %v", created.ID, err)
	}
	listed, err := d.List(ctx, "box")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != 1 || !slices.ContainsFunc(listed[0].Groups, func(g group.Group) bool {
		return g.ID == id && g.Name == "Obergeschoss" && g.TypeID == boxTypes.Types[0].ID
	}) {
		t.Errorf("listed = %+v, want the box's group %d", listed, id)
	}

	if err := d.Delete(ctx, "box", id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := d.Delete(ctx, "box", id); !errors.Is(err, hmerr.ErrGroupNotFound) {
		t.Errorf("second Delete err = %v, want ErrGroupNotFound", err)
	}

	if _, err := d.Create(ctx, "box", group.CreateInput{Name: "x", TypeID: boxTypes.Types[0].ID}); !errors.Is(err, backends.ErrUnsupported) {
		t.Errorf("Create err = %v, want a refusal matching backends.ErrUnsupported", err)
	}
}
