// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	hapublisher "github.com/SukramJ/go-hamqtt/publisher"
)

// TestCommandFiltersMatchTheRoutesReallyRegistered is the anti-drift pin on
// [commandFilters], which is now load-bearing at runtime: it arms
// [hapublisher.StateConfig.CommandFilters] and
// [hapublisher.AvailabilityConfig.CommandFilters], and a filter missing from
// it is a hole in the guard rather than a cosmetic difference.
//
// The comparison is against a real [CommandSubscriber.Start] against a
// recording subscriber, so the two cannot agree by both being copies of the
// same literal list.
//
// It compares the two as SETS rather than as sequences, which is what this
// guard was always about: every runtime reader of [commandFilters] asks
// whether some topic is matched by one of them — [anyFilterMatches] here,
// [hapublisher.StateConfig.CommandFilters] and
// [hapublisher.AvailabilityConfig.CommandFilters] in the publishers — and a
// membership test cannot observe order. The orders genuinely differ since
// go-hamqtt v0.30.0: [commandFilters] reports [CommandSubscriber.routes] as
// declared, while Start subscribes most specific first. Requiring the
// sequences to be equal would make this test fail on a library-side sort
// that cannot reach the property it guards, and would push the declaration
// list into duplicating a sort the router already owns. What must not drift
// is the MEMBERSHIP, because a filter missing from it is a hole the state
// plane can publish through; that is what is asserted, including
// multiplicity, so a duplicate cannot hide a missing entry.
func TestCommandFiltersMatchTheRoutesReallyRegistered(t *testing.T) {
	t.Parallel()

	const base = "gh"
	rec := newFilterRecorder()
	sub := NewCommandSubscriber(rec, NewTopicBuilder(base), &fakeSink{}, nil)
	if err := sub.Start(context.Background()); err != nil {
		t.Fatalf("command subscriber start: %v", err)
	}
	t.Cleanup(sub.Close)

	registered := rec.recorded()
	if len(registered) == 0 {
		t.Fatal("no filters registered — the comparison would be vacuous")
	}
	got := commandFilters(base)
	gotSorted, regSorted := slices.Clone(got), slices.Clone(registered)
	slices.Sort(gotSorted)
	slices.Sort(regSorted)
	if !reflect.DeepEqual(gotSorted, regSorted) {
		t.Fatalf("commandFilters(%q) = %v,\nthe subscriber registered  %v — the runtime guard "+
			"is armed with a filter set the daemon does not actually subscribe, so a state "+
			"publish into the difference echoes straight back into a command handler",
			base, got, registered)
	}
}

// TestTheStateRuntimeGuardRefusesACommandTopic is the positive control on the
// runtime half of the state-vs-command disjointness invariant.
//
// Before this, StateConfig.CommandFilters was unset and the invariant lived
// entirely in TestEveryStatePlaneIsDisjointFromCommandSubscriptions — a sweep
// over the shapes the planes are known to produce. A shape no runner drives
// would have gone out unnoticed. With the filters wired, the publisher itself
// refuses it.
//
// The topic is the one this daemon really shipped the defect on: program
// state mirrored onto the program's own `…/trigger` topic, which ran the
// program on the CCU on every boot and every hub republish.
func TestTheStateRuntimeGuardRefusesACommandTopic(t *testing.T) {
	t.Parallel()

	const base = "gh"
	b := NewBridge(BridgeConfig{Base: base, CentralName: "ccu-01", RawEnabled: true}, newFanoutClient())

	collide := base + "/ccu-01/hub/programs/12459/trigger"
	if !anyFilterMatches(commandFilters(base), collide) {
		t.Fatalf("%q is not matched by any command filter, so it cannot exercise the guard", collide)
	}

	_, err := b.state.Publish(context.Background(), collide, []byte(`{"value":true}`))
	if !errors.Is(err, hapublisher.ErrStateCommandCollision) {
		t.Fatalf("publishing state onto a command topic returned %v, want ErrStateCommandCollision — "+
			"the runtime guard is not armed, so the broker delivers this write straight back into "+
			"the daemon's own command handler and it runs the program on the CCU", err)
	}

	// And a topic no command filter claims must still go out, or the guard
	// would refuse the whole plane rather than the collisions in it.
	if _, err := b.state.Publish(context.Background(), base+"/bridge/health", []byte(`{}`)); err != nil {
		t.Fatalf("publishing the bridge health topic was refused: %v", err)
	}
}

// TestTheAvailabilityRuntimeGuardIsArmed is the same control on the
// availability publisher, whose retraction path is where a collision would be
// least likely to be noticed.
func TestTheAvailabilityRuntimeGuardIsArmed(t *testing.T) {
	t.Parallel()

	const base = "gh"
	b := NewBridge(BridgeConfig{Base: base, CentralName: "ccu-01", RawEnabled: true}, newFanoutClient())
	collide := base + "/system/addon_update/set"
	if !anyFilterMatches(commandFilters(base), collide) {
		t.Fatalf("%q is not matched by any command filter, so it cannot exercise the guard", collide)
	}
	if _, err := b.avail.Publish(context.Background(), collide, true); !errors.Is(err, hapublisher.ErrStateCommandCollision) {
		t.Fatalf("publishing availability onto a command topic returned %v, want "+
			"ErrStateCommandCollision", err)
	}
}

// anyFilterMatches reports whether topic is claimed by one of filters, using
// this package's own matcher.
func anyFilterMatches(filters []string, topic string) bool {
	for _, f := range filters {
		if topicMatchesFilter(topic, f) {
			return true
		}
	}
	return false
}
