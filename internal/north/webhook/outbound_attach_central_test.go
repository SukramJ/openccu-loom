// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package webhook

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// TestAttachCentralDeliversForACentralAddedAfterStart pins the entry point the
// live-adopt path uses. Start subscribes by walking the registry exactly once,
// so a CCU adopted at runtime never produced a single webhook POST — not for
// its data points, not for its status changes, not for its incidents — until a
// daemon restart turned it into a boot-time central. Nothing failed and
// nothing logged.
func TestAttachCentralDeliversForACentralAddedAfterStart(t *testing.T) {
	t.Parallel()
	reg := central.NewRegistry()
	ft := &fakeTransport{}
	o := NewOutbound(
		reg, config.NorthWebhook{Enabled: true, URL: "http://hook.test"}, nil,
		WithHTTPClient(&http.Client{Transport: ft}),
		WithBackoff(instantBackoff()),
		WithClock(fixedClock),
	)
	if err := o.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = o.Stop(context.Background()) })

	// The central appears only now — after the boot-time walk.
	late := makeCentral(t, "late")
	if err := reg.Register(late); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}

	events.Publish(late.EventBus, datapointEvent("late-HmIP-RF", "ABC:1", "STATE",
		hmtypes.BoolValue(true), hmtypes.NoneValue()))
	time.Sleep(100 * time.Millisecond)
	if n := ft.count(); n != 0 {
		t.Fatalf("deliveries before AttachCentral = %d, want 0 (the assertion below would be vacuous)", n)
	}

	detach := o.AttachCentral(late)
	if detach == nil {
		t.Fatal("AttachCentral returned a nil detach for a running bridge and an allowed central")
	}

	events.Publish(late.EventBus, datapointEvent("late-HmIP-RF", "ABC:1", "STATE",
		hmtypes.BoolValue(false), hmtypes.NoneValue()))
	waitForCount(t, ft, 1, 2*time.Second)

	// Detach must stop delivery again, or a removed CCU keeps POSTing.
	detach()
	before := ft.count()
	events.Publish(late.EventBus, datapointEvent("late-HmIP-RF", "ABC:1", "STATE",
		hmtypes.BoolValue(true), hmtypes.NoneValue()))
	time.Sleep(100 * time.Millisecond)
	if after := ft.count(); after != before {
		t.Errorf("deliveries after detach = %d, want %d", after, before)
	}
}

// TestAttachCentralRespectsTheCentralAllowList verifies the runtime-attach path
// applies the same `north.webhook.centrals` filter the boot-time walk does — a
// CCU the operator excluded must stay excluded however it joined.
func TestAttachCentralRespectsTheCentralAllowList(t *testing.T) {
	t.Parallel()
	reg := central.NewRegistry()
	o := NewOutbound(
		reg,
		config.NorthWebhook{Enabled: true, URL: "http://hook.test", Centrals: []string{"wanted"}},
		nil,
		WithHTTPClient(&http.Client{Transport: &fakeTransport{}}),
		WithBackoff(instantBackoff()),
		WithClock(fixedClock),
	)
	if err := o.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = o.Stop(context.Background()) })

	if detach := o.AttachCentral(makeCentral(t, "unwanted")); detach != nil {
		t.Error("AttachCentral subscribed a central outside the allow-list")
	}
	if detach := o.AttachCentral(makeCentral(t, "wanted")); detach == nil {
		t.Error("AttachCentral skipped an allow-listed central")
	}
}

// TestAttachCentralBeforeStartIsANoop pins that the hook cannot subscribe onto
// a bridge whose delivery worker is not running: the queue is nil until Start,
// so a subscription made earlier would drop every event it handled.
func TestAttachCentralBeforeStartIsANoop(t *testing.T) {
	t.Parallel()
	o := NewOutbound(
		central.NewRegistry(),
		config.NorthWebhook{Enabled: true, URL: "http://hook.test"}, nil,
	)
	if detach := o.AttachCentral(makeCentral(t, "early")); detach != nil {
		t.Error("AttachCentral subscribed before Start")
	}
}
