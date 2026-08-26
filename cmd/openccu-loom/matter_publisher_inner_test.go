// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/rest/ws"
)

// TestMatterEventPublisher_PublishEndpointAssembled_NoClients verifies
// that publishEndpointAssembled does not panic when no clients are
// connected to the hub.
func TestMatterEventPublisher_PublishEndpointAssembled_NoClients(t *testing.T) {
	t.Parallel()
	hub := ws.NewHub()
	pub := &matterEventPublisher{hub: hub}
	// Must not panic.
	pub.publishEndpointAssembled(5)
}

// TestMatterEventPublisher_PublishEndpointAssembled_NilHub_NoOp verifies
// that publishEndpointAssembled is safe when the hub is nil (via the
// PublishMatterEvent nil guard).
func TestMatterEventPublisher_PublishEndpointAssembled_NilHub_NoOp(t *testing.T) {
	t.Parallel()
	pub := &matterEventPublisher{hub: nil}
	// Must not panic.
	pub.publishEndpointAssembled(0)
}

// TestMatterEventPublisher_PublishFabricAdded_NoClients verifies that
// publishFabricAdded does not panic when no clients are connected.
func TestMatterEventPublisher_PublishFabricAdded_NoClients(t *testing.T) {
	t.Parallel()
	hub := ws.NewHub()
	pub := &matterEventPublisher{hub: hub}
	// Must not panic.
	pub.publishFabricAdded(1)
}

// TestMatterEventPublisher_PublishFabricAdded_NilHub_NoOp verifies that
// publishFabricAdded is safe when the hub is nil.
func TestMatterEventPublisher_PublishFabricAdded_NilHub_NoOp(t *testing.T) {
	t.Parallel()
	pub := &matterEventPublisher{hub: nil}
	// Must not panic.
	pub.publishFabricAdded(7)
}

// TestMatterEventPublisher_PublishFabricRemoved_NoClients verifies that
// publishFabricRemoved does not panic when no clients are connected.
func TestMatterEventPublisher_PublishFabricRemoved_NoClients(t *testing.T) {
	t.Parallel()
	hub := ws.NewHub()
	pub := &matterEventPublisher{hub: hub}
	// Must not panic.
	pub.publishFabricRemoved(2)
}

// TestMatterEventPublisher_PublishFabricRemoved_NilHub_NoOp verifies that
// publishFabricRemoved is safe when the hub is nil.
func TestMatterEventPublisher_PublishFabricRemoved_NilHub_NoOp(t *testing.T) {
	t.Parallel()
	pub := &matterEventPublisher{hub: nil}
	// Must not panic.
	pub.publishFabricRemoved(3)
}
