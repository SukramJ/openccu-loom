// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// payload_timestamp_layout_test.go — pins the fixed-width timestamp spelling
// of the client state payload. Lives in the `client` package so it can stamp
// the last-failure / last-callback instants directly.

package client

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestStateTimestampLayoutIsFixedWidth stamps both timestamps with an instant
// whose fractional second is exactly half a second and reads them back through
// State(). time.RFC3339Nano would render ".5Z"; the layout this payload uses
// pads to nine digits.
func TestStateTimestampLayoutIsFixedWidth(t *testing.T) {
	t.Parallel()

	nop := CallerFunc(func(_ context.Context, _ string, _ []any) (any, error) {
		return nil, nil
	})
	ic, err := New(Config{
		CentralName: "test",
		Interface:   hmenum.InterfaceHmIPRF,
		Caller:      nop,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer ic.Close()

	half := time.Date(2026, time.March, 4, 5, 6, 7, 500000000, time.UTC)
	ic.failureMu.Lock()
	ic.lastFailureAt = half
	ic.failureMu.Unlock()
	ic.callbackMu.Lock()
	ic.lastCallbackAt = half
	ic.callbackMu.Unlock()

	st, ok := ic.State().(*payload.InterfaceClientState)
	if !ok {
		t.Fatalf("State() returned %T, want *payload.InterfaceClientState", ic.State())
	}

	const want = "2026-03-04T05:06:07.500000000Z"
	if st.LastFailureAt != want {
		t.Errorf("LastFailureAt = %q, want %q (nine fractional digits, not RFC3339Nano)",
			st.LastFailureAt, want)
	}
	if st.LastCallbackAt != want {
		t.Errorf("LastCallbackAt = %q, want %q (nine fractional digits, not RFC3339Nano)",
			st.LastCallbackAt, want)
	}
	if !strings.HasSuffix(st.LastFailureAt, ".500000000Z") {
		t.Errorf("LastFailureAt %q lost its fixed-width fraction", st.LastFailureAt)
	}
}
