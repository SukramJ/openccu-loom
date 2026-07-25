// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
)

// comTester is the narrow capability the communication test needs from a
// backend — implemented by *backends.CcuBackend via the Operations
// contract.
type comTester interface {
	TestDevice(ctx context.Context, address string, maxWaitSecs, pollIntervalSecs float64) (hmapi.CommunicationTestResult, error)
}

// TestDeviceCommunication runs the CCU's per-device communication /
// function test: the interface process sends a radio test frame to the
// device and awaits its ACK. Gated to the radio interfaces (HmIP-RF,
// BidCos-RF, BidCos-Wired); VirtualDevices and CUxD answer
// [backends.ErrUnsupported] before any wire call. Multi-CCU safe via the
// registry scan.
func (a *DeviceAdminDomain) TestDeviceCommunication(ctx context.Context, address string) (hmapi.CommunicationTestResult, error) {
	if a.registry == nil || a.writer == nil {
		return hmapi.CommunicationTestResult{}, ErrNoDeviceBackend
	}
	for _, u := range a.registry.List() {
		dev, ok := u.ModelRegistry.Get(address)
		if !ok {
			continue
		}
		if !dev.Interface.SupportsCommunicationTest() {
			return hmapi.CommunicationTestResult{}, fmt.Errorf("communication test: interface %s: %w", dev.Interface, backends.ErrUnsupported)
		}
		backend, ok := a.writer.Backend(u.Name(), dev.InterfaceID)
		if !ok {
			return hmapi.CommunicationTestResult{}, fmt.Errorf("%w: %s/%s", ErrNoDeviceBackend, u.Name(), dev.InterfaceID)
		}
		tester, ok := backend.(comTester)
		if !ok {
			return hmapi.CommunicationTestResult{}, fmt.Errorf("communication test: %w", backends.ErrUnsupported)
		}
		// Defaults (30s window, 2s poll) are applied by the backend.
		return tester.TestDevice(ctx, address, 0, 0)
	}
	return hmapi.CommunicationTestResult{}, fmt.Errorf("%w: device %s", ErrNoDeviceBackend, address)
}
