// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// countingListDevicesOps wraps [fakeOperations] and counts ListDevices
// invocations, so a test can assert how many full-interface inventory
// fetches a callback handler issued for one incoming callback.
type countingListDevicesOps struct {
	*fakeOperations
	mu    sync.Mutex
	calls int
}

func (f *countingListDevicesOps) ListDevices(ctx context.Context) ([]hmproto.DeviceDescription, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	return f.fakeOperations.ListDevices(ctx)
}

func (f *countingListDevicesOps) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// TestReaddedDeviceRefreshesInventoryOnceRegardlessOfAddressCount verifies
// that a readdedDevice callback carrying several re-paired addresses issues
// exactly one full-interface listDevices refresh, not one per address. The
// refresh call takes only (ctx, fetcher, iface) — it re-pulls the whole
// interface inventory regardless of which address triggered it — so
// repeating it per address multiplies CCU round-trips without changing the
// result, and can exhaust the shared 30 s background budget on a large
// installation.
func TestReaddedDeviceRefreshesInventoryOnceRegardlessOfAddressCount(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-readded9"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	h := NewCallbackHandlers(c, nil)
	defer h.Stop()

	fake := &countingListDevicesOps{fakeOperations: &fakeOperations{kind: backends.KindCCU}}
	w := client.NewValueWriter()
	w.Register(c.Name(), hmtypes.ParseWireInterfaceID("HmIP-RF"), fake)
	h.SetWriter(w)

	if err := h.ReaddedDevice(context.Background(), "HmIP-RF", []string{"DEV001", "DEV002", "DEV003"}); err != nil {
		t.Fatalf("ReaddedDevice: %v", err)
	}

	// Stop() cancels the handler context and waits for the background
	// goroutine ReaddedDevice spawned, so the refresh has run by the time
	// this returns.
	h.Stop()

	if got := fake.callCount(); got != 1 {
		t.Fatalf("ListDevices called %d times for 3 re-paired addresses, want exactly 1", got)
	}
}
