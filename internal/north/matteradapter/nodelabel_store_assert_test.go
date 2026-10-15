// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package matteradapter_test

import (
	"github.com/SukramJ/openccu-loom/internal/north/matteradapter"
	matterendpoint "github.com/SukramJ/openccu-loom/internal/store/matterendpoint"
)

// The daemon's endpoint-identity store is the NodeLabel store the
// assembler persists controller-written bridged labels through.
var _ matteradapter.NodeLabelStore = (*matterendpoint.Store)(nil)
