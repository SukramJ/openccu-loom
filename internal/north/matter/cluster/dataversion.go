// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package cluster

import (
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// DataVersionTracker is a per-cluster monotonic counter that cluster
// servers embed to satisfy [interfaces.MatterClusterDataVersion]. Every
// successful attribute write SHOULD call [DataVersionTracker.Bump] so
// subscribers see a fresh version number in their cached state.
//
// The concrete implementation lives in pkg/hmtypes so that model
// packages (internal/model/custom/*) can embed it without importing
// this package, removing the model→northbound coupling. This alias
// keeps all cluster-internal callers unchanged.
//
// See [hmtypes.DataVersionTracker] for the full rationale and Matter
// §10.6.5 reference.
//
// Usage:
//
//	type MyCluster struct {
//	    cluster.DataVersionTracker
//	    // ...
//	}
//
//	func (c *MyCluster) MatterDataVersion() uint32 { return c.DataVersionTracker.Current() }
//
//	func (c *MyCluster) MatterWrite(...) error {
//	    // do mutation ...
//	    c.DataVersionTracker.Bump()
//	    return nil
//	}
type DataVersionTracker = hmtypes.DataVersionTracker
