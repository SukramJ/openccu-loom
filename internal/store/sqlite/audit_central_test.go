// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package sqlite

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/audit"
)

// TestAuditStoreKeepsCentral pins the round trip of a row's recorded central
// on both read paths: it is what the boot-time hydration replays into the
// in-memory buffer, so a central lost here is lost at the next restart. A row
// recorded without one reads back empty, never as some default.
func TestAuditStoreKeepsCentral(t *testing.T) {
	s := freshAuditStore(t)
	ctx := context.Background()
	for _, e := range []audit.Entry{
		{Action: audit.ActionParamsetWrite, DeviceAddress: "HmIP-RCV-1"},
		{Action: audit.ActionParamsetWrite, Central: "ccu-02", DeviceAddress: "HmIP-RCV-1"},
	} {
		if err := s.Append(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	listed, err := s.List(ctx, "", 10)
	if err != nil || len(listed) != 2 {
		t.Fatalf("List = %v, %v", listed, err)
	}
	queried, err := s.Query(ctx, audit.Query{Limit: 10})
	if err != nil || len(queried) != 2 {
		t.Fatalf("Query = %v, %v", queried, err)
	}
	for _, rows := range [][]audit.Entry{listed, queried} {
		// Newest first.
		if rows[0].Central != "ccu-02" || rows[1].Central != "" {
			t.Fatalf("centrals = %q, %q; want ccu-02, empty", rows[0].Central, rows[1].Central)
		}
	}
}
