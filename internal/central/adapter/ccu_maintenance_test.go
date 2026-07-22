// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/coordinators"
	clientpkg "github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// rebootOps is a backends.Operations that also implements the ccuRebooter
// capability. It records the reboot call and returns a configurable error.
type rebootOps struct {
	fakeOperations

	rebootCalls int
	rebootErr   error
}

func (r *rebootOps) RebootCCU(_ context.Context) (bool, error) {
	r.rebootCalls++
	if r.rebootErr != nil {
		return false, r.rebootErr
	}
	return true, nil
}

// buildCCUMaintenanceFixture wires a central named centralName with the given
// backend registered as its primary interface, and returns the domain.
func buildCCUMaintenanceFixture(t *testing.T, centralName string, ops backends.Operations) *CCUMaintenanceDomain {
	t.Helper()
	c, err := central.New(central.Config{Name: centralName})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	w := clientpkg.NewValueWriter()
	w.Register(centralName, "HmIP-RF", ops)
	ic := newTestInterfaceClient(t, centralName, "HmIP-RF", 5)
	if err := c.Clients.Register(&coordinators.ClientEntry{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Client:      ic,
	}); err != nil {
		t.Fatalf("Clients.Register: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}
	return NewCCUMaintenanceDomain(reg, w)
}

func TestCCUMaintenanceRebootCCUSuccess(t *testing.T) {
	t.Parallel()
	ops := &rebootOps{fakeOperations: fakeOperations{kind: backends.KindCCU}}
	dom := buildCCUMaintenanceFixture(t, "ccu-01", ops)
	if err := dom.RebootCCU(context.Background(), "ccu-01"); err != nil {
		t.Fatalf("RebootCCU: %v", err)
	}
	if ops.rebootCalls != 1 {
		t.Fatalf("expected 1 reboot call, got %d", ops.rebootCalls)
	}
}

func TestCCUMaintenanceRebootCCUUnknownCentral(t *testing.T) {
	t.Parallel()
	ops := &rebootOps{fakeOperations: fakeOperations{kind: backends.KindCCU}}
	dom := buildCCUMaintenanceFixture(t, "ccu-01", ops)
	err := dom.RebootCCU(context.Background(), "does-not-exist")
	if !errors.Is(err, hmerr.ErrUnknownCentral) {
		t.Fatalf("want hmerr.ErrUnknownCentral, got %v", err)
	}
	if ops.rebootCalls != 0 {
		t.Fatalf("backend must not be called for an unknown central")
	}
}

func TestCCUMaintenanceRebootCCUUnsupportedBackend(t *testing.T) {
	t.Parallel()
	// A plain fakeOperations does not implement ccuRebooter (no RebootCCU).
	ops := &fakeOperations{kind: backends.KindCUxD}
	dom := buildCCUMaintenanceFixture(t, "ccu-01", ops)
	err := dom.RebootCCU(context.Background(), "ccu-01")
	if !errors.Is(err, backends.ErrUnsupported) {
		t.Fatalf("want backends.ErrUnsupported, got %v", err)
	}
}

func TestCCUMaintenanceRebootCCUPropagatesBackendError(t *testing.T) {
	t.Parallel()
	ops := &rebootOps{
		fakeOperations: fakeOperations{kind: backends.KindCCU},
		rebootErr:      errors.New("ccu unreachable"),
	}
	dom := buildCCUMaintenanceFixture(t, "ccu-01", ops)
	if err := dom.RebootCCU(context.Background(), "ccu-01"); err == nil {
		t.Fatal("expected the backend reboot error to propagate")
	}
}

func TestCCUMaintenanceRebootCCUNilRegistry(t *testing.T) {
	t.Parallel()
	dom := NewCCUMaintenanceDomain(nil, nil)
	if err := dom.RebootCCU(context.Background(), "ccu-01"); !errors.Is(err, hmerr.ErrUnknownCentral) {
		t.Fatalf("want hmerr.ErrUnknownCentral, got %v", err)
	}
}
