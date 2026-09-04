// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package codes

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/clock"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// A perms_json document one projection of the column rejects must not
// be one the other accepts: the authentication candidates and the Row
// listing read the same column through one parser.
//
// The Rows leg is what pins the shared parser — a parser that swallowed
// the decode error would list the row. The Validate leg is a standing
// property rather than a second measurement: an undecodable permission
// set denies the verb either way, because the zero-value Perms grants
// nothing.
func TestMalformedPermsJSONIsRejectedByBothProjections(t *testing.T) {
	row := pinRow(t, "c1", "Markus", "1234", false, Perms{Disarm: true}, nil)
	row.PermsJSON = "{"
	store := &fakeStore{rows: []sqlitestore.AlarmCodeRow{row}}
	f := New(Deps{Store: store, Clock: clock.NewFake(time.Unix(0, 0))})
	ctx := context.Background()

	if identity, _, err := f.Validate(ctx, "zone-1", "disarm", "1234", "rest-operator"); err == nil {
		t.Errorf("Validate accepted a code with malformed perms_json (identity=%q)", identity)
	}

	rows, err := f.Rows(ctx)
	if err != nil {
		t.Fatalf("Rows: %v", err)
	}
	for _, r := range rows {
		if r.ID == "c1" {
			t.Errorf("Rows listed the row with malformed perms_json: %+v", r)
		}
	}
}
