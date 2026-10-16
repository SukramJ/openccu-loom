// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package matteradapter_test

import (
	"github.com/SukramJ/go-fabric/node"

	"github.com/SukramJ/openccu-loom/internal/north/matteradapter"
)

// go-fabric's endpoint persistence — the one the daemon hands both the
// assembler and the facade — is the NodeLabel store the assembler persists
// controller-written bridged labels through.
var _ matteradapter.NodeLabelStore = node.EndpointPersistence(nil)
