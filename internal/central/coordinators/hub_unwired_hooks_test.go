// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package coordinators

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/events"
)

// TestHubCoordinatorReportsEveryUnwiredHook pins the whole family of
// delegating hub methods to the same answer: a hook that is not wired is
// reported, never rendered as success.
//
// The asymmetry this replaces is what let the alarm sysvar mirror write into
// a variable that never changed — the caller's error branch could not run
// because the coordinator returned nil for a write that never reached the
// CCU. Every sibling had the same shape.
func TestHubCoordinatorReportsEveryUnwiredHook(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	for _, tc := range []struct {
		name string
		call func(*HubCoordinator) error
		want error
	}{
		{
			name: "SuppressServiceMessage",
			call: func(h *HubCoordinator) error {
				return h.SuppressServiceMessage(ctx, "HmIP-RF", "ABC0001234:1", "STICKY_UNREACH", true)
			},
			want: ErrNoServiceMessageSuppressor,
		},
		{
			name: "GetSuppressedServiceMessages",
			call: func(h *HubCoordinator) error {
				_, err := h.GetSuppressedServiceMessages(ctx, "HmIP-RF", "ABC0001234:1")
				return err
			},
			want: ErrNoServiceMessageReader,
		},
		{
			name: "ExecuteProgram",
			call: func(h *HubCoordinator) error { return h.ExecuteProgram(ctx, "prog-1") },
			want: ErrNoProgramExecutor,
		},
		{
			name: "SetProgramState",
			call: func(h *HubCoordinator) error { return h.SetProgramState(ctx, "prog-1", true) },
			want: ErrNoProgramStateWriter,
		},
		{
			name: "GetSystemVariable",
			call: func(h *HubCoordinator) error {
				_, err := h.GetSystemVariable(ctx, "var")
				return err
			},
			want: ErrNoSysvarGetter,
		},
		{
			name: "SetSystemVariable",
			call: func(h *HubCoordinator) error { return h.SetSystemVariable(ctx, "var", true) },
			want: ErrNoSysvarWriter,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := NewHubCoordinator("c-unwired", events.NewBus())
			if err := tc.call(h); !errors.Is(err, tc.want) {
				t.Fatalf("%s on an unwired coordinator returned %v, want %v", tc.name, err, tc.want)
			}
		})
	}
}
