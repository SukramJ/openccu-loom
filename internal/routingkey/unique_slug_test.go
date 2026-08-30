// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package routingkey_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/routingkey"
)

// TestUniqueSlugCollisionRule pins the collision rule two packages depend on.
//
// The rule used to exist twice — once where a zone is created (the REST
// handler) and once where a row's effective slug is re-derived (the security
// index) — because neither package can import the other. Two copies of a
// collision rule do not stay equal by luck: they diverge on the first edge
// case one of them meets, and a divergence here hands two zones one identity.
func TestUniqueSlugCollisionRule(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, stem string
		taken      []string
		want       string
	}{
		{name: "Erdgeschoss", stem: "zone", want: "erdgeschoss"},
		{name: "Erdgeschoss", stem: "zone", taken: []string{"erdgeschoss"}, want: "erdgeschoss-2"},
		{name: "Erdgeschoss", stem: "zone", taken: []string{"erdgeschoss", "erdgeschoss-2"}, want: "erdgeschoss-3"},
		// A name that slugs to nothing still needs an identifier: the UUID is
		// unusable in an entity id, which is why the slug exists.
		{name: "🏠", stem: "zone", want: "zone"},
		{name: "🏠", stem: "zone", taken: []string{"zone"}, want: "zone-2"},
		{name: "", stem: "zone", want: "zone"},
	} {
		taken := make(map[string]bool, len(c.taken))
		for _, s := range c.taken {
			taken[s] = true
		}
		if got := routingkey.UniqueSlug(c.name, c.stem, taken); got != c.want {
			t.Errorf("UniqueSlug(%q, taken=%v) = %q, want %q", c.name, c.taken, got, c.want)
		}
	}
}
