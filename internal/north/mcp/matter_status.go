// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package mcp

import "context"

// MatterStatusReader is the narrow facade `get_matter_status` reads
// through. The REST side serves its status from go-fabric-ui's handler
// (GET /api/v1/node/status) over the node facade directly; the assistant
// surface keeps a seam of its own because it also folds in what only the
// daemon knows — the operator's enabled allowlist rows and whether the
// bridge is configured on at all. The composition root implements it in
// cmd/openccu-loom (matterStatusReaderAdapter), which the daemon's health
// probe reads as well.
type MatterStatusReader interface {
	MatterStatus(ctx context.Context) MatterStatus
}

// MatterStatus is the bridge's runtime state as the assistant sees it.
type MatterStatus struct {
	// Enabled is the configuration flag (`north.matter.enabled`).
	Enabled bool
	// Listening reports whether the node's UDP listener is bound.
	Listening  bool
	ListenAddr string
	// EndpointCount is the number of bridged (non-root, non-Aggregator)
	// endpoints the node serves.
	EndpointCount int
	// FabricCount is the number of commissioned fabrics.
	FabricCount int
	// EnabledCount is how many allowlist rows the operator enabled.
	EnabledCount int
	Advertising  bool
	// WindowOpen reports whether a commissioning window is open right now;
	// WindowDuration is how long it was opened for, zero when closed.
	WindowOpen     bool
	WindowDuration uint16
}
