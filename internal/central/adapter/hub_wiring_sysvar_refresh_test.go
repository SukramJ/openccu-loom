// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// seedSysvars registers one sysvar per name on h.
func seedSysvars(h *hub.Hub, names ...string) {
	for _, name := range names {
		h.PutSysvar(hub.NewSysvar(h.CentralName, name, "", hmenum.HubValueTypeFloat, nil))
	}
}

// TestPruneRemovedSysvarsDropsOnlyTheAbsentOnes pins the sweep itself:
// a variable the CCU still reports survives the periodic refresh, one
// it no longer reports is dropped.
func TestPruneRemovedSysvarsDropsOnlyTheAbsentOnes(t *testing.T) {
	t.Parallel()

	h := hub.NewHub("ccu-01")
	seedSysvars(h, "Anwesenheit", "Urlaub")

	pruneRemovedSysvars(h, map[string]struct{}{"Anwesenheit": {}})

	if _, ok := h.Sysvar("Anwesenheit"); !ok {
		t.Error("a variable the CCU still reports was removed")
	}
	if _, ok := h.Sysvar("Urlaub"); ok {
		t.Error("a variable the CCU no longer reports survived the sweep")
	}
}

// TestPruneRemovedSysvarsReadsTheNameUnderTheDataPointLock runs the
// sweep against a concurrent operator rename — the two goroutines that
// genuinely meet here, the hub refresh job and a REST/SPA rename.
//
// The name lives on the data point and is written under its own lock,
// so the sweep has to read it through the accessor. Reading the field
// directly is a data race whose consequence is not a lost update but a
// destructive one: the sweep would remove a name nobody asked it to
// remove and retract that variable's retained discovery.
func TestPruneRemovedSysvarsReadsTheNameUnderTheDataPointLock(t *testing.T) {
	t.Parallel()

	h := hub.NewHub("ccu-01")
	seedSysvars(h, "Anwesenheit", "Urlaub")
	// Both spellings count as present, so whichever name the rename has
	// installed when the sweep looks, nothing is expected to be dropped.
	fresh := map[string]struct{}{
		"Anwesenheit": {}, "Urlaub": {}, "Ferien": {},
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := range 200 {
			if i%2 == 0 {
				h.RenameSysvar("Urlaub", "Ferien")
			} else {
				h.RenameSysvar("Ferien", "Urlaub")
			}
		}
	}()
	for range 200 {
		pruneRemovedSysvars(h, fresh)
	}
	wg.Wait()

	if _, ok := h.Sysvar("Anwesenheit"); !ok {
		t.Error("the untouched variable was swept away")
	}
	_, asUrlaub := h.Sysvar("Urlaub")
	_, asFerien := h.Sysvar("Ferien")
	if !asUrlaub && !asFerien {
		t.Error("the renamed variable was swept away under one of its two names")
	}
}
