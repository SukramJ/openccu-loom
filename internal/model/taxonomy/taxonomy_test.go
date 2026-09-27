// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package taxonomy

import (
	"errors"
	"slices"
	"testing"
)

func sample() *Taxonomy {
	return &Taxonomy{Enums: map[EnumID]*Enum{
		EnumRoom: {
			ID:    EnumRoom,
			Names: map[string]string{"en": "Rooms"},
			Roots: []*Node{
				{ID: "eg", Name: "Erdgeschoss", Children: []*Node{
					{ID: "wohnzimmer", Name: "Wohnzimmer"},
					{ID: "bad", Name: "Bad"},
				}},
				{ID: "og", Name: "Obergeschoss", Children: []*Node{
					{ID: "bad", Name: "Bad"},
				}},
			},
		},
	}}
}

func TestParseRefRoundTrip(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"room/eg", "room/eg/wohnzimmer", "function/1234", "favorite/_user7/page-2"} {
		r, err := ParseRef(s)
		if err != nil {
			t.Fatalf("ParseRef(%q): %v", s, err)
		}
		if r.String() != s {
			t.Errorf("round trip %q → %q", s, r.String())
		}
	}
	for _, bad := range []string{"", "room", "room/", "/eg", "room//eg", "room/eg/"} {
		if _, err := ParseRef(bad); !errors.Is(err, ErrInvalidRef) {
			t.Errorf("ParseRef(%q) = %v, want ErrInvalidRef", bad, err)
		}
	}
}

func TestTaxonomyAncestorsAndFindByName(t *testing.T) {
	t.Parallel()
	tx := sample()

	wz := Ref{Enum: EnumRoom, Path: "eg/wohnzimmer"}
	n, ok := tx.Node(wz)
	if !ok || n.Name != "Wohnzimmer" {
		t.Fatalf("Node(%s) = %+v, %v", wz, n, ok)
	}
	anc := tx.Ancestors(wz)
	if len(anc) != 1 || anc[0].Name != "Erdgeschoss" {
		t.Errorf("Ancestors(%s) = %+v, want [Erdgeschoss]", wz, anc)
	}
	if p, ok := tx.ParentRef(wz); !ok || p.String() != "room/eg" {
		t.Errorf("ParentRef = %v, %v", p, ok)
	}
	if _, ok := tx.ParentRef(Ref{Enum: EnumRoom, Path: "eg"}); ok {
		t.Error("a root node has no parent")
	}
	if _, ok := tx.Node(Ref{Enum: EnumRoom, Path: "eg/keller"}); ok {
		t.Error("an unknown path resolved")
	}
	if _, ok := tx.Node(Ref{Enum: "floor", Path: "eg"}); ok {
		t.Error("an unknown enum resolved")
	}

	baths := tx.FindByName(EnumRoom, "Bad")
	got := make([]string, 0, len(baths))
	for _, r := range baths {
		got = append(got, r.String())
	}
	if want := []string{"room/eg/bad", "room/og/bad"}; !slices.Equal(got, want) {
		t.Errorf("FindByName(Bad) = %v, want %v", got, want)
	}

	var depths []int
	tx.Walk(EnumRoom, func(_ Ref, _ *Node, d int) { depths = append(depths, d) })
	if want := []int{0, 1, 1, 0, 1}; !slices.Equal(depths, want) {
		t.Errorf("Walk depths = %v, want %v", depths, want)
	}
	var nilTx *Taxonomy
	nilTx.Walk(EnumRoom, func(Ref, *Node, int) { t.Error("walked a nil taxonomy") })
}
