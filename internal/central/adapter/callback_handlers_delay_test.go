// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

func newDeviceDescs() xmlrpc.ArrayValue {
	return xmlrpc.ArrayValue{
		xmlrpc.StructValue{
			Members: []xmlrpc.Member{
				{Name: "ADDRESS", Value: xmlrpc.StringValue("DELAY001")},
				{Name: "TYPE", Value: xmlrpc.StringValue("HmIP-STH")},
			},
		},
	}
}

// With delay off (default), NewDevices ingests in the background and a
// DeviceCreatedEvent fires once the ingest goroutine has run; with
// delay on, ingest is deferred and no creation event fires. Stop()
// drains the background goroutine, so after it returns the event has
// either fired or never will.
func TestCallbackHandlersDelayNewDeviceCreation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		delay     bool
		wantEvent bool
	}{
		{"immediate", false, true},
		{"deferred", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c, err := central.New(central.Config{Name: "ccu-delay"})
			if err != nil {
				t.Fatalf("central.New: %v", err)
			}
			var created atomic.Int32
			unsub := events.Subscribe(c.EventBus, func(hmevent.DeviceCreatedEvent) {
				created.Add(1)
			})
			defer unsub()

			h := NewCallbackHandlers(c, nil)
			h.SetDelayNewDeviceCreation(tc.delay)
			if err := h.NewDevices(context.Background(), "HmIP-RF", newDeviceDescs()); err != nil {
				t.Fatalf("NewDevices: %v", err)
			}
			h.Stop()

			got := created.Load() > 0
			if got != tc.wantEvent {
				t.Fatalf("DeviceCreatedEvent fired=%v, want %v (delay=%v)", got, tc.wantEvent, tc.delay)
			}
		})
	}
}

// TestNewDevicesLeavesTheInboxEmptyWithoutDeferredCreation pins that the
// pending-accept inbox is only filled when the operator can ever empty it.
//
// The queue is drained by the accept flow alone, which is the
// delay_new_device_creation path. With the default (delay off) the
// descriptions go straight to the hot-plug ingestor, so storing them as well
// only accumulated: the daemon answers listDevices with an empty array, so
// the CCU re-announces its complete inventory after every reconnect and the
// inbox kept every copy for the lifetime of the process.
func TestNewDevicesLeavesTheInboxEmptyWithoutDeferredCreation(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-no-delay-inbox"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}

	h := NewCallbackHandlers(c, nil)
	h.SetDelayNewDeviceCreation(false)
	if err := h.NewDevices(context.Background(), "HmIP-RF", newDeviceDescs()); err != nil {
		t.Fatalf("NewDevices: %v", err)
	}
	h.Stop()

	if pending := c.Devices.PendingDevices(); len(pending) != 0 {
		t.Fatalf("the deferred-creation queue holds %d entry/entries although deferred "+
			"creation is off — the inbox is written but nothing ever drains it", len(pending))
	}
}

// TestDeferredDeviceIsAnnouncedOnTheInboxAndMaterialisedOnAccept is the
// round trip the delay_new_device_creation contract promises: a device
// announced while deferred creation is on shows up on the operator's
// inbox surface, and accepting it hands the parked descriptions to the
// same materialiser the immediate path uses.
//
// Before the accept path existed, the descriptions were parked with no
// surface that listed them and no caller that drained them: the device
// stayed invisible until the daemon was restarted.
func TestDeferredDeviceIsAnnouncedOnTheInboxAndMaterialisedOnAccept(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-deferred-accept"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	var ingested atomic.Int32
	c.SetDeviceIngestFn(func(_ context.Context, _ string, descs []hmproto.DeviceDescription) error {
		if len(descs) == 0 {
			t.Error("materialiser received no descriptions")
		}
		ingested.Add(1)
		return nil
	})

	h := NewCallbackHandlers(c, nil)
	defer h.Stop()
	h.SetDelayNewDeviceCreation(true)
	if err := h.NewDevices(context.Background(), "HmIP-RF", newDeviceDescs()); err != nil {
		t.Fatalf("NewDevices: %v", err)
	}

	listed := c.HubModel.Inbox.List()
	if len(listed) != 1 || listed[0].Address != "DELAY001" {
		t.Fatalf("inbox = %+v, want the deferred device DELAY001 — nothing tells the operator it is waiting", listed)
	}
	if !listed[0].PendingCreation {
		t.Error("inbox entry is not marked as pending creation, so the SPA cannot tell it apart from a CCU inbox entry")
	}

	var created atomic.Int32
	var source hmenum.SourceOfDeviceCreation
	unsub := events.Subscribe(c.EventBus, func(e hmevent.DeviceCreatedEvent) {
		created.Add(1)
		source = e.Source
	})
	defer unsub()

	accepted, err := AcceptPendingDevice(context.Background(), c, "DELAY001")
	if err != nil {
		t.Fatalf("AcceptPendingDevice: %v", err)
	}
	if !accepted {
		t.Fatal("AcceptPendingDevice reported nothing to accept although the device is parked")
	}
	if got := ingested.Load(); got != 1 {
		t.Fatalf("materialiser ran %d time(s), want 1 — the accepted device has no data points without it", got)
	}
	if got := created.Load(); got != 1 {
		t.Fatalf("DeviceCreatedEvent fired %d time(s), want 1", got)
	}
	if source != hmenum.SourceOfDeviceCreationManual {
		t.Errorf("creation source = %v, want MANUAL", source)
	}
	if listed := c.HubModel.Inbox.List(); len(listed) != 0 {
		t.Fatalf("inbox still lists %+v after the accept", listed)
	}
}

// TestAcceptPendingDeviceKeepsTheEntryWhenMaterialisationFails pins that a
// failed accept is retryable: the descriptions go back into the queue and
// the device stays on the inbox surface instead of vanishing unmaterialised.
func TestAcceptPendingDeviceKeepsTheEntryWhenMaterialisationFails(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-deferred-retry"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	c.SetDeviceIngestFn(func(context.Context, string, []hmproto.DeviceDescription) error {
		return errors.New("simulated materialisation failure")
	})

	h := NewCallbackHandlers(c, nil)
	defer h.Stop()
	h.SetDelayNewDeviceCreation(true)
	if err := h.NewDevices(context.Background(), "HmIP-RF", newDeviceDescs()); err != nil {
		t.Fatalf("NewDevices: %v", err)
	}

	if _, err := AcceptPendingDevice(context.Background(), c, "DELAY001"); err == nil {
		t.Fatal("AcceptPendingDevice swallowed the materialisation failure")
	}
	if pending := c.Devices.PendingDevices(); len(pending) != 1 {
		t.Fatalf("deferred queue = %+v, want the device back for a retry", pending)
	}
	if listed := c.HubModel.Inbox.List(); len(listed) != 1 {
		t.Fatalf("inbox = %+v, want the device still listed", listed)
	}
}
