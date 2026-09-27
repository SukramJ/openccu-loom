// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package taxonomy models how a system organises its devices and channels:
// named enumerations (rooms, functions, floors, favourites, …), each a tree
// of nodes, and the references from an address to the nodes it belongs to.
//
// The model is backend-neutral. A CCU's rooms and functions are flat lists,
// so they become trees of depth one whose node ids are the ReGa ids. An
// openccu-lite system nests them ("room/eg/wohnzimmer") and treats the enum
// ids themselves as data. Everything that reads the taxonomy works against
// the tree and never learns which system produced it.
//
// The package is pure data: no I/O, and a [Taxonomy] is never mutated after
// it is built — a change builds a new one, so a reader can hold a snapshot
// without locking.
package taxonomy

import (
	"errors"
	"fmt"
	"strings"
)

// EnumID names one taxonomy. It is data, not a closed set: a system may
// define enums beyond the two every Homematic system has.
type EnumID string

// The enums every Homematic system has.
const (
	EnumRoom     EnumID = "room"
	EnumFunction EnumID = "function"
)

// Path is the slash-joined chain of node ids from an enum's root to a node,
// without the enum id ("eg/wohnzimmer"). A node id never contains a slash.
type Path string

// Ref names one node: the enum and the path inside it. Its string form is
// "<enum>/<path>" ("room/eg/wohnzimmer").
type Ref struct {
	Enum EnumID
	Path Path
}

// String renders the reference as "<enum>/<path>".
func (r Ref) String() string { return string(r.Enum) + "/" + string(r.Path) }

// ErrInvalidRef reports a string that is not "<enum>/<path>".
var ErrInvalidRef = errors.New("taxonomy: invalid reference")

// ParseRef reads the string form "<enum>/<path>" ("room/eg/wohnzimmer"):
// the enum id up to the first slash, then at least one node id, none of
// them empty.
func ParseRef(s string) (Ref, error) {
	enum, path, ok := strings.Cut(s, "/")
	if !ok || enum == "" || path == "" {
		return Ref{}, fmt.Errorf("%w: %q", ErrInvalidRef, s)
	}
	for seg := range strings.SplitSeq(path, "/") {
		if seg == "" {
			return Ref{}, fmt.Errorf("%w: %q", ErrInvalidRef, s)
		}
	}
	return Ref{Enum: EnumID(enum), Path: Path(path)}, nil
}

// ErrAmbiguousName reports a display name that several nodes of one enum
// carry, so a write addressed by name cannot tell which node is meant.
var ErrAmbiguousName = errors.New("taxonomy: ambiguous name")

// AmbiguousNameError names the nodes a display name could mean; the caller
// retries with one of their paths. It matches [ErrAmbiguousName].
type AmbiguousNameError struct {
	Enum       EnumID
	Name       string
	Candidates []Ref
}

// Error implements error.
func (e *AmbiguousNameError) Error() string {
	paths := make([]string, len(e.Candidates))
	for i, r := range e.Candidates {
		paths[i] = r.String()
	}
	return fmt.Sprintf("taxonomy: %s name %q is ambiguous: %s", e.Enum, e.Name, strings.Join(paths, ", "))
}

// Is matches [ErrAmbiguousName].
func (e *AmbiguousNameError) Is(target error) bool { return target == ErrAmbiguousName }

// Parent returns the reference of the node one level up, and false for a
// root node.
func (r Ref) Parent() (Ref, bool) {
	i := strings.LastIndexByte(string(r.Path), '/')
	if i < 0 {
		return Ref{}, false
	}
	return Ref{Enum: r.Enum, Path: r.Path[:i]}, true
}

// Child returns the reference of the child with id under r.
func (r Ref) Child(id string) Ref {
	return Ref{Enum: r.Enum, Path: Path(string(r.Path) + "/" + id)}
}

// Root returns the reference of the root node with id in enum.
func Root(enum EnumID, id string) Ref { return Ref{Enum: enum, Path: Path(id)} }

// Assignment is one node an address is directly assigned to, with the
// node's display name as the taxonomy held it when the assignment was
// stamped. Name is empty for a reference the taxonomy does not resolve.
type Assignment struct {
	Ref  Ref
	Name string
}

// Refs returns the references of assignments.
func Refs(assignments []Assignment) []Ref {
	if len(assignments) == 0 {
		return nil
	}
	out := make([]Ref, len(assignments))
	for i, a := range assignments {
		out[i] = a.Ref
	}
	return out
}

// Node is one entry of an enum's tree.
type Node struct {
	// ID is unique among its siblings and stable: renaming a node keeps it.
	ID string
	// Name is the display name.
	Name string
	// Icon is an optional icon hint; unknown values are ignored.
	Icon     string
	Children []*Node
}

// Enum is one taxonomy: its display names and its ordered tree.
type Enum struct {
	ID EnumID
	// Names maps a language tag ("en", "de") to the display name.
	Names map[string]string
	// Roots is the ordered top level of the tree; order is display order.
	Roots []*Node
}

// Taxonomy is an immutable snapshot of every enum a system defines.
type Taxonomy struct {
	// Revision is the source's revision counter; 0 when the source has none.
	Revision uint64
	Enums    map[EnumID]*Enum
}

// Node resolves r to its node.
func (t *Taxonomy) Node(r Ref) (*Node, bool) {
	chain := t.chain(r)
	if chain == nil {
		return nil, false
	}
	return chain[len(chain)-1], true
}

// Ancestors returns the nodes above r, root first, excluding r itself. It is
// nil for a root node or an unknown reference.
func (t *Taxonomy) Ancestors(r Ref) []*Node {
	chain := t.chain(r)
	if len(chain) < 2 {
		return nil
	}
	return chain[:len(chain)-1]
}

// ParentRef returns the reference of r's parent, and false for a root node
// or an unknown reference.
func (t *Taxonomy) ParentRef(r Ref) (Ref, bool) {
	if _, ok := t.Node(r); !ok {
		return Ref{}, false
	}
	return r.Parent()
}

// FindByName returns every node in enum whose display name is name, in tree
// order. Several nodes may share a name (two "Bad" rooms on two floors).
func (t *Taxonomy) FindByName(enum EnumID, name string) []Ref {
	var out []Ref
	t.Walk(enum, func(r Ref, n *Node, _ int) {
		if n.Name == name {
			out = append(out, r)
		}
	})
	return out
}

// Walk visits every node of enum depth-first in tree order; depth is 0 for a
// root node.
func (t *Taxonomy) Walk(enum EnumID, fn func(r Ref, n *Node, depth int)) {
	if t == nil {
		return
	}
	e := t.Enums[enum]
	if e == nil {
		return
	}
	var walk func(prefix Ref, hasPrefix bool, nodes []*Node, depth int)
	walk = func(prefix Ref, hasPrefix bool, nodes []*Node, depth int) {
		for _, n := range nodes {
			r := Root(enum, n.ID)
			if hasPrefix {
				r = prefix.Child(n.ID)
			}
			fn(r, n, depth)
			walk(r, true, n.Children, depth+1)
		}
	}
	walk(Ref{}, false, e.Roots, 0)
}

// chain returns the nodes from the root down to r, or nil when r does not
// resolve.
func (t *Taxonomy) chain(r Ref) []*Node {
	if t == nil || r.Path == "" {
		return nil
	}
	e := t.Enums[r.Enum]
	if e == nil {
		return nil
	}
	ids := strings.Split(string(r.Path), "/")
	chain := make([]*Node, 0, len(ids))
	level := e.Roots
	for _, id := range ids {
		var next *Node
		for _, n := range level {
			if n.ID == id {
				next = n
				break
			}
		}
		if next == nil {
			return nil
		}
		chain = append(chain, next)
		level = next.Children
	}
	return chain
}
