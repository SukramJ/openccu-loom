// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
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

// With delay off (default), NewDevices ingests immediately and a
// DeviceCreatedEvent fires; with delay on, ingest is deferred and no
// creation event fires.
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

			got := created.Load() > 0
			if got != tc.wantEvent {
				t.Fatalf("DeviceCreatedEvent fired=%v, want %v (delay=%v)", got, tc.wantEvent, tc.delay)
			}
		})
	}
}
