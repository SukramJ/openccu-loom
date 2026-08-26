// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hmtypes

import (
	"crypto/rand"
	"encoding/binary"
	"sync/atomic"
)

// DataVersionTracker is a per-cluster monotonic counter satisfying the
// [interfaces.MatterClusterDataVersion] contract. Every successful attribute
// write SHOULD call [DataVersionTracker.Bump] so subscribers see a fresh
// version number in their cached state.
//
// Placed in pkg/hmtypes so that model packages (internal/model/custom/*)
// can embed it without importing internal/north/matter/cluster, which
// would introduce a downward domain→northbound coupling.
// internal/north/matter/cluster re-exports the type via a type alias so
// all existing cluster-side callers remain unchanged.
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
// Usage:
//
//	type MyCluster struct {
//	    hmtypes.DataVersionTracker
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
type DataVersionTracker struct {
	v atomic.Uint32
}

// InitialDataVersion returns a uniformly-random non-zero uint32. It
// is a package-level var for test-side override.
var InitialDataVersion = func() uint32 {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failure on this platform is fatal — the daemon
		// cannot ship Subscribe-Initial with a deterministic-looking
		// DataVersion fallback (Apple-cache regression). Panic
		// surfaces the configuration problem at start-up rather than
		// silently degrading.
		panic("matter: crypto/rand.Read failed: " + err.Error())
	}
	v := binary.BigEndian.Uint32(b[:])
	if v == 0 {
		v = 1
	}
	return v
}

// Current returns the current DataVersion. On first access on a fresh
// tracker, a uniformly-random non-zero uint32 is installed via CAS so
// every subsequent reader sees the same value until [Bump] advances it.
func (d *DataVersionTracker) Current() uint32 {
	if v := d.v.Load(); v != 0 {
		return v
	}
	// Race-tolerant init: only the first CAS wins, every other caller
	// reloads and observes the installed value.
	d.v.CompareAndSwap(0, InitialDataVersion())
	return d.v.Load()
}

// Bump increments the DataVersion and returns the new value. The first
// call on a fresh tracker installs the random initial value first (via
// [Current]) and then advances by 1, so the post-bump value is always
// > 1 and unique across cluster instances.
//
// Wrap-around past 0xFFFFFFFF is intentional and fine — the IM filter
// comparison uses == so any increment signals "cluster changed". Zero
// is skipped (reserved per Matter §10.6.5).
func (d *DataVersionTracker) Bump() uint32 {
	// Ensure the random initial value is installed before incrementing
	// so we never bump from the reserved zero state.
	_ = d.Current()
	next := d.v.Add(1)
	if next == 0 {
		next = d.v.Add(1)
	}
	return next
}
