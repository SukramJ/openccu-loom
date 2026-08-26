// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// deferredAcceptCentral registers one central holding a device in the
// deferred-creation queue and returns it together with a counter the
// installed materialiser increments.
func deferredAcceptCentral(t *testing.T, name string) (*central.Registry, *central.Unit, *atomic.Int32) {
	t.Helper()
	cu, err := central.New(central.Config{Name: name})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(cu); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}
	var ingested atomic.Int32
	cu.SetDeviceIngestFn(func(context.Context, string, []hmproto.DeviceDescription) error {
		ingested.Add(1)
		return nil
	})

	h := NewCallbackHandlers(cu, nil)
	t.Cleanup(h.Stop)
	h.SetDelayNewDeviceCreation(true)
	if err := h.NewDevices(context.Background(), "HmIP-RF", newDeviceDescs()); err != nil {
		t.Fatalf("NewDevices: %v", err)
	}
	return reg, cu, &ingested
}

// TestAcceptInboxDeviceMaterialisesADeferredDeviceWithoutACCUInbox pins the
// path an operator takes for a device only this daemon is holding back: the
// central has no CCU-side inbox accepter wired, and the accept must still
// materialise the parked device instead of failing with "no backend".
func TestAcceptInboxDeviceMaterialisesADeferredDeviceWithoutACCUInbox(t *testing.T) {
	t.Parallel()
	reg, cu, ingested := deferredAcceptCentral(t, "ccu-accept-deferred")

	admin := NewDeviceAdminDomain(reg, nil)
	if err := admin.AcceptInboxDevice(
		context.Background(), "DELAY001", interfaces.AcceptInboxOptions{},
	); err != nil {
		t.Fatalf("AcceptInboxDevice: %v", err)
	}
	if got := ingested.Load(); got != 1 {
		t.Fatalf("materialiser ran %d time(s), want 1 — the accepted device would have no data points", got)
	}
	if pending := cu.Devices.PendingDevices(); len(pending) != 0 {
		t.Fatalf("deferred queue = %+v after the accept, want empty", pending)
	}
	if listed := cu.HubModel.Inbox.List(); len(listed) != 0 {
		t.Fatalf("inbox = %+v after the accept, want empty", listed)
	}
}

// failingInboxAccepter is a CCU-side inbox that rejects every accept, so a
// test can prove the daemon does not quietly materialise a device the CCU
// refused to hand over.
type failingInboxAccepter struct{}

func (failingInboxAccepter) AcceptDeviceInInbox(context.Context, string) error {
	return errors.New("ccu refused the accept")
}

// TestAcceptInboxDeviceKeepsADeferredDevicePendingWhenTheCCURefuses pins that
// a failed CCU-side accept is surfaced and leaves the device parked: the
// operator retries rather than ending up with a device the CCU never released.
func TestAcceptInboxDeviceKeepsADeferredDevicePendingWhenTheCCURefuses(t *testing.T) {
	t.Parallel()
	reg, cu, ingested := deferredAcceptCentral(t, "ccu-accept-refused")
	cu.HubModel.InboxAccepter = failingInboxAccepter{}

	admin := NewDeviceAdminDomain(reg, nil)
	if err := admin.AcceptInboxDevice(
		context.Background(), "DELAY001", interfaces.AcceptInboxOptions{},
	); err == nil {
		t.Fatal("AcceptInboxDevice swallowed the CCU-side failure")
	}
	if got := ingested.Load(); got != 0 {
		t.Fatalf("materialiser ran %d time(s) although the CCU refused the accept", got)
	}
	if pending := cu.Devices.PendingDevices(); len(pending) != 1 {
		t.Fatalf("deferred queue = %+v, want the device still waiting", pending)
	}
}
