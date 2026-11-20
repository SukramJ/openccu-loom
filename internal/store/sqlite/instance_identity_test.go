// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package sqlite

import (
	"context"
	"regexp"
	"sync"
	"testing"
)

var instanceIDShape = regexp.MustCompile(`^[0-9a-f]{32}$`)

// TestInstanceIDIsCreatedOnceAndKept: the first call creates the identity,
// every later call returns it, and the table holds exactly one row.
func TestInstanceIDIsCreatedOnceAndKept(t *testing.T) {
	t.Parallel()
	db := openTestDB(t, "identity.db")
	ctx := context.Background()

	first, err := InstanceID(ctx, db)
	if err != nil {
		t.Fatalf("InstanceID: %v", err)
	}
	if !instanceIDShape.MatchString(first) {
		t.Fatalf("InstanceID = %q, want 32 lower-case hex digits", first)
	}
	for range 3 {
		again, err := InstanceID(ctx, db)
		if err != nil {
			t.Fatalf("InstanceID again: %v", err)
		}
		if again != first {
			t.Fatalf("InstanceID changed from %q to %q", first, again)
		}
	}
	var rows int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM instance_identity`).Scan(&rows); err != nil {
		t.Fatalf("count: %v", err)
	}
	if rows != 1 {
		t.Fatalf("instance_identity holds %d rows, want 1", rows)
	}
}

// TestInstanceIDConcurrentFirstCallsAgree: callers racing the first creation
// all get the one stored id.
func TestInstanceIDConcurrentFirstCallsAgree(t *testing.T) {
	t.Parallel()
	db := openTestDB(t, "identity-race.db")
	ctx := context.Background()

	const n = 8
	ids := make([]string, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() { ids[i], errs[i] = InstanceID(ctx, db) })
	}
	wg.Wait()
	for i := range n {
		if errs[i] != nil {
			t.Fatalf("call %d: %v", i, errs[i])
		}
		if ids[i] != ids[0] {
			t.Fatalf("call %d returned %q, call 0 %q", i, ids[i], ids[0])
		}
	}
}

// TestInstanceIDDiffersPerDatabase: two installations get two identities.
func TestInstanceIDDiffersPerDatabase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	a, err := InstanceID(ctx, openTestDB(t, "a.db"))
	if err != nil {
		t.Fatalf("InstanceID a: %v", err)
	}
	b, err := InstanceID(ctx, openTestDB(t, "b.db"))
	if err != nil {
		t.Fatalf("InstanceID b: %v", err)
	}
	if a == b {
		t.Fatalf("two databases share the identity %q", a)
	}
}
