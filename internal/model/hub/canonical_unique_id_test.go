// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hub

import "testing"

// The routing key for a hub entity is keyed on the CCU's stable id rather
// than on its display name. The name is editable in the WebUI, and a key
// built from it re-keys the consumer's entity on every rename — taking its
// history, its area and every automation built on it. Both families carry an
// id; the name slug stands in only while that id is unresolved.
//
// The reference implementation applies the same rule, which is what keeps
// these fixtures shared rather than declared divergences. Provenance is in
// notes/parity/, not here.

func TestSysvarCanonicalUniqueIDUsesTheVid(t *testing.T) {
	const serial10 = "11a0001234"
	sv := NewSysvar("home", "Außen Temperatur", "", "", nil)
	sv.ApplyMeta(SysvarMeta{Vid: 12345})

	got := sv.CanonicalUniqueID(serial10)
	if want := "loom_11a0001234_sysvar_12345"; got != want {
		t.Fatalf("Sysvar.CanonicalUniqueID = %q, want %q", got, want)
	}

	// A rename must not move the key — that is the whole point.
	renamed := NewSysvar("home", "Aussentemperatur Nord", "", "", nil)
	renamed.ApplyMeta(SysvarMeta{Vid: 12345})
	if renamed.CanonicalUniqueID(serial10) != got {
		t.Fatalf("rename moved the key: %q -> %q", got, renamed.CanonicalUniqueID(serial10))
	}
}

func TestSysvarCanonicalUniqueIDFallsBackToTheSlug(t *testing.T) {
	// Vid is 0 until a hub scan resolves it. A key is still owed then, and a
	// consumer rebuilding one has to accept this shape during the rollover.
	sv := NewSysvar("home", "Außen Temperatur", "", "", nil)

	got := sv.CanonicalUniqueID("11a0001234")
	if want := "loom_11a0001234_sysvar_aussen-temperatur"; got != want {
		t.Fatalf("Sysvar.CanonicalUniqueID without a vid = %q, want %q", got, want)
	}
}

func TestProgramCanonicalUniqueIDUsesTheID(t *testing.T) {
	const serial10 = "11a0001234"
	p := NewProgram("home", "1234", "My Prog", "", false, nil)

	got := p.CanonicalUniqueID(serial10)
	if want := "loom_11a0001234_program_1234"; got != want {
		t.Fatalf("Program.CanonicalUniqueID = %q, want %q", got, want)
	}

	renamed := NewProgram("home", "1234", "Renamed Prog", "", false, nil)
	if renamed.CanonicalUniqueID(serial10) != got {
		t.Fatalf("rename moved the key: %q -> %q", got, renamed.CanonicalUniqueID(serial10))
	}
}

func TestProgramCanonicalUniqueIDFallsBackToTheSlug(t *testing.T) {
	p := NewProgram("home", "", "My Prog", "", false, nil)

	got := p.CanonicalUniqueID("11a0001234")
	if want := "loom_11a0001234_program_my-prog"; got != want {
		t.Fatalf("Program.CanonicalUniqueID without an id = %q, want %q", got, want)
	}
}
