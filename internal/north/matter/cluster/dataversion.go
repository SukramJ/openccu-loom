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
// Mirrors matter.js packages/protocol/src/interaction/
// InteractionServer.ts DataVersion tracking on per-cluster state. The
// initial value is a uniformly random non-zero uint32 chosen at first
// access — matter.js samples observed in the field carry values like
// 3408898508 / 3191265986 / 2061169561 (per-cluster initial randoms
// from a `Crypto.getRandomUint32()` call). Apple Home's MTRDevice
// cache appears to treat a uniform DataVersion=1 across every cluster
// as an "uninitialised" signal and refuses to persist those clusters
// to its on-disk cache (surface symptom: `Storing cluster information
// count: 3` despite 207 attribute reports landing live). Random init
// makes each (endpoint, cluster) carry a distinct DataVersion before
// the first mutation, which is what Apple looks for.
//
// Matter §10.6.5: "A DataVersion of zero is reserved for absent or
// invalid"; the random generator skips zero accordingly.
//
// See [hmtypes.DataVersionTracker] for the counter itself.
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
