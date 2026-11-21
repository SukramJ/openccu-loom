// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// TestFirmwareUpdateStatusChangedRefreshesInventoryOncePerBurst pins the
// handling of the HmIP server's firmwareUpdateStatusChanged callback. The
// server announces a finished background firmware delivery by calling it
// once per device of the batch, back to back. Each call must succeed — a
// fault counts toward the server's eviction of the callback client — and
// the burst must collapse into a single description refresh of the
// interface, since one refresh re-pulls every device's firmware state.
func TestFirmwareUpdateStatusChangedRefreshesInventoryOncePerBurst(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-fwstatus"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	h := NewCallbackHandlers(c, nil)
	defer h.Stop()
	h.firmwareStatusDebounce = 50 * time.Millisecond

	fake := &countingListDevicesOps{fakeOperations: &fakeOperations{kind: backends.KindCCU}}
	w := client.NewValueWriter()
	w.Register(c.Name(), hmtypes.ParseWireInterfaceID("HmIP-RF"), fake)
	h.SetWriter(w)

	for _, addr := range []string{"DEV001", "DEV002", "DEV003", "DEV004"} {
		if err := h.FirmwareUpdateStatusChanged(context.Background(), "HmIP-RF", addr, 1); err != nil {
			t.Fatalf("FirmwareUpdateStatusChanged(%s): %v", addr, err)
		}
	}

	deadline := time.Now().Add(5 * time.Second)
	for fake.callCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := fake.callCount(); got != 1 {
		t.Fatalf("ListDevices called %d times after a burst of 4 notifications, want 1", got)
	}

	// A later notification, after the burst was served, schedules a new
	// refresh rather than being swallowed.
	if err := h.FirmwareUpdateStatusChanged(context.Background(), "HmIP-RF", "DEV005", 1); err != nil {
		t.Fatalf("FirmwareUpdateStatusChanged(DEV005): %v", err)
	}
	deadline = time.Now().Add(5 * time.Second)
	for fake.callCount() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := fake.callCount(); got != 2 {
		t.Fatalf("ListDevices called %d times after a second notification, want 2", got)
	}
}
