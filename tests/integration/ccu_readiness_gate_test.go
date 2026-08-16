// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build integration

// pairing_lifecycle_test.go covers the readiness gate the daemon waits
// behind at boot. A CCU that had not finished booting used to answer its
// remote API as though it had, so the "not ready" case could only be
// exercised through the web API — the half of the surface the gate does
// not actually protect the ingest against.

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/godevccu/pkg/godevccu"

	"github.com/SukramJ/openccu-loom/internal/client/backends"
)

// TestNotReadyCCURefusesTheRemoteAPI pins the boot state the readiness
// gate exists for.
//
// An add-on started alongside a rebooting CCU reaches a box that
// answers, but not yet with anything true. The daemon waits for the
// CCU's own boot marker before loading names and devices, so that a
// co-booting CCU never yields devices-without-names. That gate is only
// meaningful if the interface process actually refuses in the meantime —
// which the simulator did not model until it gated the XML-RPC surface
// too, so the "not ready" case could only ever be tested through the
// web API.
func TestNotReadyCCURefusesTheRemoteAPI(t *testing.T) {
	v, err := godevccu.New(godevccu.Config{
		Mode:          godevccu.BackendModeCCU,
		Host:          "127.0.0.1",
		XMLRPCPort:    godevccu.EphemeralPort,
		JSONRPCPort:   godevccu.EphemeralPort,
		Devices:       defaultMockDevices,
		StartNotReady: true,
	})
	if err != nil {
		t.Fatalf("godevccu.New: %v", err)
	}
	if err := v.Start(); err != nil {
		t.Fatalf("godevccu.Start: %v", err)
	}
	t.Cleanup(func() { _ = v.Stop() })

	client := newXMLRPCClient(t, "http://"+v.XMLRPCAddr().String()+"/")
	backend := backends.NewCcuBackend(&xmlrpcBackendCaller{client: client}, nil, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if _, err := backend.ListDevices(ctx); err == nil {
		t.Error("a CCU that has not finished booting answered listDevices — the daemon would " +
			"ingest whatever that half-started box reports, which is the devices-without-names " +
			"state the readiness gate exists to avoid")
	}

	v.SetReady(true)

	descs, err := backend.ListDevices(ctx)
	if err != nil {
		t.Fatalf("listDevices after the CCU reported ready: %v", err)
	}
	if len(descs) == 0 {
		t.Error("a ready CCU returned an empty device list; the gate opened but nothing came " +
			"through, which the ingest path reads as a fleet that vanished")
	}
}
