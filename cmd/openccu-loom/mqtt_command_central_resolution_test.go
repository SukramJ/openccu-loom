// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	clientpkg "github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// recordingBackendOps records the SetValue calls that reach the wire.
type recordingBackendOps struct {
	*testBackendOps

	mu        sync.Mutex
	addresses []string
}

func (r *recordingBackendOps) SetValue(_ context.Context, address string, _ hmenum.Parameter, _ any,
	_ hmenum.CommandPriority, _ hmenum.CommandRxMode,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.addresses = append(r.addresses, address)
	return nil
}

func (r *recordingBackendOps) calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.addresses)
}

// TestMQTTSubscriberBuilderResolvesEscapedCentralSegment pins the
// composition root, not the setter: it builds the subscriber the way the
// daemon does, publishes to the command topic the daemon itself
// advertises for a central whose name contains a space, and asserts the
// write reaches that central's backend.
//
// Every publisher escapes the central name into the topic
// (`Wohn Zimmer` → `Wohn_Zimmer`) while the ValueWriter is keyed on the
// configured name, so without the resolver in the composition root
// every MQTT command for such a CCU died as "no backend" while its
// state topics kept updating.
func TestMQTTSubscriberBuilderResolvesEscapedCentralSegment(t *testing.T) {
	t.Parallel()
	const (
		base        = "openccu-loom"
		centralName = "Wohn Zimmer"
		segment     = "Wohn_Zimmer"
	)
	ctx := context.Background()

	unit, err := central.New(central.Config{Name: centralName})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(unit); err != nil {
		t.Fatalf("registry.Register: %v", err)
	}
	ops := &recordingBackendOps{testBackendOps: &testBackendOps{}}
	writer := clientpkg.NewValueWriter()
	writer.Register(centralName, "HmIP-RF", ops)

	build := makeMQTTSubscriberBuilder(ctx, reg, writer, nil, nil, nil, nil, nil, supervisorLogger())
	noop := mqtt.NewNoopClient()
	bridge := mqtt.NewBridge(mqtt.BridgeConfig{
		Base: base, CentralName: centralName, RawEnabled: true,
	}, noop)
	teardown, err := build(ctx, noop, bridge)
	if err != nil {
		t.Fatalf("subscriber builder: %v", err)
	}
	t.Cleanup(func() {
		if teardown != nil {
			teardown()
		}
	})

	if !noop.DeliverInbound(base+"/+/+/+/+/+/+/set",
		base+"/"+segment+"/HmIP-RF/0001ABCD/4/values/STATE/set", []byte("true")) {
		t.Fatal("the daemon does not subscribe to its own declared command topic")
	}

	deadline := time.Now().Add(2 * time.Second)
	for ops.calls() == 0 && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if ops.calls() == 0 {
		t.Fatalf("no write reached the backend of central %q — every MQTT command for it is dropped", centralName)
	}
	if got := ops.addresses[0]; got != "0001ABCD:4" {
		t.Errorf("wrote to %q, want %q", got, "0001ABCD:4")
	}
}
