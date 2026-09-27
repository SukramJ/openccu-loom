// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// TestCCUTaxonomyAdminRefusesNesting pins the flat side of node editing:
// on a CCU, whose rooms and functions do not nest, a node below a parent
// and a move are refused with the tree feature named, and an enum other
// than rooms and functions is invalid — before any CCU call.
func TestCCUTaxonomyAdminRefusesNesting(t *testing.T) {
	t.Parallel()
	u, err := central.New(central.Config{Name: "ccu"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	u.SetFeatures(ccuFeatures("CCU3"))
	reg := central.NewRegistry()
	if err := reg.Register(u); err != nil {
		t.Fatalf("Register: %v", err)
	}
	admin := NewTaxonomyAdmin(reg)
	ctx := context.Background()

	_, err = admin.CreateNode(ctx, "ccu", "room", "1234", "Bad")
	if fe, ok := errors.AsType[*hmerr.FeatureUnavailableError](err); !ok || fe.Feature != hmenum.FeatureTaxonomyTree {
		t.Errorf("nested create: %v, want taxonomy.tree refused", err)
	}
	root := ""
	if err := admin.UpdateNode(ctx, "ccu", "room", "1234", nil, &root, nil); !errors.Is(err, hmerr.ErrFeatureUnavailable) {
		t.Errorf("move: %v, want feature_unavailable", err)
	}
	if _, err := admin.CreateNode(ctx, "ccu", "floor", "", "EG"); !errors.Is(err, hmerr.ErrValidation) {
		t.Errorf("unknown enum: %v, want a validation error", err)
	}
}

// TestLiteNodeIDFromName pins the node id a created node gets: lower case,
// umlauts and ß spelled out, other runs one hyphen, at most 32
// characters, and a numbered suffix when a sibling holds the id.
func TestLiteNodeIDFromName(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		taken []string
		want  string
	}{
		{"Küche", nil, "kueche"},
		{"Gäste WC", nil, "gaeste-wc"},
		{"  Straße / Einfahrt!", nil, "strasse-einfahrt"},
		{"Küche", []string{"kueche"}, "kueche-2"},
		{"Küche", []string{"kueche", "kueche-2"}, "kueche-3"},
		{"???", nil, "node"},
		{"Ein sehr langer Raumname für den Test", nil, "ein-sehr-langer-raumname-fuer-de"},
		{"Ein sehr langer Raumname für den Test", []string{"ein-sehr-langer-raumname-fuer-de"}, "ein-sehr-langer-raumname-fuer-2"},
	} {
		taken := map[string]bool{}
		for _, id := range tc.taken {
			taken[id] = true
		}
		if got := liteNodeID(tc.name, taken); got != tc.want {
			t.Errorf("liteNodeID(%q, %v) = %q, want %q", tc.name, tc.taken, got, tc.want)
		}
	}
}
