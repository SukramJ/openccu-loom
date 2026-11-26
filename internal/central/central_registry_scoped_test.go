// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package central

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/pkg/hmreqctx"
)

// TestRegistryHoldsDeviceCountsTheInbox pins that a device the CCU offers for
// pairing counts as held: POST /devices/{addr}/accept names its central, and
// the device-route guard would refuse it with 404 if only the model counted.
func TestRegistryHoldsDeviceCountsTheInbox(t *testing.T) {
	r := NewRegistry()
	c, err := New(Config{Name: "ccu-a"})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Register(c); err != nil {
		t.Fatal(err)
	}
	if c.HubModel == nil || c.HubModel.Inbox == nil {
		t.Fatal("New built a central without an inbox")
	}
	c.HubModel.Inbox.Replace([]hub.InboxDevice{{Address: "NEW0001"}})

	if known, holds := r.HoldsDevice("ccu-a", "NEW0001"); !known || !holds {
		t.Fatalf("inbox device: known %v holds %v, want true true", known, holds)
	}
	if known, holds := r.HoldsDevice("ccu-a", "NEW0001:1"); !known || !holds {
		t.Fatalf("inbox device by channel address: known %v holds %v, want true true", known, holds)
	}
	if known, holds := r.HoldsDevice("ccu-a", "OTHER"); !known || holds {
		t.Fatalf("absent device: known %v holds %v, want true false", known, holds)
	}
	if known, _ := r.HoldsDevice("ghost", "NEW0001"); known {
		t.Fatal("unknown central reported as known")
	}
}

func scopedNames(units []*Unit) []string {
	out := make([]string, 0, len(units))
	for _, u := range units {
		out = append(out, u.Name())
	}
	return out
}

// TestRegistryScoped pins the three answers a request-path address lookup
// depends on: every unit when unscoped, exactly the bound unit when scoped,
// and nothing — never a fallback to another CCU — when the bound central is
// not registered.
func TestRegistryScoped(t *testing.T) {
	r := NewRegistry()
	for _, name := range []string{"ccu-b", "ccu-a"} {
		c, err := New(Config{Name: name})
		if err != nil {
			t.Fatalf("New(%s): %v", name, err)
		}
		if err := r.Register(c); err != nil {
			t.Fatalf("Register(%s): %v", name, err)
		}
	}

	if got := scopedNames(r.Scoped(context.Background())); len(got) != 2 || got[0] != "ccu-a" || got[1] != "ccu-b" {
		t.Fatalf("unscoped = %v, want [ccu-a ccu-b]", got)
	}
	ctx := hmreqctx.WithCentralScope(context.Background(), "ccu-b")
	if got := scopedNames(r.Scoped(ctx)); len(got) != 1 || got[0] != "ccu-b" {
		t.Fatalf("scoped to ccu-b = %v, want [ccu-b]", got)
	}
	ghost := hmreqctx.WithCentralScope(context.Background(), "ghost")
	if got := r.Scoped(ghost); len(got) != 0 {
		t.Fatalf("scoped to an unknown central = %v, want none", scopedNames(got))
	}
}

// TestWithCentralScopeEmptyLeavesUnscoped pins that an empty name is no
// scope: a guard that forwards an absent query parameter must not bind the
// request to "".
func TestWithCentralScopeEmptyLeavesUnscoped(t *testing.T) {
	ctx := hmreqctx.WithCentralScope(context.Background(), "")
	if name, ok := hmreqctx.CentralScope(ctx); ok {
		t.Fatalf("CentralScope after empty bind = %q, true; want unscoped", name)
	}
}
