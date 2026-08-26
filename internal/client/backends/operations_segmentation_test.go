// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package backends

// Compile-time assertions that every concrete backend satisfies both the
// composite [Operations] interface and each capability sub-interface
// individually. The embedding in Operations guarantees this by construction,
// but explicit assertions here make the decomposition visible to the
// compiler at a glance and will fail with a clear error if a new method is
// accidentally omitted from an implementation.

var (
	// Full composite interface.
	_ Operations = (*CcuBackend)(nil)
	_ Operations = (*CuxdBackend)(nil)
	_ Operations = (*HomegearBackend)(nil)

	// LifecycleOps — Kind, Capabilities, Init, Deinit, Ping.
	_ LifecycleOps = (*CcuBackend)(nil)
	_ LifecycleOps = (*CuxdBackend)(nil)
	_ LifecycleOps = (*HomegearBackend)(nil)

	// DeviceOps — enumeration, firmware, pairing, bulk data, metadata.
	_ DeviceOps = (*CcuBackend)(nil)
	_ DeviceOps = (*CuxdBackend)(nil)
	_ DeviceOps = (*HomegearBackend)(nil)

	// ParamsetOps — descriptor and value read/write for MASTER/VALUES/LINK keys.
	_ ParamsetOps = (*CcuBackend)(nil)
	_ ParamsetOps = (*CuxdBackend)(nil)
	_ ParamsetOps = (*HomegearBackend)(nil)

	// ValueOps — single-parameter get/set and click-event usage counter.
	_ ValueOps = (*CcuBackend)(nil)
	_ ValueOps = (*CuxdBackend)(nil)
	_ ValueOps = (*HomegearBackend)(nil)

	// LinkOps — direct-link CRUD and per-link paramset access.
	_ LinkOps = (*CcuBackend)(nil)
	_ LinkOps = (*CuxdBackend)(nil)
	_ LinkOps = (*HomegearBackend)(nil)

	// SystemOps — install mode, service/alarm messages, rooms, functions,
	// programs, system variables, and system update info.
	_ SystemOps = (*CcuBackend)(nil)
	_ SystemOps = (*CuxdBackend)(nil)
	_ SystemOps = (*HomegearBackend)(nil)
)
