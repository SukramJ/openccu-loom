// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hub

import "testing"

// TestAnyReachableIsTheDisjunctionNotTheConjunction is the one case that
// separates the two folds, and it is the case the per-CCU availability gate
// is built on.
//
// One interface down with another up is a per-interface fault — a crashed
// CUxD, an unplugged wired gateway — and the CCU is still on the bus: ReGa
// answers, so sysvar values, program state and the system scores are not
// stale. AllReachable says "not everything is up", which is true and is what
// the per-interface connectivity sensor reports. AnyReachable says "the CCU
// is still there", which is the question the availability gate asks.
func TestAnyReachableIsTheDisjunctionNotTheConjunction(t *testing.T) {
	t.Parallel()
	c := NewConnectivity()
	c.OnState("HmIP-RF", true)
	c.OnState("CUxD", false)

	reachable, observed := c.AnyReachable()
	if !observed {
		t.Fatal("two recorded states and the tracker reports nothing observed")
	}
	if !reachable {
		t.Fatal("AnyReachable is false with one interface up — the gate would grey out " +
			"every hub entity of a working CCU for an unrelated interface fault")
	}
	if all, _ := c.AllReachable(); all {
		t.Fatal("AllReachable is true with one interface down — the two folds are the same " +
			"function, so this test proves nothing")
	}
}

// TestAnyReachableIsFalseWhenEveryInterfaceIsDown is the defect's own shape:
// a CCU that goes away takes every interface process with it.
func TestAnyReachableIsFalseWhenEveryInterfaceIsDown(t *testing.T) {
	t.Parallel()
	c := NewConnectivity()
	c.OnState("HmIP-RF", false)
	c.OnState("BidCos-RF", false)

	reachable, observed := c.AnyReachable()
	if !observed {
		t.Fatal("observed=false after two recorded states")
	}
	if reachable {
		t.Fatal("AnyReachable is true with every interface down")
	}
}

// TestAnyReachableReportsAnUnobservedTracker: the caller has to be able to
// tell "nothing seen yet" from "seen, and nothing is up". Folding the two
// together publishes a retained `offline` for a CCU the daemon has just
// successfully talked to.
func TestAnyReachableReportsAnUnobservedTracker(t *testing.T) {
	t.Parallel()
	reachable, observed := NewConnectivity().AnyReachable()
	if observed {
		t.Fatal("an empty tracker reports observed=true")
	}
	if reachable {
		t.Fatal("an empty tracker reports a reachable interface")
	}
}
