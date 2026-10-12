// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"context"
	"regexp"
	"testing"

	matterstore "github.com/SukramJ/go-fabric/store"

	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// TestLoomMigrationsCarryEveryGoFabricMatterTable pins loom's goose
// migrations against the Matter store schema go-fabric ships: every table
// and index `store.Schema()` creates must exist after loom's migrations
// ran. The daemon owns its one database and never applies go-fabric's
// schema itself, so a table that module adds in a release is invisible
// here until a migration copies it — and the store's queries then fail at
// the first write that needs it. That is how the group tables were missing
// for a whole dependency release: every KeySetWrite answered FAILURE and no
// bridged endpoint's Groups cluster was usable.
func TestLoomMigrationsCarryEveryGoFabricMatterTable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := sqlite.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	have := map[string]bool{}
	rows, err := db.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type IN ('table','index')`)
	if err != nil {
		t.Fatalf("sqlite_master: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		have[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}

	want := regexp.MustCompile(`(?i)CREATE (?:TABLE|INDEX) (?:IF NOT EXISTS )?([a-z_]+)`).FindAllStringSubmatch(matterstore.Schema(), -1)
	if len(want) == 0 {
		t.Fatal("go-fabric store.Schema() names no table — the regexp or the schema changed shape")
	}
	for _, m := range want {
		if !have[m[1]] {
			t.Errorf("go-fabric store.Schema() creates %q, loom's migrations do not — add a goose migration under internal/store/sqlite/migrations/ copying its DDL", m[1])
		}
	}
}
