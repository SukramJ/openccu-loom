// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package matteradapter_test

import (
	"context"
	"log/slog"
	"slices"
	"sort"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matteradapter"
	"github.com/SukramJ/openccu-loom/internal/store/matterendpoint"
)

// denyOne is an allow-list that refuses exactly one source.
type denyOne struct{ key string }

func (d denyOne) IsExposed(_ context.Context, k matterendpoint.SourceKey) (bool, error) {
	return k.String() != d.key, nil
}

// decideFleet runs DecideDevice over the golden fleet and DescribeDevices
// over the same fleet with the same assembler, and returns the decisions
// and the keys of the described endpoints.
func decideFleet(t *testing.T, a *matteradapter.Assembler) ([]matteradapter.SourceDecision, []string) {
	t.Helper()
	ctx := context.Background()
	fleet := goldenFleet(t)
	var decisions []matteradapter.SourceDecision
	for _, dev := range fleet {
		d, err := a.DecideDevice(ctx, goldenCentralName, fleet, dev)
		if err != nil {
			t.Fatalf("DecideDevice(%s): %v", dev.Address, err)
		}
		decisions = append(decisions, d...)
	}
	described, err := a.DescribeDevices(ctx, []matteradapter.DeviceSnapshot{{CentralName: goldenCentralName, Devices: fleet}})
	if err != nil {
		t.Fatalf("DescribeDevices: %v", err)
	}
	var keys []string
	for _, d := range described {
		for i := range d.Endpoints {
			keys = append(keys, d.Endpoints[i].StableKey.String())
		}
	}
	sort.Strings(keys)
	return decisions, keys
}

func publishedKeys(decisions []matteradapter.SourceDecision) []string {
	var out []string
	for _, d := range decisions {
		if d.Outcome == matteradapter.OutcomePublished {
			out = append(out, d.Key.String())
		}
	}
	sort.Strings(out)
	return out
}

// The decisions and the described endpoints come from one pass, and this
// holds them to it: what the walk calls published is exactly what it
// describes, over a fleet that takes every projection path.
func TestPublishedDecisionsAreTheDescribedEndpoints(t *testing.T) {
	a := matteradapter.New(goldenConfig(t), slog.New(slog.DiscardHandler))
	decisions, described := decideFleet(t, a)
	if len(described) == 0 {
		t.Fatal("the golden fleet described no endpoint; the comparison would hold vacuously")
	}
	if got := publishedKeys(decisions); !slices.Equal(got, described) {
		t.Fatalf("published decisions and described endpoints differ\n published: %v\n described: %v", got, described)
	}
}

// Every decision states a complete outcome: a withheld one a reason of the
// closed set, a folded one its carrier, a published one neither.
func TestEveryDecisionIsComplete(t *testing.T) {
	a := matteradapter.New(goldenConfig(t), slog.New(slog.DiscardHandler))
	decisions, _ := decideFleet(t, a)
	reasons := matteradapter.WithheldReasons()
	seen := map[matteradapter.SourceOutcome]int{}
	for _, d := range decisions {
		seen[d.Outcome]++
		switch d.Outcome {
		case matteradapter.OutcomeWithheld:
			if !slices.Contains(reasons, d.Reason) {
				t.Errorf("%s: withheld for %q, which is not in the closed set", d.Key, d.Reason)
			}
		case matteradapter.OutcomeFolded:
			if d.Into == "" || d.Reason != "" {
				t.Errorf("%s: folded into %q with reason %q", d.Key, d.Into, d.Reason)
			}
		case matteradapter.OutcomePublished:
			if d.Into != "" || d.Reason != "" {
				t.Errorf("%s: published with into %q, reason %q", d.Key, d.Into, d.Reason)
			}
		default:
			t.Errorf("%s: outcome %q", d.Key, d.Outcome)
		}
	}
	for _, o := range []matteradapter.SourceOutcome{matteradapter.OutcomePublished, matteradapter.OutcomeFolded, matteradapter.OutcomeWithheld} {
		if seen[o] == 0 {
			t.Errorf("the golden fleet produced no %q decision; this test does not cover it", o)
		}
	}
}

// A source the operator has not switched on is withheld for that reason
// and is not described.
func TestASourceOffTheAllowListIsWithheldForIt(t *testing.T) {
	a := matteradapter.New(goldenConfig(t), slog.New(slog.DiscardHandler))
	_, before := decideFleet(t, a)
	denied := before[0]

	a.SetExposureChecker(denyOne{key: denied})
	decisions, after := decideFleet(t, a)
	if slices.Contains(after, denied) {
		t.Fatalf("%s is still described with the allow-list refusing it", denied)
	}
	found := false
	for _, d := range decisions {
		if d.Key.String() != denied {
			continue
		}
		found = true
		if d.Outcome != matteradapter.OutcomeWithheld || d.Reason != matteradapter.WithheldNotOnAllowList {
			t.Errorf("%s: outcome %q, reason %q; want withheld, %q", denied, d.Outcome, d.Reason, matteradapter.WithheldNotOnAllowList)
		}
	}
	if !found {
		t.Fatalf("no decision for %s", denied)
	}
}
