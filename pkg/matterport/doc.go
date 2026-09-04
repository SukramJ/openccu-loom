// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package matterport holds the port contracts between the domain model
// and the Matter bridge: the interfaces a data point implements to
// materialise as a bridged endpoint, the cluster-server surface the
// bridge dispatches through, and the measurement / eligibility
// classifications the model computes once and the bridge consumes.
//
// The package exists so the Matter surface can be depended on without
// pulling in the REST and observer contracts that share
// [github.com/SukramJ/openccu-loom/pkg/interfaces]: those drag in
// pkg/hmapi, which the bridge has no business seeing. The Matter-side
// symbols therefore carry no Matter prefix here — the package name
// already says it — and pkg/interfaces re-exports every one of them as
// a compatibility alias so existing call sites keep compiling.
//
// The one remaining dependency on the host application is
// [github.com/SukramJ/openccu-loom/pkg/hmenum], for the
// CommandPriority that MatterWrite and MatterInvoke forward to the
// southbound command queue. Everything else is stdlib.
//
// See ADR 0012 for the rich-model / dumb-bridge split these contracts
// implement, and SPECIFICATION.md §6.2 / §6.3 for the endpoint surface.
package matterport
