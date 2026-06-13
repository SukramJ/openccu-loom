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
