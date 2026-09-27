// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package taxonomy

import (
	"errors"
	"slices"
	"strings"
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

func TestRefParentAndChild(t *testing.T) {
	t.Parallel()
	r := Root(EnumRoom, "eg").Child("wohnzimmer")
	if r.String() != "room/eg/wohnzimmer" {
		t.Errorf("String = %q", r.String())
	}
	p, ok := r.Parent()
	if !ok || p.String() != "room/eg" {
		t.Errorf("Parent = %v, %v", p, ok)
	}
	if _, ok := p.Parent(); ok {
		t.Error("a root reference has no parent")
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

// TestParseRefRoundTrip pins the string form a system's enum paths arrive
// in: the enum id up to the first slash, the path after it, and nothing
// empty in between.
func TestParseRefRoundTrip(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"room/eg", "room/eg/wohnzimmer", "function/licht", "floor/a-1/b"} {
		r, err := ParseRef(s)
		if err != nil {
			t.Fatalf("ParseRef(%q): %v", s, err)
		}
		if r.String() != s {
			t.Errorf("ParseRef(%q).String() = %q", s, r.String())
		}
	}
	if r, _ := ParseRef("room/eg/wohnzimmer"); r.Enum != EnumRoom || r.Path != "eg/wohnzimmer" {
		t.Errorf("ParseRef split = %+v, want enum room, path eg/wohnzimmer", r)
	}
	for _, s := range []string{"", "room", "room/", "/eg", "room//eg", "room/eg/"} {
		if _, err := ParseRef(s); !errors.Is(err, ErrInvalidRef) {
			t.Errorf("ParseRef(%q) err = %v, want ErrInvalidRef", s, err)
		}
	}
}

// TestAmbiguousNameErrorListsCandidates pins that the error both matches
// the sentinel a caller branches on and names every candidate path.
func TestAmbiguousNameErrorListsCandidates(t *testing.T) {
	t.Parallel()
	err := error(&AmbiguousNameError{Enum: EnumRoom, Name: "Küche", Candidates: []Ref{
		{Enum: EnumRoom, Path: "eg/kueche"}, {Enum: EnumRoom, Path: "og/kueche"},
	}})
	if !errors.Is(err, ErrAmbiguousName) {
		t.Error("AmbiguousNameError does not match ErrAmbiguousName")
	}
	for _, want := range []string{"room/eg/kueche", "room/og/kueche"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %s", err, want)
		}
	}
}
