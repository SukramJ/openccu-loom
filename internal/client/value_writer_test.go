// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package client

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// TestValueWriterRegistrationAndWritePathShareOneKey pins that the two ways
// into the backend registry agree on the key.
//
// Registration takes the wire id as [hmtypes.WireInterfaceID] so a caller
// cannot hand it the bare interface name a device also carries, while the
// write entry points still take it as a string — their signature is fixed by
// port interfaces in the packages that call them. The adoption happens in one
// place; if it ever moves or changes shape, a write stops resolving the
// backend it was just registered against and every command on that interface
// fails with ErrNoBackend while the interface is plainly connected.
func TestValueWriterRegistrationAndWritePathShareOneKey(t *testing.T) {
	t.Parallel()

	const centralName = "ccu-1"
	wireID := hmtypes.NewWireInterfaceID(centralName, hmenum.InterfaceHmIPRF)

	b := &countingBackend{}
	w := NewValueWriter()
	w.Register(centralName, wireID, b)

	if _, ok := w.Backend(centralName, wireID); !ok {
		t.Fatalf("Backend(%q, %q) missed the backend that was just registered", centralName, wireID)
	}
	if err := w.SetValue(
		context.Background(), centralName, wireID.String(), "ABC0001:1",
		hmenum.ParameterState, true, hmenum.CommandPriorityCritical,
	); err != nil {
		t.Fatalf("SetValue through the same wire id: %v", err)
	}
	if b.SetCallCount() == 0 {
		t.Fatal("the write never reached the registered backend")
	}

	// The bare interface is a different key: it is what a device carries
	// alongside the wire id, and resolving with it is the mistake the typed
	// key exists to make impossible where it can, and visible where it cannot.
	bare := hmtypes.ParseWireInterfaceID(string(hmenum.InterfaceHmIPRF))
	if _, ok := w.Backend(centralName, bare); ok {
		t.Errorf("Backend(%q, %q) resolved — the bare interface must not alias the wire id",
			centralName, bare)
	}
	if err := w.SetValue(
		context.Background(), centralName, string(hmenum.InterfaceHmIPRF), "ABC0001:1",
		hmenum.ParameterState, true, hmenum.CommandPriorityCritical,
	); !errors.Is(err, ErrNoBackend) {
		t.Errorf("SetValue with the bare interface: err=%v, want ErrNoBackend", err)
	}
}
