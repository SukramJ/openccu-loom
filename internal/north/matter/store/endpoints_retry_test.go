// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package store

import (
	"errors"
	"fmt"
	"testing"
)

// TestIsSQLiteBusy pins the classification that drives the bounded
// retry in UpsertEndpointAssigning: a SQLite BUSY / locked error (which
// endpoint assembly can hit when the device-load pipeline writes
// concurrently during a busy boot) is retryable; every other error is
// surfaced immediately.
func TestIsSQLiteBusy(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{
			"modernc busy verbatim",
			errors.New("matter store: upsert endpoint assigning: insert: database is locked (5) (SQLITE_BUSY)"),
			true,
		},
		{
			"wrapped busy",
			fmt.Errorf("assign new id: %w", errors.New("database is locked")),
			true,
		},
		{"table locked", errors.New("database table is locked: matter_endpoints"), true},
		{"uppercase code", errors.New("insert: SQLITE_BUSY"), true},
		{"unrelated", errors.New("constraint failed: UNIQUE"), false},
		{"not found", errors.New("sql: no rows in result set"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isSQLiteBusy(tc.err); got != tc.want {
				t.Fatalf("isSQLiteBusy(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
