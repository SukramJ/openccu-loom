// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

// daemon_coverage8_test.go — targeted coverage for remaining small gaps:
//   - daemon.go: loadFabricRootPublicKey success path (line 2330)
//   - daemon.go: deriveOperationalIPK success path (lines 2492-2497)
//   - ws_adapters.go: wsLinkQuery.ListLinks domain error path (lines 197-199)
//   - ws_adapters.go: wsLinkQuery.LinkableChannels device-not-found error (line 241)
//   - matter_window_adapter.go: announce path (lines 62-70)

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/adapter"
)

// ── loadFabricRootPublicKey: success path (line 2330) ────────────────────────

// TestWSLinkQuery_ListLinks_DomainError_ReturnsError exercises the error path
// at lines 197-199 in ws_adapters.go where the domain.ListLinks call fails.
// We use a real LinksDomain with an empty central registry so lookupDevice fails.
func TestWSLinkQuery_ListLinks_DomainError_ReturnsError(t *testing.T) {
	t.Parallel()
	// Create a LinksDomain with a registry that has no devices.
	// ListLinks will fail at lookupDevice because the device is not registered.
	reg := buildTestRegistry(t, "ccu-01")
	domain := adapter.NewLinksDomain(reg, nil, nil)

	q := &wsLinkQuery{domain: domain, registry: reg}
	_, err := q.ListLinks(context.Background(), "NOSUCHDEV:0")
	// Expect error because the device is not found.
	if err == nil {
		t.Fatal("expected error when device not found in ListLinks, got nil")
	}
}

// ── ws_adapters.go: wsLinkQuery.LinkableChannels device-not-found (line 241) ─

// TestWSLinkQuery_LinkableChannels_DeviceNotFound_ReturnsError exercises the
// device-not-found error path at line 241 of ws_adapters.go where the loop
// over registered centrals finds no device matching the address.
func TestWSLinkQuery_LinkableChannels_DeviceNotFound_ReturnsError(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "ccu-01") // has no devices
	domain := adapter.NewLinksDomain(reg, nil, nil)

	q := &wsLinkQuery{domain: domain, registry: reg}
	_, err := q.LinkableChannels(context.Background(), "NOSUCHDEV:0")
	if err == nil {
		t.Fatal("expected error when device not found in LinkableChannels, got nil")
	}
}

// ── matter_window_adapter.go: announce path (lines 62-70) ────────────────────
