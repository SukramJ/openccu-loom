// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matteradapter"
)

// TestMatterReassemblerFailsOnlyWhenTheTopologyIsNotServed pins what the
// node pages read from a reassembly: go-fabric-ui answers a selection
// "stored, not in effect" and withholds endpoints_changed whenever the
// hook fails, so a device the sync could not bridge must not fail it —
// the topology is served — while a sync after which the node does not
// serve the described topology must.
func TestMatterReassemblerFailsOnlyWhenTheTopologyIsNotServed(t *testing.T) {
	t.Parallel()
	notServed := fmt.Errorf("matteradapter: central %q: %w", "ccu1", fmt.Errorf("%w: reassemble", matteradapter.ErrNotServed))
	for _, tc := range []struct {
		name    string
		sync    error
		wantErr bool
	}{
		{"served", nil, false},
		{"a device refused", errors.New(`matteradapter: central "ccu1": node: batch Add k: duplicate key`), false},
		{"not served", notServed, true},
		{"not served beside a refused device", errors.Join(errors.New("refused"), notServed), true},
	} {
		syncErr := tc.sync
		reassemble := matterReassembler(func(context.Context) error { return syncErr }, nil, slog.New(slog.DiscardHandler))
		err := reassemble(t.Context())
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: reassemble = %v, want failure %v", tc.name, err, tc.wantErr)
		}
	}
}
