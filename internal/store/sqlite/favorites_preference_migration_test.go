// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package sqlite

import (
	"context"
	"testing"

	"github.com/pressly/goose/v3"
)

// TestFavoritesPreferenceIsDroppedOnUpgrade pins that the upgrade removes
// the stored pinned-items lists and nothing else: a preference a client
// still reads has to survive the same statement.
func TestFavoritesPreferenceIsDroppedOnUpgrade(t *testing.T) {
	ctx := context.Background()
	db := openDBAtVersion(t, "favorites.db", 51)

	for _, row := range []struct{ subject, key string }{
		{"alice", "favorites"},
		{"alice", "start_route"},
		{"bob", "favorites"},
	} {
		if _, err := db.ExecContext(
			ctx,
			`INSERT INTO user_preferences (subject, key, value_json, updated_unix) VALUES (?, ?, '[]', 1)`,
			row.subject, row.key,
		); err != nil {
			t.Fatalf("seed %s/%s: %v", row.subject, row.key, err)
		}
	}

	openMu.Lock()
	err := goose.UpToContext(ctx, db, "migrations", 52)
	openMu.Unlock()
	if err != nil {
		t.Fatalf("migrate to 52: %v", err)
	}

	rows, err := db.QueryContext(ctx, `SELECT subject, key FROM user_preferences ORDER BY subject, key`)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var got []string
	for rows.Next() {
		var subject, key string
		if err := rows.Scan(&subject, &key); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, subject+"/"+key)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if len(got) != 1 || got[0] != "alice/start_route" {
		t.Errorf("preferences after the upgrade = %v, want only alice/start_route", got)
	}
}
