// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"testing"
)

func TestReloadDeps_MQTTReseed_RoundTrips(t *testing.T) {
	t.Parallel()
	d := newReloadDeps()
	if d.MQTTReseed() != nil {
		t.Fatal("MQTTReseed must be nil before any SetMQTTReseed")
	}

	var calls int
	d.SetMQTTReseed(func(context.Context) { calls++ })
	fn := d.MQTTReseed()
	if fn == nil {
		t.Fatal("MQTTReseed must return the installed hook")
	}
	fn(context.Background())
	if calls != 1 {
		t.Fatalf("installed hook called %d times, want 1", calls)
	}

	// A nil fn clears the slot.
	d.SetMQTTReseed(nil)
	if d.MQTTReseed() != nil {
		t.Fatal("SetMQTTReseed(nil) must clear the hook")
	}
}

func TestReloadDeps_MQTTReseed_NilReceiver(t *testing.T) {
	t.Parallel()
	var d *reloadDeps
	// Nil-receiver paths must not panic and must report no hook.
	d.SetMQTTReseed(func(context.Context) {})
	if d.MQTTReseed() != nil {
		t.Fatal("nil reloadDeps must report no reseed hook")
	}
}

func TestReloadDeps_MDNSTXTRefresh_RoundTrips(t *testing.T) {
	t.Parallel()
	d := newReloadDeps()
	// No advertiser bound yet: the hub-ready pipeline fires anyway and
	// must find nothing to do.
	d.RefreshMDNSTXT()

	var calls int
	d.SetMDNSTXTRefresh(func() { calls++ })
	d.RefreshMDNSTXT()
	if calls != 1 {
		t.Fatalf("installed hook called %d times, want 1", calls)
	}

	// Advertiser teardown clears the slot; later ready events are no-ops.
	d.SetMDNSTXTRefresh(nil)
	d.RefreshMDNSTXT()
	if calls != 1 {
		t.Fatalf("hook called %d times after the slot was cleared, want 1", calls)
	}
}

// TestReloadDeps_MDNSTXTRefresh_SurvivesNilBagAsMethodValue pins the
// exact shape the composition root uses: it hands `deps.RefreshMDNSTXT`
// to the southbound wiring as the post-hub-ready hook while deps may be
// nil, and the hook then runs on every central's southbound-ready event.
//
// Taking a method value off a nil pointer is legal Go — the receiver is
// copied, not dereferenced — and the nil guard inside the method makes
// the call total. Reaching into the bag's fields from a closure instead
// is what panicked every config-file-less installation for eight
// releases, silently dropping the ADR 0058 serial re-announce.
func TestReloadDeps_MDNSTXTRefresh_SurvivesNilBagAsMethodValue(t *testing.T) {
	t.Parallel()
	var d *reloadDeps
	// Binding the method value must not dereference the receiver, and
	// calling it must not either. Either one panics the boot path.
	postHubReady := d.RefreshMDNSTXT
	postHubReady()

	d.SetMDNSTXTRefresh(func() { t.Error("a nil bag must not retain a hook") })
	postHubReady()
}

func TestReloadDeps_NotifyNorthBridges_NilReceiver(t *testing.T) {
	t.Parallel()
	var d *reloadDeps
	d.NotifyNorthBridges(nil)

	// A bag with no hook installed is the production case.
	newReloadDeps().NotifyNorthBridges(nil)
}
