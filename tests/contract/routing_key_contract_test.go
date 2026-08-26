// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/routingkey"
)

// The fixtures under testdata/routing_key/ are byte-pinned copies of the
// shared cross-backend routing-key golden fixtures. Every consumer of
// the contract replays the same cases so the key format cannot silently
// drift between backends; see
// docs/external-clients/ha-drop-in-identity-and-scoping.md.
//
// The fixtures' `expected` values are no longer trusted blindly: they are
// verified automatically against the Python reference implementation by
// script/routing_key_parity.py (run via `make routing-key-parity`), which
// replays every case through the reference generate_unique_id /
// generate_channel_unique_id and python-slugify's slugify(). The tests
// below pin Go == fixtures; the parity script pins Python == fixtures;
// together they form the cross-repo guard Go == Python. When the contract
// bumps, re-pin the *_golden.json files and re-run the parity script so
// the new `expected` values stay reference-verified — do not hand-edit
// them away from the upstream output.
//
// Deliberate divergences live in their own fixture
// (cuxd_scoping_golden.json) so the shared files above stay byte-identical
// copies. Both sides replay it: this file asserts the Go output and that
// the divergence is still real, the parity script asserts the reference
// output and that it still differs. A divergence that is only in the code
// is invisible to both halves of the guard, which leaves
// `make routing-key-parity` green while the two implementations disagree.

func loadRoutingKeyFixture(t *testing.T, name string, into any) {
	t.Helper()
	path := filepath.Join("testdata", "routing_key", name)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	if err := json.Unmarshal(raw, into); err != nil {
		t.Fatalf("decode fixture %s: %v", name, err)
	}
}

// TestRoutingKeyUniqueIDGolden locks GenerateUniqueID bit-for-bit against
// the parameter-level routing-key contract.
func TestRoutingKeyUniqueIDGolden(t *testing.T) {
	var fixture struct {
		Cases []struct {
			CentralID string `json:"central_id"`
			Address   string `json:"address"`
			Parameter string `json:"parameter"`
			Prefix    string `json:"prefix"`
			Expected  string `json:"expected"`
		} `json:"cases"`
	}
	loadRoutingKeyFixture(t, "unique_id_golden.json", &fixture)
	if len(fixture.Cases) == 0 {
		t.Fatal("unique_id fixture carries no cases")
	}
	for _, c := range fixture.Cases {
		got := routingkey.GenerateUniqueID(c.CentralID, c.Address, c.Parameter, c.Prefix)
		if got != c.Expected {
			t.Errorf("GenerateUniqueID(%q, %q, %q, %q) = %q, want %q",
				c.CentralID, c.Address, c.Parameter, c.Prefix, got, c.Expected)
		}
	}
}

// TestRoutingKeyChannelUniqueIDGolden locks GenerateChannelUniqueID
// against the channel/device-level routing-key contract.
func TestRoutingKeyChannelUniqueIDGolden(t *testing.T) {
	var fixture struct {
		Cases []struct {
			CentralID string `json:"central_id"`
			Address   string `json:"address"`
			Expected  string `json:"expected"`
		} `json:"cases"`
	}
	loadRoutingKeyFixture(t, "channel_unique_id_golden.json", &fixture)
	if len(fixture.Cases) == 0 {
		t.Fatal("channel_unique_id fixture carries no cases")
	}
	for _, c := range fixture.Cases {
		got := routingkey.GenerateChannelUniqueID(c.CentralID, c.Address)
		if got != c.Expected {
			t.Errorf("GenerateChannelUniqueID(%q, %q) = %q, want %q",
				c.CentralID, c.Address, got, c.Expected)
		}
	}
}

// TestRoutingKeyCUxDScopingGolden locks the one address family whose key
// deliberately differs from the shared cross-backend contract: CUxD hands
// out the same synthetic addresses on every CCU it runs on (the first
// "(28) System" device is `CUX2801001` on every install), so the
// parameter-level key carries the central discriminator here while the
// reference implementation leaves it bare.
//
// The divergence is pinned rather than merely allowed. `expected` is what
// this side emits; `reference_expected` is what the other side emits, and
// the two must still differ — a Go-side revert to parity would otherwise
// leave the fixture, the published client contract and
// notes/parity/by_design.md describing a rule the code no longer applies,
// which is exactly the state that made the divergence invisible to
// `make routing-key-parity` in the first place. The channel-level cases
// carry no `reference_expected` because both sides agree there: only the
// parameter-level key is scoped.
func TestRoutingKeyCUxDScopingGolden(t *testing.T) {
	var fixture struct {
		UniqueIDCases []struct {
			CentralID         string `json:"central_id"`
			Address           string `json:"address"`
			Parameter         string `json:"parameter"`
			Prefix            string `json:"prefix"`
			Expected          string `json:"expected"`
			ReferenceExpected string `json:"reference_expected"`
		} `json:"unique_id_cases"`
		ChannelCases []struct {
			CentralID string `json:"central_id"`
			Address   string `json:"address"`
			Expected  string `json:"expected"`
		} `json:"channel_unique_id_cases"`
	}
	loadRoutingKeyFixture(t, "cuxd_scoping_golden.json", &fixture)
	if len(fixture.UniqueIDCases) == 0 || len(fixture.ChannelCases) == 0 {
		t.Fatal("CUxD scoping fixture carries no cases")
	}
	for _, c := range fixture.UniqueIDCases {
		got := routingkey.GenerateUniqueID(c.CentralID, c.Address, c.Parameter, c.Prefix)
		if got != c.Expected {
			t.Errorf("GenerateUniqueID(%q, %q, %q, %q) = %q, want %q",
				c.CentralID, c.Address, c.Parameter, c.Prefix, got, c.Expected)
		}
		if c.ReferenceExpected == "" {
			t.Errorf("case %q carries no reference_expected; the divergence it declares cannot be checked", c.Address)
			continue
		}
		if c.Expected == c.ReferenceExpected {
			t.Errorf("case %q declares a divergence that no longer exists (%q); retire the fixture entry, "+
				"the by_design.md rationale and the scoped-class list in the published client contract together",
				c.Address, c.Expected)
		}
		if !routingkey.NeedsCentralScope(c.Address) {
			t.Errorf("NeedsCentralScope(%q) = false; the north-bound planes would publish the colliding "+
				"unscoped id instead of withholding it until the serial is known", c.Address)
		}
	}
	for _, c := range fixture.ChannelCases {
		got := routingkey.GenerateChannelUniqueID(c.CentralID, c.Address)
		if got != c.Expected {
			t.Errorf("GenerateChannelUniqueID(%q, %q) = %q, want %q",
				c.CentralID, c.Address, got, c.Expected)
		}
	}
}

// TestRoutingKeyHubSlugGolden locks HubSlug against the hub-name slug
// contract — the Unicode-transliteration rule whose drift silently loses
// Home Assistant entities for any non-ASCII hub name.
func TestRoutingKeyHubSlugGolden(t *testing.T) {
	var fixture struct {
		Cases []struct {
			Name string `json:"name"`
			Slug string `json:"slug"`
		} `json:"cases"`
	}
	loadRoutingKeyFixture(t, "hub_slug_golden.json", &fixture)
	if len(fixture.Cases) == 0 {
		t.Fatal("hub_slug fixture carries no cases")
	}
	for _, c := range fixture.Cases {
		got := routingkey.HubSlug(c.Name)
		if got != c.Slug {
			t.Errorf("HubSlug(%q) = %q, want %q", c.Name, got, c.Slug)
		}
	}
}
