// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build integration

package integration

// Fleet-scale guards for the un-ignore candidate grouping. The flat
// candidate list is the cross-product of parameter × model × channel in
// three pattern formats, so it grows into the thousands while the set
// of distinct parameters stays small. The picker is built on the
// grouped shape; these tests pin that the grouping actually collapses
// the fleet, that both shapes describe the same candidate set, and that
// every candidate carries a reason the UI can render.
//
// All checks are subtests of one parent so the ~400-device ingest runs
// once.

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/internal/store/visibility"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// fleetQueryFacade ingests the full embedded godevccu fleet and returns
// the central's query facade.
func fleetQueryFacade(t *testing.T) *central.QueryFacade {
	t.Helper()

	srv := startMockCCUWithDevices(t, nil) // nil = every embedded model
	xmlClient := newXMLRPCClient(t, srv.URL())
	caller := &xmlrpcBackendCaller{client: xmlClient}
	backend := backends.NewCcuBackend(caller, nil, nil)

	c, err := central.New(central.Config{Name: "ccu-fleet"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	pipeline := adapter.NewDevicePipeline(c)
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := pipeline.IngestFromBackend(ctx, "HmIP-RF", hmenum.InterfaceHmIPRF, backend, nil, nil, logger); err != nil {
		t.Fatalf("IngestFromBackend: %v", err)
	}
	return c.QueryFacade()
}

func TestUnIgnoreCandidateGroupsAgainstTheFleet(t *testing.T) {
	qf := fleetQueryFacade(t)

	flatValues := qf.GetUnIgnoreCandidates(hmenum.ParamsetKeyValues)
	flatMaster := qf.GetUnIgnoreCandidates(hmenum.ParamsetKeyMaster)
	groups := qf.GetUnIgnoreCandidateGroups(hmenum.ParamsetKeyValues, hmenum.ParamsetKeyMaster)

	// The collapse the picker depends on. Concrete numbers on the
	// embedded fleet at the time of writing: ~1857 VALUES + ~943 MASTER
	// flat patterns from 35 + 10 distinct parameters across ~357
	// models. The assertion is a bound rather than an exact count so a
	// new embedded device does not fail the build, but it fails loudly
	// if the grouping degenerates back into a row-per-pattern list.
	t.Run("groups_collapse_the_fleet", func(t *testing.T) {
		flatCount := len(flatValues) + len(flatMaster)
		if flatCount < 500 {
			t.Fatalf("flat candidates = %d; the fleet did not load as expected", flatCount)
		}
		if len(groups) == 0 {
			t.Fatal("no candidate groups")
		}
		if len(groups)*10 > flatCount {
			t.Errorf("groups = %d vs flat = %d; grouping no longer collapses the fleet",
				len(groups), flatCount)
		}
		t.Logf("fleet: %d flat patterns → %d groups", flatCount, len(groups))
	})

	// Anti-drift pin between the two shapes: a pattern offered by one
	// and not the other is a scope the operator can reach in one view
	// and not the other.
	t.Run("groups_cover_every_flat_pattern", func(t *testing.T) {
		fromGroups := make(map[string]struct{})
		for _, g := range groups {
			if g.SimplePattern != "" {
				fromGroups[g.SimplePattern] = struct{}{}
			}
			for _, m := range g.Models {
				if m.WildcardPattern != "" {
					fromGroups[m.WildcardPattern] = struct{}{}
				}
				for _, p := range m.ChannelPatterns {
					fromGroups[p] = struct{}{}
				}
			}
		}
		flat := make(map[string]struct{}, len(flatValues)+len(flatMaster))
		for _, p := range flatValues {
			flat[p] = struct{}{}
		}
		for _, p := range flatMaster {
			flat[p] = struct{}{}
		}

		var missing, extra []string
		for p := range flat {
			if _, ok := fromGroups[p]; !ok {
				missing = append(missing, p)
			}
		}
		for p := range fromGroups {
			if _, ok := flat[p]; !ok {
				extra = append(extra, p)
			}
		}
		if len(missing) > 0 {
			t.Errorf("%d pattern(s) in the flat list are unreachable through groups, e.g. %v",
				len(missing), missing[:min(5, len(missing))])
		}
		if len(extra) > 0 {
			t.Errorf("%d pattern(s) are offered by groups but absent from the flat list, e.g. %v",
				len(extra), extra[:min(5, len(extra))])
		}
	})

	// Drift detector between visibility.Classify and the suppression
	// passes that produce the candidates. The classifier recomputes the
	// reason rather than reading it off the data point — a forced-usage
	// mark records that a parameter is suppressed, never which pass did
	// it — so a new suppression rule without a matching classifier
	// branch shows up here as `unknown` before an operator meets an
	// unlabelled row.
	t.Run("every_candidate_has_a_known_reason", func(t *testing.T) {
		var unknown []string
		for _, g := range groups {
			if g.Reason != visibility.ReasonUnknown {
				continue
			}
			models := make([]string, 0, len(g.Models))
			for _, m := range g.Models {
				models = append(models, m.Model)
			}
			unknown = append(unknown, g.Parameter+" ("+string(g.Paramset)+") on "+
				strings.Join(models[:min(3, len(models))], ", "))
		}
		if len(unknown) > 0 {
			t.Errorf("%d candidate group(s) carry no known suppression reason — "+
				"visibility.Classify has drifted from the mark passes:\n  %s",
				len(unknown), strings.Join(unknown, "\n  "))
		}

		// Log the distribution: it is the fastest way to see, from a CI
		// log alone, whether a rule change moved a whole bucket.
		byPrimary := make(map[visibility.HiddenReason]int)
		soleReason := make(map[visibility.HiddenReason]int)
		for _, g := range groups {
			byPrimary[g.Reason]++
			if len(g.Reasons) == 1 {
				soleReason[g.Reasons[0]]++
			}
		}
		for _, r := range append(visibility.AllHiddenReasons(), visibility.ReasonUnknown) {
			if byPrimary[r] == 0 && soleReason[r] == 0 {
				continue
			}
			t.Logf("reason %-20s primary=%-3d sole=%d", r, byPrimary[r], soleReason[r])
		}
	})

	// Every pattern the picker can tick must be one the save path
	// accepts; otherwise a tick turns into a parse error the operator
	// cannot act on.
	t.Run("every_offered_pattern_parses", func(t *testing.T) {
		checked := 0
		for _, g := range groups {
			for _, pattern := range candidatePatternsOf(g) {
				parsed := visibility.ParseUnIgnoreLine(pattern)
				if parsed.Entry == nil || parsed.Err != "" {
					t.Errorf("ParseUnIgnoreLine(%q) = err %q, want a parsed entry", pattern, parsed.Err)
				}
				checked++
			}
		}
		if checked == 0 {
			t.Fatal("no patterns checked")
		}
		t.Logf("%d offered patterns parse", checked)
	})

	// Drive the same data through the REST handler so the DTO
	// conversion and the multi-central merge run at fleet scale rather
	// than against a two-row fake.
	t.Run("rest_endpoint_serves_the_fleet_grouped", func(t *testing.T) {
		provider := &integrationGroupProvider{central: "ccu-fleet", qf: qf}
		lister := &integrationCentralLister{names: []string{"ccu-fleet"}}
		h := handlers.ListVisibilityUnIgnoreCandidates(lister, provider, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/visibility/unignore/candidates", http.NoBody)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
		var body handlers.UnIgnoreCandidateListDTO
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(body.Groups) == 0 {
			t.Fatal("groups is empty")
		}
		if len(body.Reasons) == 0 {
			t.Error("reason vocabulary is empty")
		}

		var master, values int
		for _, g := range body.Groups {
			switch g.Paramset {
			case string(hmenum.ParamsetKeyMaster):
				master++
			case string(hmenum.ParamsetKeyValues):
				values++
			}
			if len(g.Models) == 0 {
				t.Errorf("group %q has no model scopes", g.Parameter)
			}
			if g.Reason == "" {
				t.Errorf("group %q has an empty reason", g.Parameter)
			}
		}
		// Both paramsets must arrive without `include_master`; the
		// picker offers the paramset as a client-side filter chip.
		if master == 0 {
			t.Error("no MASTER groups in the default response")
		}
		if values == 0 {
			t.Error("no VALUES groups in the default response")
		}
	})
}

// candidatePatternsOf flattens every pattern form a group offers.
func candidatePatternsOf(g visibility.CandidateGroup) []string {
	out := make([]string, 0, 1+len(g.Models))
	if g.SimplePattern != "" {
		out = append(out, g.SimplePattern)
	}
	for _, m := range g.Models {
		if m.WildcardPattern != "" {
			out = append(out, m.WildcardPattern)
		}
		for _, p := range m.ChannelPatterns {
			out = append(out, p)
		}
	}
	return out
}

// integrationGroupProvider satisfies both candidate provider surfaces
// off one query facade, mirroring the production visibilityAdapter.
type integrationGroupProvider struct {
	central string
	qf      *central.QueryFacade
}

func (p *integrationGroupProvider) UnIgnoreCandidates(centralName string, paramset hmenum.ParamsetKey) []string {
	if centralName != p.central || p.qf == nil {
		return nil
	}
	return p.qf.GetUnIgnoreCandidates(paramset)
}

func (p *integrationGroupProvider) UnIgnoreCandidateGroups(centralName string, paramsets []hmenum.ParamsetKey) []visibility.CandidateGroup {
	if centralName != p.central || p.qf == nil {
		return nil
	}
	return p.qf.GetUnIgnoreCandidateGroups(paramsets...)
}
