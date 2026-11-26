// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package sqlite

import (
	"context"
	"slices"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/audit"
)

// TestAuditStoreKeepsOnBehalfOf pins the round trip of the asserted actor
// beside the principal, on both read paths.
func TestAuditStoreKeepsOnBehalfOf(t *testing.T) {
	s := freshAuditStore(t)
	ctx := context.Background()
	if err := s.Append(ctx, audit.Entry{
		User: "homematicip_local", OnBehalfOf: "Alex (ha:abc)",
		Action: audit.ActionParamsetWrite, DeviceAddress: "A1",
	}); err != nil {
		t.Fatal(err)
	}
	listed, err := s.List(ctx, "", 10)
	if err != nil || len(listed) != 1 {
		t.Fatalf("List = %v, %v", listed, err)
	}
	queried, err := s.Query(ctx, audit.Query{Limit: 10})
	if err != nil || len(queried) != 1 {
		t.Fatalf("Query = %v, %v", queried, err)
	}
	for _, e := range []audit.Entry{listed[0], queried[0]} {
		if e.User != "homematicip_local" || e.OnBehalfOf != "Alex (ha:abc)" {
			t.Fatalf("row = user %q on_behalf_of %q", e.User, e.OnBehalfOf)
		}
	}
}

// TestAuditStoreDeviceExactKeepsOneDevice pins that the exact device filter
// returns the device and its channels and nothing that merely shares the
// prefix — the leak the prefix filter would open for a device-scoped read.
func TestAuditStoreDeviceExactKeepsOneDevice(t *testing.T) {
	s := freshAuditStore(t)
	ctx := context.Background()
	for _, addr := range []string{"ABC1", "ABC1:3", "ABC12", "ABC12:1", "XABC1"} {
		if err := s.Append(ctx, audit.Entry{Action: audit.ActionParamsetWrite, DeviceAddress: addr}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Query(ctx, audit.Query{DeviceExact: "ABC1", Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	addrs := make([]string, 0, len(got))
	for _, e := range got {
		addrs = append(addrs, e.DeviceAddress)
	}
	slices.Sort(addrs)
	if !slices.Equal(addrs, []string{"ABC1", "ABC1:3"}) {
		t.Fatalf("DeviceExact ABC1 = %v, want [ABC1 ABC1:3]", addrs)
	}
}
