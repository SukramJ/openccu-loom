// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mdns_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/mdns"
)

// ---- NewNoop ----

func TestNoop_NewNoop_EmptyActive(t *testing.T) {
	t.Parallel()
	n := mdns.NewNoop()
	if got := n.Active(); len(got) != 0 {
		t.Fatalf("Active() on fresh Noop: len=%d, want 0", len(got))
	}
}

// ---- Publish ----

func TestNoop_Publish_ValidService_ActiveCount(t *testing.T) {
	t.Parallel()
	n := mdns.NewNoop()
	svc := validService(t)
	if err := n.Publish(context.Background(), svc); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if got := len(n.Active()); got != 1 {
		t.Fatalf("Active() len=%d, want 1", got)
	}
}

func TestNoop_Publish_InvalidService_ReturnsValidationError(t *testing.T) {
	t.Parallel()
	n := mdns.NewNoop()
	svc := validService(t)
	svc.InstanceName = "" // triggers ErrInvalidService
	err := n.Publish(context.Background(), svc)
	if err == nil {
		t.Fatal("expected error for invalid service, got nil")
	}
	if !errors.Is(err, mdns.ErrInvalidService) {
		t.Fatalf("expected ErrInvalidService, got %v", err)
	}
}

func TestNoop_Publish_SameKey_Replaces(t *testing.T) {
	t.Parallel()
	n := mdns.NewNoop()
	ctx := context.Background()

	svc := validService(t)
	if err := n.Publish(ctx, svc); err != nil {
		t.Fatalf("first Publish: %v", err)
	}

	// Publish same instance + serviceType with a different port.
	svc.Port = 9999
	if err := n.Publish(ctx, svc); err != nil {
		t.Fatalf("second Publish: %v", err)
	}

	active := n.Active()
	if len(active) != 1 {
		t.Fatalf("Active() len=%d after replace, want 1", len(active))
	}
	if active[0].Port != 9999 {
		t.Fatalf("Active()[0].Port = %d, want 9999 (updated value)", active[0].Port)
	}
}

func TestNoop_Publish_DifferentKey_IncreasesCount(t *testing.T) {
	t.Parallel()
	n := mdns.NewNoop()
	ctx := context.Background()

	svc1 := validService(t)
	if err := n.Publish(ctx, svc1); err != nil {
		t.Fatalf("Publish svc1: %v", err)
	}

	// Different InstanceName → different key.
	svc2 := validService(t)
	svc2.InstanceName = "AAAAAAAAAAAAAAAA-0000000099999999"
	if err := n.Publish(ctx, svc2); err != nil {
		t.Fatalf("Publish svc2: %v", err)
	}

	if got := len(n.Active()); got != 2 {
		t.Fatalf("Active() len=%d, want 2", got)
	}
}

// ---- Withdraw ----

func TestNoop_Withdraw_Existing_Removes(t *testing.T) {
	t.Parallel()
	n := mdns.NewNoop()
	ctx := context.Background()

	svc := validService(t)
	if err := n.Publish(ctx, svc); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if err := n.Withdraw(ctx, svc.InstanceName, svc.ServiceType); err != nil {
		t.Fatalf("Withdraw: %v", err)
	}
	if got := len(n.Active()); got != 0 {
		t.Fatalf("Active() len=%d after Withdraw, want 0", got)
	}
}

func TestNoop_Withdraw_NonExisting_ReturnsErrServiceNotFound(t *testing.T) {
	t.Parallel()
	n := mdns.NewNoop()
	err := n.Withdraw(context.Background(), "NOTEXIST", mdns.ServiceTypeOperational)
	if err == nil {
		t.Fatal("expected error for non-existing service, got nil")
	}
	if !errors.Is(err, mdns.ErrServiceNotFound) {
		t.Fatalf("expected ErrServiceNotFound, got %v", err)
	}
}

// ---- Close ----

func TestNoop_Close_NoopIdempotent(t *testing.T) {
	t.Parallel()
	n := mdns.NewNoop()
	if err := n.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := n.Close(); err != nil {
		t.Fatalf("second Close (idempotent): %v", err)
	}
}

// ---- Active defensive copy ----

func TestNoop_Active_LengthConsistentAfterMutate(t *testing.T) {
	t.Parallel()
	n := mdns.NewNoop()
	ctx := context.Background()

	svc := validService(t)
	if err := n.Publish(ctx, svc); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	first := n.Active()
	if len(first) != 1 {
		t.Fatalf("Active() len=%d, want 1", len(first))
	}

	// Mutate the returned slice: clear it.
	first[0] = mdns.Service{}

	// Active() should still report 1 item with original data.
	second := n.Active()
	if len(second) != 1 {
		t.Fatalf("Active() after external mutation: len=%d, want 1", len(second))
	}
	if second[0].InstanceName != svc.InstanceName {
		t.Fatalf("InstanceName changed after external slice mutation: got %q, want %q",
			second[0].InstanceName, svc.InstanceName)
	}
}

// ---- Concurrent safety ----

func TestNoop_Concurrent_Race(t *testing.T) {
	t.Parallel()
	n := mdns.NewNoop()
	ctx := context.Background()

	// Pre-populate one entry so Withdraw has something to hit sometimes.
	base := validService(t)
	if err := n.Publish(ctx, base); err != nil {
		t.Fatalf("initial Publish: %v", err)
	}

	var wg sync.WaitGroup
	const goroutines = 16

	for i := range goroutines {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			switch i % 4 {
			case 0:
				// Publish the base service (replace).
				_ = n.Publish(ctx, base)
			case 1:
				// Publish a distinct service.
				svc := validService(t)
				svc.InstanceName = "BBBBBBBBBBBBBBBB-000000000000000" + string(rune('0'+i%10))
				_ = n.Publish(ctx, svc)
			case 2:
				// Withdraw base — may or may not exist.
				_ = n.Withdraw(ctx, base.InstanceName, base.ServiceType)
			case 3:
				// Read active.
				_ = n.Active()
			}
		}(i)
	}
	wg.Wait()
}
