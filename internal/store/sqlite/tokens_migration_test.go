// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package sqlite

import (
	"context"
	"testing"
)

// TestMigrationFoldsLegacyTokenSubjects verifies the repair of token rows an
// older daemon wrote with the operator's own spelling: they must come out of
// the migration chain keyed on the canonical subject, the same spelling the
// users table and every per-subject store use. Without the repair an existing
// installation keeps a bearer identity that no user-side operation can address
// — it survives the purge that follows the deletion of its own account.
//
// Not marked t.Parallel(): openDBAtVersion holds the package-level openMu
// across two sequential goose calls.
func TestMigrationFoldsLegacyTokenSubjects(t *testing.T) {
	ctx := context.Background()

	// Migrate to the last revision before the repair, then plant the rows an
	// older binary would have written.
	db := openDBAtVersion(t, "tokens_subject_fold.db", 34)
	if _, err := db.ExecContext(ctx,
		`INSERT INTO tokens (fingerprint, token_hash, subject, role, created_at)
		 VALUES ('aaaaaaaaaaaa', 'hash-a', ' Admin ', 'admin', '2026-01-01T00:00:00Z'),
		        ('bbbbbbbbbbbb', 'hash-b', 'operator', 'operator', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("seed legacy token rows: %v", err)
	}

	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	rows, err := db.QueryContext(ctx, `SELECT fingerprint, subject FROM tokens ORDER BY fingerprint`)
	if err != nil {
		t.Fatalf("query tokens: %v", err)
	}
	defer func() { _ = rows.Close() }()
	got := make(map[string]string)
	for rows.Next() {
		var fp, subject string
		if err := rows.Scan(&fp, &subject); err != nil {
			t.Fatalf("scan token row: %v", err)
		}
		got[fp] = subject
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate tokens: %v", err)
	}
	want := map[string]string{
		"aaaaaaaaaaaa": "admin",
		"bbbbbbbbbbbb": "operator",
	}
	for fp, wantSubject := range want {
		if got[fp] != wantSubject {
			t.Errorf("token %s subject=%q want %q", fp, got[fp], wantSubject)
		}
	}
}
