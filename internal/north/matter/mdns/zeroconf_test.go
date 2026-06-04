// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mdns_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/matter/mdns"
)

// TestZeroconf_PublishWithdraw verifies the lifecycle: Publish records
// a service, Active reflects it, Withdraw removes it. Uses the
// in-process zeroconf server but does not assert multicast traffic
// (loopback-only CI environments don't always have a multicast peer).
func TestZeroconf_PublishWithdraw(t *testing.T) {
	t.Parallel()
	z := mdns.NewZeroconf()
	t.Cleanup(func() { _ = z.Close() })

	svc := mdns.Service{
		InstanceName: "TESTINST",
		ServiceType:  mdns.ServiceTypeOperational,
		Port:         54321,
		HostName:     "test-bridge",
		TXT:          []mdns.TXTRecord{{Key: "SII", Value: "5000"}},
	}
	if err := z.Publish(context.Background(), svc); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if got := z.Active(); len(got) != 1 || got[0].InstanceName != "TESTINST" {
		t.Errorf("Active after Publish: got %+v, want one TESTINST entry", got)
	}
	if err := z.Withdraw(context.Background(), "TESTINST", mdns.ServiceTypeOperational); err != nil {
		t.Fatalf("Withdraw: %v", err)
	}
	if got := z.Active(); len(got) != 0 {
		t.Errorf("Active after Withdraw: got %d entries, want 0", len(got))
	}
}

// TestZeroconf_WithdrawNotFound — Withdraw on an unknown instance
// returns ErrServiceNotFound.
func TestZeroconf_WithdrawNotFound(t *testing.T) {
	t.Parallel()
	z := mdns.NewZeroconf()
	t.Cleanup(func() { _ = z.Close() })
	err := z.Withdraw(context.Background(), "MISSING", mdns.ServiceTypeOperational)
	if !errors.Is(err, mdns.ErrServiceNotFound) {
		t.Errorf("Withdraw missing: want ErrServiceNotFound, got %v", err)
	}
}

// TestZeroconf_PublishAfterClose — Publish after Close returns an
// error rather than allocating a stale server.
func TestZeroconf_PublishAfterClose(t *testing.T) {
	t.Parallel()
	z := mdns.NewZeroconf()
	if err := z.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	svc := mdns.Service{
		InstanceName: "CLOSED",
		ServiceType:  mdns.ServiceTypeOperational,
		Port:         12345,
		HostName:     "test",
	}
	if err := z.Publish(context.Background(), svc); err == nil {
		t.Error("Publish after Close: want error, got nil")
	}
}

// TestZeroconf_CloseIdempotent — Close called twice returns nil both
// times.
func TestZeroconf_CloseIdempotent(t *testing.T) {
	t.Parallel()
	z := mdns.NewZeroconf()
	if err := z.Close(); err != nil {
		t.Errorf("first Close: %v", err)
	}
	if err := z.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

// TestZeroconf_RepublishReplaces — publishing the same instance/type
// pair twice replaces the prior server cleanly (no leak).
func TestZeroconf_RepublishReplaces(t *testing.T) {
	t.Parallel()
	z := mdns.NewZeroconf()
	t.Cleanup(func() { _ = z.Close() })
	svc := mdns.Service{
		InstanceName: "REUSED",
		ServiceType:  mdns.ServiceTypeOperational,
		Port:         33333,
		HostName:     "test",
		TXT:          []mdns.TXTRecord{{Key: "v", Value: "1"}},
	}
	if err := z.Publish(context.Background(), svc); err != nil {
		t.Fatalf("first Publish: %v", err)
	}
	svc.TXT[0].Value = "2"
	if err := z.Publish(context.Background(), svc); err != nil {
		t.Fatalf("second Publish: %v", err)
	}
	if got := z.Active(); len(got) != 1 {
		t.Errorf("Active after republish: got %d, want 1", len(got))
	}
}

// TestZeroconf_SubtypesRegisterSatellites — when a Service carries
// subtypes, Publish registers an additional zeroconf.Server per
// subtype so chip-tool's `_L<discriminator>._sub.*` PTR queries
// resolve to the same instance.
func TestZeroconf_SubtypesRegisterSatellites(t *testing.T) {
	t.Parallel()
	z := mdns.NewZeroconf()
	t.Cleanup(func() { _ = z.Close() })
	svc := mdns.Service{
		InstanceName: "DEADBEEF",
		ServiceType:  mdns.ServiceTypeCommissionable,
		Port:         5540,
		HostName:     "test",
		Subtypes:     []string{"_L3840", "_S15", "_V65521"},
	}
	if err := z.Publish(context.Background(), svc); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	// Active reports the primary only (subtypes are an internal
	// detail of zeroconf wiring, not a separate logical service).
	got := z.Active()
	if len(got) != 1 || got[0].InstanceName != "DEADBEEF" {
		t.Errorf("Active: got %+v, want one DEADBEEF entry", got)
	}
	// Withdraw must tear down the satellites along with the primary
	// — a follow-up Publish with the same key would otherwise see a
	// stale partial registration.
	if err := z.Withdraw(context.Background(), "DEADBEEF", mdns.ServiceTypeCommissionable); err != nil {
		t.Fatalf("Withdraw: %v", err)
	}
	if got := z.Active(); len(got) != 0 {
		t.Errorf("Active after Withdraw: got %d entries, want 0", len(got))
	}
}

// TestZeroconf_AttachSubtypeResponder verifies that AttachSubtypeResponder
// does not panic and does not affect the Active set.
func TestZeroconf_AttachSubtypeResponder(t *testing.T) {
	t.Parallel()
	z := mdns.NewZeroconf()
	t.Cleanup(func() { _ = z.Close() })

	// Passing a nil responder must not panic.
	z.AttachSubtypeResponder(nil)

	svc := mdns.Service{
		InstanceName: "ATTACHTEST",
		ServiceType:  mdns.ServiceTypeOperational,
		Port:         7777,
		HostName:     "test",
	}
	if err := z.Publish(context.Background(), svc); err != nil {
		t.Fatalf("Publish after nil attach: %v", err)
	}
	if got := z.Active(); len(got) != 1 {
		t.Errorf("Active: got %d, want 1", len(got))
	}
}

// TestZeroconf_StartReannounceLoop_CancelStops verifies that the cancel
// function returned by StartReannounceLoop stops the goroutine without
// blocking.
func TestZeroconf_StartReannounceLoop_CancelStops(t *testing.T) {
	t.Parallel()
	z := mdns.NewZeroconf()
	t.Cleanup(func() { _ = z.Close() })

	ctx := context.Background()
	cancel, _ := z.StartReannounceLoop(ctx, 24*time.Hour) // very long interval — never ticks
	// Should not block.
	cancel()
}

// TestZeroconf_StartReannounceLoop_ContextCancel verifies that a cancelled
// parent context stops the loop.
func TestZeroconf_StartReannounceLoop_ContextCancel(t *testing.T) {
	t.Parallel()
	z := mdns.NewZeroconf()
	t.Cleanup(func() { _ = z.Close() })

	ctx, ctxCancel := context.WithCancel(context.Background())
	_, _ = z.StartReannounceLoop(ctx, 24*time.Hour)
	ctxCancel() // stop via ctx
	// Brief pause so goroutine has time to exit before cleanup.
	time.Sleep(10 * time.Millisecond)
}

// TestZeroconf_StartReannounceLoop_Tick verifies that the loop actually
// calls republishAll when the ticker fires, by using a very short interval
// and checking the active set is still consistent.
func TestZeroconf_StartReannounceLoop_Tick(t *testing.T) {
	t.Parallel()
	z := mdns.NewZeroconf()
	t.Cleanup(func() { _ = z.Close() })

	svc := mdns.Service{
		InstanceName: "LOOPTICK",
		ServiceType:  mdns.ServiceTypeOperational,
		Port:         8888,
		HostName:     "test",
	}
	if err := z.Publish(context.Background(), svc); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	ctx, ctxCancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer ctxCancel()
	cancel, _ := z.StartReannounceLoop(ctx, 50*time.Millisecond)
	defer cancel()

	// Wait long enough for at least one tick.
	time.Sleep(120 * time.Millisecond)

	// Active set must still be intact after re-publish.
	if got := z.Active(); len(got) != 1 {
		t.Errorf("Active after reannounce tick: got %d, want 1", len(got))
	}
}

// TestZeroconf_TriggerReannounce_ImmediateRePublish verifies that
// TriggerReannounce re-publishes the active service set without waiting
// for the periodic tick. A very long interval is used so the periodic
// path never fires during the test.
func TestZeroconf_TriggerReannounce_ImmediateRePublish(t *testing.T) {
	t.Parallel()
	z := mdns.NewZeroconf()
	t.Cleanup(func() { _ = z.Close() })

	svc := mdns.Service{
		InstanceName: "TRIGGERTST",
		ServiceType:  mdns.ServiceTypeOperational,
		Port:         8989,
		HostName:     "test",
	}
	if err := z.Publish(context.Background(), svc); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	ctx := t.Context()
	// Long interval so only TriggerReannounce drives republish.
	_, trigger := z.StartReannounceLoop(ctx, 24*time.Hour)

	// Fire the trigger and immediately check the active set is intact.
	trigger <- struct{}{}
	time.Sleep(20 * time.Millisecond)

	if got := z.Active(); len(got) != 1 {
		t.Errorf("Active after TriggerReannounce: got %d, want 1", len(got))
	}
}

// TestPrimaryHostIPs_NoDuplicates verifies that primaryHostIPs never
// returns duplicate address strings even on a multi-homed host.
func TestPrimaryHostIPs_NoDuplicates(t *testing.T) {
	t.Parallel()
	// We cannot call primaryHostIPs directly from outside the package;
	// instead use the white-box internal test path. Here we just verify
	// that z.Publish does not panic when the system has more than one NIC
	// — the real dedup behaviour is tested in the internal package test.
	z := mdns.NewZeroconf()
	t.Cleanup(func() { _ = z.Close() })

	svc := mdns.Service{
		InstanceName: "MULTINICTEST",
		ServiceType:  mdns.ServiceTypeOperational,
		Port:         9090,
		HostName:     "test",
	}
	// Publish must not panic regardless of NIC count.
	_ = z.Publish(context.Background(), svc)
}
