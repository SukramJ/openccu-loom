// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"slices"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/naming"
)

// hazardCentrals are the CCU names the discovery-slug unification moved, one
// per divergence class, plus the two names it promised not to move.
//
// They are named here rather than added to a golden fixture on purpose: a
// fixture is regenerated from production code, so a row asserting "the node
// id is X" cannot distinguish the code being right from the code and the row
// moving together. These are literals, written down once, that a regeneration
// cannot reach.
var hazardCentrals = []struct {
	name string
	// wasSlug is the pre-unification spelling, nowSlug the shared rule's.
	// Equal means the central was never affected.
	wasSlug, nowSlug string
}{
	{"Café", "caf", "cafe"},
	{"Søren", "s_ren", "soeren"},
	{"Watchdog:_CCU-Jack", "watchdog__ccu-jack", "watchdog_ccu-jack"},
	{"CCU Küche", "ccu_kueche", "ccu_kueche"},
	{"ccu-01", "ccu-01", "ccu-01"},
}

// TestDiscoverySlugUnificationMovedTheseFields is the blast-radius pin: for
// every field a central name reaches, it states whether the unification moved
// it and to what.
//
// It exists because the eleven golden fixtures cannot answer this. Every
// central in them (`ccu-01`, `ccu-02`, `CCU Küche`, `ccu-keller`) is a name
// the two rules already agreed on, so the fixtures ship green through a
// change that moves a published device identifier on any installation whose
// CCU is named in French, Spanish, Danish, Swedish or Norwegian. This is the
// measurement that decided the change was byte-risky under ADR 0068 and had
// to carry a migration note.
//
// Read alongside TestDiscoverySlugUnificationLeftTheseFieldsAlone, which
// pins the other half — the fields a central name does NOT reach.
func TestDiscoverySlugUnificationMovedTheseFields(t *testing.T) {
	t.Parallel()

	pd := naming.PathData{Address: "0001ABCD", ChannelNo: 1}

	for _, c := range hazardCentrals {
		moved := c.wasSlug != c.nowSlug

		t.Run("node_id/per-device/"+c.name, func(t *testing.T) {
			t.Parallel()
			got := pd.DiscoveryNodeID(c.name)
			if want := c.nowSlug + "_0001abcd"; got != want {
				t.Errorf("DiscoveryNodeID(%q) = %q, want %q", c.name, got, want)
			}
			if was := c.wasSlug + "_0001abcd"; moved == (got == was) {
				t.Errorf("DiscoveryNodeID(%q) = %q; moved=%v but the old spelling was %q", c.name, got, moved, was)
			}
		})

		t.Run("node_id/hub/"+c.name, func(t *testing.T) {
			t.Parallel()
			got := hubNodeID(c.name, "sysvars")
			if want := c.nowSlug + "_sysvars"; got != want {
				t.Errorf("hubNodeID(%q, sysvars) = %q, want %q", c.name, got, want)
			}
		})

		t.Run("identifiers/central/"+c.name, func(t *testing.T) {
			t.Parallel()
			got := centralDeviceIdentifier(c.name)
			if want := "openccu-loom_central_" + c.nowSlug; got != want {
				t.Errorf("centralDeviceIdentifier(%q) = %q, want %q", c.name, got, want)
			}
			// This is the field that put the change behind ADR 0068: Home
			// Assistant keys the DEVICE registry on `identifiers` and has no
			// migration path for it either.
			if was := "openccu-loom_central_" + c.wasSlug; moved == (got == was) {
				t.Errorf("centralDeviceIdentifier(%q) = %q; moved=%v but the old value was %q", c.name, got, moved, was)
			}
		})

		t.Run("identifiers/central-scoped-device/"+c.name, func(t *testing.T) {
			t.Parallel()
			// BidCoS-RF is a virtual-remote root: an address that repeats
			// verbatim across CCUs, so its identifier carries the central
			// slug (routingkey.NeedsCentralScope). A globally unique hardware
			// address does not, and is therefore untouched — asserted below.
			got := physicalDeviceIdentifier(c.name, "BidCoS-RF")
			if want := "openccu-loom_" + c.nowSlug + "_bidcos-rf"; got != want {
				t.Errorf("physicalDeviceIdentifier(%q, BidCoS-RF) = %q, want %q", c.name, got, want)
			}
			if bare := physicalDeviceIdentifier(c.name, "0001D3C99C1234"); bare != "openccu-loom_0001d3c99c1234" {
				t.Errorf("physicalDeviceIdentifier(%q, 0001D3C99C1234) = %q — a globally unique address must carry no central slug and therefore cannot move", c.name, bare)
			}
		})

		t.Run("orphan-sweep-reaches-the-old-spelling/"+c.name, func(t *testing.T) {
			t.Parallel()
			// ADR 0068 obligation 3: the retained configs of the old identity
			// are retracted rather than left as permanently unavailable
			// phantoms. The sweep can only reach them if it still recognises
			// the pre-unification node-id prefix.
			prefixes := discoveryNodePrefixes(c.name)
			if !slices.Contains(prefixes, c.nowSlug+"_") {
				t.Errorf("discoveryNodePrefixes(%q) = %v, missing the canonical prefix %q", c.name, prefixes, c.nowSlug+"_")
			}
			if !slices.Contains(prefixes, c.wasSlug+"_") {
				t.Errorf("discoveryNodePrefixes(%q) = %v, missing the pre-unification prefix %q — every entity of this CCU keeps a phantom config forever", c.name, prefixes, c.wasSlug+"_")
			}
			if moved && len(prefixes) < 2 {
				t.Errorf("discoveryNodePrefixes(%q) = %v; a moved central needs both spellings", c.name, prefixes)
			}
		})
	}
}

// TestDiscoverySlugUnificationLeftTheseFieldsAlone is the other half of the
// blast radius, and the half that decides how the change ships: the fields a
// slugged name does NOT reach.
//
//   - `unique_id` on the per-datapoint planes is
//     [DefaultDiscoveryBuilder.scopedUniqueID] — the CCU SERIAL, never the
//     name. On the hub planes it is `routingkey.CanonicalUniqueID` over the
//     serial and the ISE id, via `routingkey.HubSlug`, which this change does
//     not touch.
//   - `default_entity_id` is seeded from the `unique_id` on the planes that
//     publish one at all, and suppressed on the rest.
//   - state / command / availability topics go through `naming.TopicSafe`
//     (`topic.Safe`), which was already the shared rule and normalises far
//     less.
//
// A change that made any of these depend on the discovery slug would turn a
// recoverable topic move into an unrecoverable registry orphan, and that is
// what this test refuses.
func TestDiscoverySlugUnificationLeftTheseFieldsAlone(t *testing.T) {
	t.Parallel()

	for _, c := range hazardCentrals {
		t.Run("topics/"+c.name, func(t *testing.T) {
			t.Parallel()
			// TopicSafe keeps the name recognisable; it neither
			// transliterates nor collapses, so no divergence class reaches it.
			got := naming.MQTTHubSysvarState("gh", c.name, "Café Terrasse")
			if !strings.Contains(got, naming.TopicSafe(c.name)) {
				t.Errorf("MQTTHubSysvarState(%q) = %q, expected the TopicSafe spelling %q", c.name, got, naming.TopicSafe(c.name))
			}
			if strings.Contains(got, naming.DiscoverySlug(c.name)) && naming.DiscoverySlug(c.name) != naming.TopicSafe(c.name) {
				t.Errorf("MQTTHubSysvarState(%q) = %q went through the discovery slug — a state topic must not follow the discovery identifier", c.name, got)
			}
		})
	}

	t.Run("the CCU interface vocabulary is slug-invariant", func(t *testing.T) {
		t.Parallel()
		// safeLower(iface) feeds install-mode and connectivity unique_ids
		// (`loom_<serial10>_install_mode_<suffix>`), which is the one path
		// from this function to a `unique_id`. It is safe only because every
		// CCU interface id is ASCII with no separator run — asserted, not
		// assumed, because a new interface family carrying either would move
		// a unique_id with no migration path.
		for _, iface := range []string{"HmIP-RF", "BidCos-RF", "BidCos-Wired", "VirtualDevices", "CUxD"} {
			// A plain lower-case, compared byte for byte — not a fold. The
			// claim is that the slug does nothing here beyond the case
			// change, so an equality that ignored case would assert nothing.
			want := strings.ToLower(iface)
			if got := safeLower(iface); got != want {
				t.Errorf("safeLower(%q) = %q, want %q — this interface id is not slug-invariant and its unique_id would move", iface, got, want)
			}
		}
	})
}
