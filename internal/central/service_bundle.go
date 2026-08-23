// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package central

import (
	"context"
	"sync"

	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// serviceBundle groups the runtime service-dispatch closures that are
// populated by the hub-wiring adapter after the JSON-RPC session is
// established. Keeping them in a named struct shrinks the [Unit]
// surface and makes the wiring concern explicit.
//
// Access is always through a pointer (embedded as Unit.services); the
// mutex must never be copied.
type serviceBundle struct {
	mu sync.RWMutex

	acceptInboxFn  func(ctx context.Context, address string) error
	createBackupFn func(ctx context.Context) ([]byte, error)
	renameDeviceFn func(ctx context.Context, address, name string) error
	// deviceIngestFn materialises announced device descriptions into the
	// domain model (channels, data points, custom data points, values).
	// Both paths that turn an announcement into a device use it: the
	// hot-plug callback and the operator accepting a deferred device.
	deviceIngestFn   func(ctx context.Context, interfaceID string, descriptions []hmproto.DeviceDescription) error
	loadAndRefreshFn func(ctx context.Context) error
	saveFilesFn      func(ctx context.Context) error
	validateConfigFn func(ctx context.Context) (SystemInfo, error)
	// hubLogoutFn is the optional hook that performs the hub-side JSON-RPC
	// logout during [Unit.Stop]. When nil the step is skipped (e.g. in tests
	// or when the hub session was never established). Wire it via
	// [Unit.SetHubLogoutFn] from the hub-wiring adapter after Login succeeds.
	hubLogoutFn func(ctx context.Context) error
}
