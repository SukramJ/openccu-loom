// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// hotplugIngestorCall records the arguments of one invocation of a fake
// hot-plug ingestor installed via [central.Unit.SetDeviceIngestFn].
type hotplugIngestorCall struct {
	interfaceID  string
	descriptions []hmproto.DeviceDescription
}

// fakeHotplugIngestor is a test double for the function NewDevices hands to
// [central.Unit.SetDeviceIngestFn]. entered closes on the first call so
// a test can observe "ingest is running" before it completes. When release is
// non-nil the call blocks on it (or on ctx cancellation) before recording
// itself on calls, letting a test hold the ingest open to check ordering or
// drain behaviour. err is returned from every call, letting a test simulate
// an ingest failure.
type fakeHotplugIngestor struct {
	calls     chan hotplugIngestorCall
	entered   chan struct{}
	enterOnce sync.Once
	release   chan struct{}
	err       error
}

func newFakeHotplugIngestor() *fakeHotplugIngestor {
	return &fakeHotplugIngestor{
		calls:   make(chan hotplugIngestorCall, 4),
		entered: make(chan struct{}),
	}
}

func (f *fakeHotplugIngestor) ingest(ctx context.Context, interfaceID string, descriptions []hmproto.DeviceDescription) error {
	f.enterOnce.Do(func() { close(f.entered) })
	if f.release != nil {
		select {
		case <-f.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	f.calls <- hotplugIngestorCall{interfaceID: interfaceID, descriptions: descriptions}
	return f.err
}

// waitForCreatedEvent polls counter until it observes a DeviceCreatedEvent or
// times out. Polling (rather than a channel) keeps the helper usable with the
// plain atomic.Int32 counter the existing delay test already uses.
func waitForCreatedEvent(t *testing.T, counter *atomic.Int32, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if counter.Load() > 0 {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return counter.Load() > 0
}

// TestNewDevicesInvokesInstalledHotplugIngestorAsynchronously verifies that a
// hot-plug ingestor installed via SetDeviceIngestFn is invoked in the
// background with the parsed device descriptions and the canonical
// (instance-stripped) interface id, not the raw wire triple NewDevices
// receives.
func TestNewDevicesInvokesInstalledHotplugIngestorAsynchronously(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-hotplug", InstanceName: "loom1"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	h := NewCallbackHandlers(c, nil)
	defer h.Stop()

	fake := newFakeHotplugIngestor()
	c.SetDeviceIngestFn(fake.ingest)

	// The raw wire id carries the instance-name prefix; the ingestor must see
	// the stripped canonical form used everywhere else in the model.
	if err := h.NewDevices(context.Background(), "loom1-ccu-hotplug-HmIP-RF", newDeviceDescs()); err != nil {
		t.Fatalf("NewDevices: %v", err)
	}

	select {
	case call := <-fake.calls:
		const wantIfaceID = "ccu-hotplug-HmIP-RF"
		if call.interfaceID != wantIfaceID {
			t.Fatalf("ingestor interfaceID = %q, want canonical %q", call.interfaceID, wantIfaceID)
		}
		if len(call.descriptions) != 1 || call.descriptions[0].Address != "DELAY001" {
			t.Fatalf("ingestor descriptions = %+v, want one entry for DELAY001", call.descriptions)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("installed hot-plug ingestor was never invoked")
	}
}

// TestNewDevicesEventFiresOnlyAfterIngestorReturns pins the ordering
// NewDevices' doc comment promises: HandleNewDevices — and with it the
// DeviceCreatedEvent — must run only after the hot-plug ingestor has
// returned, so north-bound subscribers resolve the device in the model when
// the event fires. The fake ingestor blocks on a release gate so the test
// can observe "no event yet" while ingest is still running.
func TestNewDevicesEventFiresOnlyAfterIngestorReturns(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-order"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	var created atomic.Int32
	unsub := events.Subscribe(c.EventBus, func(hmevent.DeviceCreatedEvent) {
		created.Add(1)
	})
	defer unsub()

	h := NewCallbackHandlers(c, nil)
	defer h.Stop()

	fake := newFakeHotplugIngestor()
	fake.release = make(chan struct{})
	c.SetDeviceIngestFn(fake.ingest)

	if err := h.NewDevices(context.Background(), "HmIP-RF", newDeviceDescs()); err != nil {
		t.Fatalf("NewDevices: %v", err)
	}

	select {
	case <-fake.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("hot-plug ingestor never started")
	}

	// Give a wrongly-ordered implementation (one that fires HandleNewDevices
	// concurrently with, rather than after, the ingestor) a chance to leak
	// the event through while ingest is still blocked.
	time.Sleep(50 * time.Millisecond)
	if got := created.Load(); got != 0 {
		t.Fatalf("DeviceCreatedEvent fired before the hot-plug ingestor returned (count=%d)", got)
	}

	close(fake.release)

	select {
	case <-fake.calls:
	case <-time.After(2 * time.Second):
		t.Fatal("ingestor call was not recorded after release")
	}
	if !waitForCreatedEvent(t, &created, 2*time.Second) {
		t.Fatal("DeviceCreatedEvent did not fire after the hot-plug ingestor returned")
	}
}

// TestNewDevicesWithoutIngestorStillHandlesNewDevices verifies that
// NewDevices degrades to registry-and-event bookkeeping only when no
// ingestor has been installed (the pre-wiring window the doc comment on
// SetDeviceIngestFn describes) — HandleNewDevices still runs and the
// DeviceCreatedEvent still fires.
func TestNewDevicesWithoutIngestorStillHandlesNewDevices(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-no-ingestor"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	var created atomic.Int32
	unsub := events.Subscribe(c.EventBus, func(hmevent.DeviceCreatedEvent) {
		created.Add(1)
	})
	defer unsub()

	h := NewCallbackHandlers(c, nil)
	defer h.Stop()
	// No SetDeviceIngestFn call: hotplugIngest stays nil.

	if err := h.NewDevices(context.Background(), "HmIP-RF", newDeviceDescs()); err != nil {
		t.Fatalf("NewDevices: %v", err)
	}

	if !waitForCreatedEvent(t, &created, 2*time.Second) {
		t.Fatal("DeviceCreatedEvent did not fire without an installed hot-plug ingestor")
	}
}

// TestNewDevicesDeferredCreationSkipsIngestorAndEvent verifies that
// delayNewDeviceCreation mode bypasses both the hot-plug ingestor and
// HandleNewDevices — the descriptions only land in the delayed-inbox store.
// It then drives the inbox-accept path to prove the descriptions really
// reached the deferred queue, not just that no event fired for an
// unrelated reason.
func TestNewDevicesDeferredCreationSkipsIngestorAndEvent(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-deferred"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	var created atomic.Int32
	unsub := events.Subscribe(c.EventBus, func(hmevent.DeviceCreatedEvent) {
		created.Add(1)
	})
	defer unsub()

	h := NewCallbackHandlers(c, nil)
	defer h.Stop()
	h.SetDelayNewDeviceCreation(true)

	fake := newFakeHotplugIngestor()
	c.SetDeviceIngestFn(fake.ingest)

	if err := h.NewDevices(context.Background(), "HmIP-RF", newDeviceDescs()); err != nil {
		t.Fatalf("NewDevices: %v", err)
	}

	select {
	case call := <-fake.calls:
		t.Fatalf("hot-plug ingestor was invoked in delayed mode: %+v", call)
	case <-time.After(200 * time.Millisecond):
		// Expected: no call within the grace window.
	}
	if got := created.Load(); got != 0 {
		t.Fatalf("DeviceCreatedEvent fired in delayed mode (count=%d)", got)
	}

	// Prove the description actually reached the deferred-creation queue:
	// the accept path only finds it there.
	accepted, err := AcceptPendingDevice(context.Background(), c, "DELAY001")
	if err != nil {
		t.Fatalf("AcceptPendingDevice: %v", err)
	}
	if !accepted {
		t.Fatal("the accept path found nothing in the deferred queue; NewDevices did not store the description")
	}
	if !waitForCreatedEvent(t, &created, 2*time.Second) {
		t.Fatal("accepting the deferred device published no DeviceCreatedEvent")
	}
}

// TestNewDevicesIngestorErrorStillHandlesNewDevices verifies that a failing
// hot-plug ingestor does not stop HandleNewDevices from running (the
// DeviceCreatedEvent still fires) and does not panic the background
// goroutine.
func TestNewDevicesIngestorErrorStillHandlesNewDevices(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-ingest-error"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	var created atomic.Int32
	unsub := events.Subscribe(c.EventBus, func(hmevent.DeviceCreatedEvent) {
		created.Add(1)
	})
	defer unsub()

	h := NewCallbackHandlers(c, nil)
	defer h.Stop()

	fake := newFakeHotplugIngestor()
	fake.err = errors.New("simulated ingest failure")
	c.SetDeviceIngestFn(fake.ingest)

	if err := h.NewDevices(context.Background(), "HmIP-RF", newDeviceDescs()); err != nil {
		t.Fatalf("NewDevices: %v", err)
	}

	select {
	case <-fake.calls:
	case <-time.After(2 * time.Second):
		t.Fatal("hot-plug ingestor was never invoked")
	}
	if !waitForCreatedEvent(t, &created, 2*time.Second) {
		t.Fatal("DeviceCreatedEvent did not fire after a failing hot-plug ingestor")
	}
}

// TestStopDrainsInFlightHotplugIngestGoroutine verifies that Stop() blocks
// until the background goroutine NewDevices spawned has fully drained,
// mirroring the self-reload drain guarantee in
// TestRegisterCentralCallbacksDeregisterDrainsHandler. The fake ingestor
// blocks until context cancellation, so Stop() must cancel h.ctx and wait
// for the goroutine before returning.
func TestStopDrainsInFlightHotplugIngestGoroutine(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-drain-hotplug"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	h := NewCallbackHandlers(c, nil)

	fake := newFakeHotplugIngestor()
	// Never closed: only ctx cancellation (via Stop) can unblock this call.
	fake.release = make(chan struct{})
	c.SetDeviceIngestFn(fake.ingest)

	if err := h.NewDevices(context.Background(), "HmIP-RF", newDeviceDescs()); err != nil {
		t.Fatalf("NewDevices: %v", err)
	}

	select {
	case <-fake.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("hot-plug ingestor never started; Stop() drain has nothing to prove")
	}

	stopped := make(chan struct{})
	go func() {
		h.Stop()
		close(stopped)
	}()

	select {
	case <-stopped:
		// Stop() drained the in-flight hot-plug goroutine.
	case <-time.After(2 * time.Second):
		t.Fatal("Stop() did not return; the in-flight hot-plug ingest goroutine leaked past shutdown")
	}
}
