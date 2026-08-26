// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/coordinators"
)

func newTestCentral(t *testing.T) *central.Unit {
	t.Helper()
	c, err := central.New(central.Config{Name: "test"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	return c
}

// TestWireLinkCoordinatorInstallsResolver verifies that after the wire
// step, central.Link.SetResolver was actually called — the resolver
// returns a non-nil LinkClient for any non-empty device address.
func TestWireLinkCoordinatorInstallsResolver(t *testing.T) {
	t.Parallel()
	c := newTestCentral(t)
	domain := NewLinksDomain(central.NewRegistry(), nil, nil)

	if err := WireLinkCoordinator(c, domain); err != nil {
		t.Fatalf("WireLinkCoordinator: %v", err)
	}

	// Probe the resolver indirectly: the LinkCoordinator's GetLinks
	// should now route through the adapter (and fail because the
	// LinksDomain has no devices, but with a non-ErrLinkClientMissing
	// error — a real domain call took place).
	_, err := c.Link.GetLinks(t.Context(), "VCU0001")
	if err == nil {
		t.Fatal("GetLinks must error against an empty registry")
	}
	if errors.Is(err, coordinators.ErrLinkClientMissing) {
		t.Fatalf("after wiring, ErrLinkClientMissing must NOT surface — got %v", err)
	}
}

func TestWireLinkCoordinatorRejectsNilCentral(t *testing.T) {
	t.Parallel()
	if err := WireLinkCoordinator(nil, &LinksDomain{}); err == nil {
		t.Fatal("nil central must yield an error")
	}
}

func TestWireLinkCoordinatorRejectsNilDomain(t *testing.T) {
	t.Parallel()
	c := newTestCentral(t)
	if err := WireLinkCoordinator(c, nil); err == nil {
		t.Fatal("nil domain must yield an error")
	}
}

// ============================================================
// linkClientAdapter — non-nil domain delegation paths
// ============================================================

func TestLinkClientAdapterNonNilDomainAddLink(t *testing.T) {
	t.Parallel()
	// domain is non-nil but registry is nil → AddLink errors, doesn't panic
	a := &linkClientAdapter{domain: NewLinksDomain(nil, nil, nil)}
	err := a.AddLink(context.Background(), "SENDER:1", "RECEIVER:1", "link-name", "description")
	if err == nil {
		t.Error("nil registry AddLink must return error")
	}
}

func TestLinkClientAdapterNonNilDomainRemoveLink(t *testing.T) {
	t.Parallel()
	a := &linkClientAdapter{domain: NewLinksDomain(nil, nil, nil)}
	err := a.RemoveLink(context.Background(), "SENDER:1", "RECEIVER:1")
	if err == nil {
		t.Error("nil registry RemoveLink must return error")
	}
}

func TestLinkClientAdapterNonNilDomainGetLinks(t *testing.T) {
	t.Parallel()
	a := &linkClientAdapter{domain: NewLinksDomain(nil, nil, nil)}
	_, err := a.GetLinks(context.Background(), "DEV001")
	if err == nil {
		t.Error("nil registry GetLinks must return error")
	}
}
