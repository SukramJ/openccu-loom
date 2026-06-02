// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hub

import "testing"

func TestSysvarCanonicalUniqueID(t *testing.T) {
	sv := NewSysvar("home", "Außen Temperatur", "", "", nil)
	const serial10 = "11a0001234"
	got := sv.CanonicalUniqueID(serial10)
	want := "loom_11a0001234_sysvar_aussen-temperatur"
	if got != want {
		t.Fatalf("Sysvar.CanonicalUniqueID = %q, want %q", got, want)
	}
}

func TestProgramCanonicalUniqueID(t *testing.T) {
	p := NewProgram("home", "1234", "My Prog", "", false, nil)
	const serial10 = "11a0001234"
	got := p.CanonicalUniqueID(serial10)
	want := "loom_11a0001234_program_my-prog"
	if got != want {
		t.Fatalf("Program.CanonicalUniqueID = %q, want %q", got, want)
	}
}
