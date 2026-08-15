// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

// Tests for primaryBackendOf — the resolver every CCU-level operation
// (backup, maintenance, heating groups, sysvar creation) routes through.
//
// Strategy: register several client entries on one Unit, back each with a
// fakeOperations of a known Kind, and assert which backend comes back. The
// entries carry production-shaped wire ids (`<central>-<interface>`) because
// the pin comparison has to strip that prefix.

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/coordinators"
	clientpkg "github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// registerBackendInterface wires one interface onto unit: a client entry the
// coordinator can list plus the backend the writer resolves for it.
func registerBackendInterface(
	t *testing.T,
	unit *central.Unit,
	w *clientpkg.ValueWriter,
	iface hmenum.Interface,
	kind backends.Kind,
) *fakeOperations {
	t.Helper()
	wireID := hmtypes.NewWireInterfaceID(unit.Name(), iface)
	ic := newTestInterfaceClient(t, unit.Name(), string(iface), 5)
	if err := unit.Clients.Register(&coordinators.ClientEntry{
		InterfaceID: wireID.String(),
		Interface:   iface,
		Client:      ic,
	}); err != nil {
		t.Fatalf("Clients.Register(%s): %v", iface, err)
	}
	ops := &fakeOperations{kind: kind}
	w.Register(unit.Name(), wireID, ops)
	return ops
}

// TestPrimaryBackendOf_PrefersCCUBackendOverCUxD is the regression the
// interface-id sort produced: `CUxD` sorts before `HmIP-RF`, so an HmIP-only
// central that also runs CUxD resolved its BIN-RPC adapter as "primary" for
// every CCU-level operation. CUxD cannot back up the CCU, create a system
// variable, reboot it or edit a heating group — the backup path in particular
// answered with a nil archive and no error.
func TestPrimaryBackendOf_PrefersCCUBackendOverCUxD(t *testing.T) {
	t.Parallel()

	unit := newTestCentralNamed(t, "ccu-01")
	w := clientpkg.NewValueWriter()
	registerBackendInterface(t, unit, w, hmenum.InterfaceCUxD, backends.KindCUxD)
	wantOps := registerBackendInterface(t, unit, w, hmenum.InterfaceHmIPRF, backends.KindCCU)

	_, got, err := primaryBackendOf(unit, w)
	if err != nil {
		t.Fatalf("primaryBackendOf: %v", err)
	}
	if got != backends.Operations(wantOps) {
		t.Fatalf("primaryBackendOf resolved kind %s, want the HmIP-RF CCU backend", got.Kind())
	}
}

// TestPrimaryBackendOf_HonoursPinnedPrimaryInterface verifies that the
// operator's `primary_interface` pin decides between two CCU-class
// interfaces, instead of the interface-id sort silently picking BidCos-RF.
func TestPrimaryBackendOf_HonoursPinnedPrimaryInterface(t *testing.T) {
	t.Parallel()

	unit := newTestCentralNamed(t, "ccu-01")
	w := clientpkg.NewValueWriter()
	registerBackendInterface(t, unit, w, hmenum.InterfaceBidCosRF, backends.KindCCU)
	wantOps := registerBackendInterface(t, unit, w, hmenum.InterfaceHmIPRF, backends.KindCCU)
	unit.Health.SetPrimaryInterface(string(hmenum.InterfaceHmIPRF))

	_, got, err := primaryBackendOf(unit, w)
	if err != nil {
		t.Fatalf("primaryBackendOf: %v", err)
	}
	if got != backends.Operations(wantOps) {
		t.Fatal("primaryBackendOf ignored the pinned primary_interface")
	}
}

// TestPrimaryBackendOf_FallsBackWhenNoCCUInterfaceExists keeps a central that
// runs no CCU-class interface at all reaching its backend: the operation then
// fails with that backend's own unsupported error, which is diagnosable, and
// not with a resolution error that hides which interface was tried.
func TestPrimaryBackendOf_FallsBackWhenNoCCUInterfaceExists(t *testing.T) {
	t.Parallel()

	unit := newTestCentralNamed(t, "ccu-01")
	w := clientpkg.NewValueWriter()
	wantOps := registerBackendInterface(t, unit, w, hmenum.InterfaceCUxD, backends.KindCUxD)

	_, got, err := primaryBackendOf(unit, w)
	if err != nil {
		t.Fatalf("primaryBackendOf: %v", err)
	}
	if got != backends.Operations(wantOps) {
		t.Fatal("primaryBackendOf did not fall back to the only registered backend")
	}
}
