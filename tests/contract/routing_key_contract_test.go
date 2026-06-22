// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

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
