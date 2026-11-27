// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package matteradapter

import (
	"errors"
	"fmt"

	"thingfabric.dev/go-fabric/node"
)

// ErrNotServed marks the part of a [Reconciler.Sync] error after which the
// node does not serve what the walk described: the walk failed, so nothing
// was applied; a reassembly failed, so the set holds the changes but the
// node still serves the topology before (node.BatchError.Reassembly); or
// the node is stopped. Any other part of the error — a device a batch
// refused, a store lookup, a rename — costs that device alone: the rest of
// the topology is served.
//
// The next batch of the central reassembles a failed one, an empty batch
// included (go-fabric 0.28), and every sync runs a batch per central, so
// the next sync serves what an earlier one could not.
var ErrNotServed = errors.New("matteradapter: the node does not serve the described topology")

// batchFailure marks err with [ErrNotServed] when the batch left the
// described topology unserved, and returns it as it is otherwise.
func batchFailure(err error) error {
	if err == nil {
		return nil
	}
	if be, ok := errors.AsType[*node.BatchError](err); ok && be.Reassembly != nil {
		return fmt.Errorf("%w: %w", ErrNotServed, err)
	}
	if errors.Is(err, node.ErrStopped) {
		return fmt.Errorf("%w: %w", ErrNotServed, err)
	}
	return err
}

// batchCalls reads the calls a [node.Partition.Batch] result names, keyed
// by the call's own key: undone are the calls refused or undone after fn
// returned — the partition's set is as it would be without them — and
// unfinished the applied calls whose store work failed: the change stands,
// and a Forget among them is to be made again. A call fn's error names was
// refused at the call and is in neither; a failed reassembly names no call
// either — its unbinding and erasures wait, recorded, for the next
// reassembly. Both maps are nil for a result that is not a
// [*node.BatchError].
func batchCalls(err error) (undone, unfinished map[string]struct{}) {
	be, ok := errors.AsType[*node.BatchError](err)
	if !ok {
		return nil, nil
	}
	undone, unfinished = map[string]struct{}{}, map[string]struct{}{}
	for _, c := range be.Calls {
		if c.Key == nil {
			continue
		}
		if c.Applied {
			unfinished[c.Key.String()] = struct{}{}
		} else {
			undone[c.Key.String()] = struct{}{}
		}
	}
	return undone, unfinished
}
