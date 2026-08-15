// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package cachereset_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/central/cachereset"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// bareTopology reports interfaces the way the daemon's config-backed topology
// does: bare names, without the central-name prefix the persisted rows carry.
type bareTopology struct {
	centrals   []string
	interfaces map[string][]string
}

func (t bareTopology) Centrals() []string                 { return t.centrals }
func (t bareTopology) Interfaces(central string) []string { return t.interfaces[central] }

// openStores opens a migrated database and returns the two row-counting
// stores a scoped clear has to empty.
func openStores(t *testing.T) (*sqlite.ValuesCacheStore, *sqlite.MasterValuesStore) {
	t.Helper()
	dsn := sqlite.FileDSN(filepath.Join(t.TempDir(), "cachereset.db"))
	db, err := sqlite.Open(context.Background(), dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return sqlite.NewValuesCacheStore(db), sqlite.NewMasterValuesStore(db)
}

// TestStoreInterfaceIDMatchesWireID pins the identifier this package composes
// against the canonical one the device pipeline stamps onto every device (and
// therefore onto every persisted cache row). The two are separate
// implementations only because the cache-reset service must stay free of the
// south-bound adapter package; if they ever disagree, a scoped clear silently
// deletes nothing again.
func TestStoreInterfaceIDMatchesWireID(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ central, iface string }{
		{"ccu", "HmIP-RF"},
		{"ccu1", "BidCos-RF"},
		{"", "CUxD"},
		// Two interface tokens carry a hyphen themselves, so a central
		// named after a radio makes the bare name look like it already
		// carries the prefix. `HmIP-RF` under a central called `HmIP`
		// must still be prefixed — the persisted rows are keyed
		// `HmIP-HmIP-RF`.
		{"HmIP", "HmIP-RF"},
		{"BidCos", "BidCos-RF"},
		{"BidCos", "BidCos-Wired"},
	} {
		got := cachereset.StoreInterfaceID(tc.central, tc.iface)
		want := adapter.WireInterfaceID(tc.central, hmenum.Interface(tc.iface))
		if got != want {
			t.Errorf("StoreInterfaceID(%q, %q) = %q, want %q", tc.central, tc.iface, got, want)
		}
	}
}

// TestStoreInterfaceIDIsIdempotent verifies a caller that already passes the
// canonical id (the REST/WS surface documents "interface id", and hmcli users
// copy it out of a topic) is not double-prefixed.
func TestStoreInterfaceIDIsIdempotent(t *testing.T) {
	t.Parallel()
	if got := cachereset.StoreInterfaceID("ccu", "ccu-HmIP-RF"); got != "ccu-HmIP-RF" {
		t.Errorf("StoreInterfaceID on an already-canonical id = %q, want %q", got, "ccu-HmIP-RF")
	}
}

// TestClearGlobalScopeRemovesPersistedRows drives Clear against the real
// stores with a bare-name topology — the daemon's production shape. Before the
// units were normalized, every DELETE was an exact match on the bare interface
// name while the rows carried `<central>-<interface>`: the operator's "clear
// caches and re-pull" reported zero rows removed and the re-init re-hydrated
// from exactly the stale rows they asked to discard.
func TestClearGlobalScopeRemovesPersistedRows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	values, master := openStores(t)

	now := time.Now()
	const central, wireIface = "ccu", "ccu-HmIP-RF"
	if err := values.SaveValue(ctx, central, wireIface, "VCU0001:1", "LEVEL", 0.5, now, now); err != nil {
		t.Fatalf("seed values_cache: %v", err)
	}
	if err := master.SaveParameter(ctx, central, wireIface, "VCU0001:1", "TEMP", 21.0); err != nil {
		t.Fatalf("seed master_values: %v", err)
	}

	svc := cachereset.New(cachereset.Deps{
		Values: values,
		Master: master,
		Topology: bareTopology{
			centrals:   []string{central},
			interfaces: map[string][]string{central: {"HmIP-RF"}},
		},
	})

	rep, err := svc.Clear(ctx, cachereset.Scope{Kind: cachereset.ScopeGlobal})
	if err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if rep.Values != 1 {
		t.Errorf("Report.Values = %d, want 1", rep.Values)
	}
	if rep.Master != 1 {
		t.Errorf("Report.Master = %d, want 1", rep.Master)
	}

	rows, err := values.LoadChannel(ctx, central, wireIface, "VCU0001:1")
	if err != nil {
		t.Fatalf("LoadChannel: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("values_cache rows left = %d, want 0", len(rows))
	}
}

// TestClearInterfaceScopeAcceptsBareAndWireIDs pins that a scoped clear works
// whichever spelling the caller uses: the SPA and hmcli pass the bare
// configured name, other clients copy the canonical id off a topic.
func TestClearInterfaceScopeAcceptsBareAndWireIDs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	const central, wireIface = "ccu", "ccu-HmIP-RF"

	for _, supplied := range []string{"HmIP-RF", "ccu-HmIP-RF"} {
		t.Run(supplied, func(t *testing.T) {
			t.Parallel()
			values, master := openStores(t)
			now := time.Now()
			if err := values.SaveValue(ctx, central, wireIface, "VCU0001:1", "LEVEL", 0.5, now, now); err != nil {
				t.Fatalf("seed values_cache: %v", err)
			}
			svc := cachereset.New(cachereset.Deps{Values: values, Master: master})
			rep, err := svc.Clear(ctx, cachereset.Scope{
				Kind: cachereset.ScopeInterface, Central: central, Interface: supplied,
			})
			if err != nil {
				t.Fatalf("Clear: %v", err)
			}
			if rep.Values != 1 {
				t.Errorf("Report.Values = %d, want 1", rep.Values)
			}
		})
	}
}
