// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package contract

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"sort"
	"strings"
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

	for _, name := range goFabricMatterTables(t) {
		if !have[name] {
			t.Errorf("go-fabric store.Schema() creates %q, loom's migrations do not — add a goose migration under internal/store/sqlite/migrations/ copying its DDL", name)
		}
	}
}

// TestLoomMigrationsMatchGoFabricMatterTableShapes pins the shape of every
// hand-carried table — columns, their types, NOT NULL, defaults, primary
// key members and foreign keys — against the database go-fabric's own
// `store.Apply` produces, which includes its `Upgrade` steps. A table that
// exists with the wrong constraint passes the table-level pin above and
// still fails at a live write: go-fabric 0.3.0 dropped the foreign key from
// matter_group_key_map to matter_group_keys because a commissioner writes
// the GroupKeyMap before the key set it names, and the hand-carried copy
// kept the constraint, so that write answered FAILURE again.
func TestLoomMigrationsMatchGoFabricMatterTableShapes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	loom, err := sqlite.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = loom.Close() })

	ref, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open reference sqlite: %v", err)
	}
	t.Cleanup(func() { _ = ref.Close() })
	ref.SetMaxOpenConns(1)
	if err := matterstore.Apply(ctx, ref); err != nil {
		t.Fatalf("go-fabric store.Apply: %v", err)
	}

	for _, table := range goFabricMatterTables(t) {
		want := tableShape(ctx, t, ref, table)
		got := tableShape(ctx, t, loom, table)
		if want != got {
			t.Errorf("table %s differs from go-fabric store.Apply():\n--- go-fabric\n%s\n--- loom migrations\n%s\nadd a goose migration under internal/store/sqlite/migrations/ that rebuilds the table to go-fabric's shape", table, want, got)
		}
	}
}

// goFabricMatterTables lists the tables and indexes go-fabric's schema
// creates, in the schema's order.
func goFabricMatterTables(t *testing.T) []string {
	t.Helper()
	want := regexp.MustCompile(`(?i)CREATE (?:TABLE|INDEX) (?:IF NOT EXISTS )?([a-z_]+)`).FindAllStringSubmatch(matterstore.Schema(), -1)
	if len(want) == 0 {
		t.Fatal("go-fabric store.Schema() names no table — the regexp or the schema changed shape")
	}
	names := make([]string, 0, len(want))
	for _, m := range want {
		names = append(names, m[1])
	}
	return names
}

type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// tableShape renders a table's columns and foreign keys as SQLite's own
// pragmas report them, one line each, sorted, so two databases compare
// by content rather than by DDL text. An index name yields an empty shape
// on both sides; its presence is the other test's concern.
func tableShape(ctx context.Context, t *testing.T, db querier, table string) string {
	t.Helper()
	var lines []string
	collect := func(query string, scan func(rows *sql.Rows) (string, error)) {
		rows, err := db.QueryContext(ctx, query)
		if err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		defer rows.Close()
		for rows.Next() {
			line, err := scan(rows)
			if err != nil {
				t.Fatalf("scan %s: %v", query, err)
			}
			lines = append(lines, line)
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("rows %s: %v", query, err)
		}
	}
	collect(fmt.Sprintf(`SELECT name, type, "notnull", COALESCE(dflt_value, ''), pk FROM pragma_table_info(%q)`, table), func(rows *sql.Rows) (string, error) {
		var name, typ, dflt string
		var notnull, pk int
		if err := rows.Scan(&name, &typ, &notnull, &dflt, &pk); err != nil {
			return "", err
		}
		return fmt.Sprintf("column %s %s notnull=%d default=%s pk=%d", name, strings.ToUpper(typ), notnull, dflt, pk), nil
	})
	collect(fmt.Sprintf(`SELECT "table", "from", "to", on_update, on_delete FROM pragma_foreign_key_list(%q)`, table), func(rows *sql.Rows) (string, error) {
		var ref, from, to, onUpdate, onDelete string
		if err := rows.Scan(&ref, &from, &to, &onUpdate, &onDelete); err != nil {
			return "", err
		}
		return fmt.Sprintf("foreign key %s -> %s.%s on_update=%s on_delete=%s", from, ref, to, onUpdate, onDelete), nil
	})
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}
