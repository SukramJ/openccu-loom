// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/internal/store/visibility"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// visibilityAdapter bundles the daemon-side wiring that the visibility
// REST handlers (`/visibility/unignore*`) depend on. It implements the
// three narrow interfaces exposed by `internal/north/rest/handlers`:
// VisibilityCentralLister, VisibilityCandidateProvider, and
// VisibilityRegistryLoader. The single underlying [*visibility.Registry]
// is shared across centrals — un-ignore rules are global in the
// existing backend (one decider, one rule set). Per-central SQLite rows
// are unioned at apply time so the table partitioning still reflects
// "which central asked for this pattern" while the runtime sees one
// merged list.
type visibilityAdapter struct {
	registry        *visibility.Registry
	registryStore   *sqlite.VisibilityUnIgnoreStore
	centralRegistry *central.Registry
}

// newVisibilityAdapter wires the adapter against the live registry and
// SQLite store. Either argument may be nil — in that case the relevant
// REST endpoint will degrade to 503 service_unready.
func newVisibilityAdapter(
	reg *visibility.Registry,
	store *sqlite.VisibilityUnIgnoreStore,
	centralReg *central.Registry,
) *visibilityAdapter {
	return &visibilityAdapter{
		registry:        reg,
		registryStore:   store,
		centralRegistry: centralReg,
	}
}

// Names implements handlers.VisibilityCentralLister.
func (v *visibilityAdapter) Names() []string {
	if v == nil || v.centralRegistry == nil {
		return nil
	}
	return v.centralRegistry.Names()
}

// UnIgnoreCandidates implements handlers.VisibilityCandidateProvider.
// The visibility candidate list is computed per central by walking the
// central's model registry. Returns parameter names sorted in the order
// that the per-central QueryFacade reports them.
func (v *visibilityAdapter) UnIgnoreCandidates(centralName string, paramset hmenum.ParamsetKey) []string {
	if v == nil || v.centralRegistry == nil {
		return nil
	}
	unit, ok := v.centralRegistry.Get(centralName)
	if !ok {
		return nil
	}
	q := unit.QueryFacade()
	if q == nil {
		return nil
	}
	return q.GetUnIgnoreCandidates(paramset)
}

// UnIgnoreCandidateGroups implements
// handlers.VisibilityCandidateGroupProvider — the grouped counterpart to
// [visibilityAdapter.UnIgnoreCandidates], walking the central's model
// once for all requested paramsets.
func (v *visibilityAdapter) UnIgnoreCandidateGroups(centralName string, paramsets []hmenum.ParamsetKey) []visibility.CandidateGroup {
	if v == nil || v.centralRegistry == nil {
		return nil
	}
	unit, ok := v.centralRegistry.Get(centralName)
	if !ok {
		return nil
	}
	q := unit.QueryFacade()
	if q == nil {
		return nil
	}
	return q.GetUnIgnoreCandidateGroups(paramsets...)
}

// LoadUnIgnore implements handlers.VisibilityRegistryLoader. It writes
// `patterns` for `centralName` into the shared registry as part of the
// union of every central's persisted set, then re-runs the
// suppression-mark pass on every device belonging to the named central.
// The result `affectedDevices` is the count of devices touched.
//
// Pre-condition: the caller must have already persisted `patterns` to
// SQLite via VisibilityUnIgnoreStore.Replace — LoadUnIgnore reads back
// every central's persisted set to compute the union and does NOT
// merge `patterns` ad-hoc.
func (v *visibilityAdapter) LoadUnIgnore(centralName string, _ []string) (affectedDevices int, parseErrors []string, err error) {
	if v == nil || v.registry == nil || v.registryStore == nil || v.centralRegistry == nil {
		return 0, nil, errors.New("visibility adapter not wired")
	}
	// Build the union of every central's persisted patterns. SQLite
	// is the source of truth — REST PUT writes there first, then this
	// loader reads back to recompute the registry-wide view.
	var union []string
	seen := make(map[string]struct{})
	for _, name := range v.centralRegistry.Names() {
		patterns, err := v.registryStore.Patterns(context.Background(), name)
		if err != nil {
			return 0, nil, fmt.Errorf("read patterns for %s: %w", name, err)
		}
		for _, p := range patterns {
			if _, dup := seen[p]; dup {
				continue
			}
			seen[p] = struct{}{}
			union = append(union, p)
		}
	}
	// Apply the union to the shared registry. A parse error here is
	// surfaced as a soft parseErrors entry — the persisted rows are
	// already written, and the operator should see the message rather
	// than receive a 500. Hence the explicit (nil, nil) error return
	// alongside the populated parseErrors slice.
	if loadErr := v.registry.LoadUnIgnore(strings.NewReader(strings.Join(union, "\n"))); loadErr != nil {
		parseErrors = []string{loadErr.Error()}
		return 0, parseErrors, nil //nolint:nilerr // surfaced as parse error, not 500
	}

	// Re-run the suppression-mark pass on every device of the
	// named central so the per-DP IsUnIgnored bit reflects the new
	// rules. Other centrals share the same registry; if patterns
	// promoted by central A also un-cover parameters on central B,
	// the next pipeline cycle (Init or Reassemble) picks them up —
	// callers that need an immediate fan-out can trigger a
	// per-central re-mark themselves.
	unit, ok := v.centralRegistry.Get(centralName)
	if !ok {
		return 0, nil, fmt.Errorf("central %q not registered", centralName)
	}
	decider := v.registry.Parameter()
	count := 0
	if unit.ModelRegistry != nil {
		for _, d := range unit.ModelRegistry.List() {
			visibility.ApplyUnIgnoredMarks(d, decider)
			count++
		}
	}
	return count, nil, nil
}

// Compile-time interface checks.
var (
	_ handlers.VisibilityCentralLister          = (*visibilityAdapter)(nil)
	_ handlers.VisibilityCandidateProvider      = (*visibilityAdapter)(nil)
	_ handlers.VisibilityCandidateGroupProvider = (*visibilityAdapter)(nil)
	_ handlers.VisibilityRegistryLoader         = (*visibilityAdapter)(nil)
)
