// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

func TestIntersects(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		a, b []string
		want bool
	}{
		{"common token", []string{"SWITCH", "WEATHER"}, []string{"WEATHER"}, true},
		{"disjoint", []string{"SWITCH"}, []string{"WEATHER"}, false},
		{"empty a", nil, []string{"SWITCH"}, false},
		{"empty b", []string{"SWITCH"}, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := intersects(tc.a, tc.b); got != tc.want {
				t.Errorf("intersects(%v,%v)=%v want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// buildRoleMatchFixture registers a source channel that can act in both
// directions plus two candidates — one pure receiver (matches the source
// as a sender) and one pure sender (matches the source as a receiver).
func buildRoleMatchFixture(t *testing.T) *LinksDomain {
	t.Helper()
	c, err := central.New(central.Config{Name: "ccu-rm"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}

	src := device.New(device.Config{
		InterfaceID: "HmIP-RF", Interface: hmenum.InterfaceHmIPRF,
		Address: "SRC", Model: "HmIP-WRC",
	})
	srcCh := src.AddChannel("SRC:1", 1, "KEY_TRANSCEIVER", hmenum.ParamsetKeyValues)
	srcCh.SetLinkRoles([]string{"SWITCH"}, []string{"WEATHER"})
	c.ModelRegistry.Put(src)

	act := device.New(device.Config{
		InterfaceID: "HmIP-RF", Interface: hmenum.InterfaceHmIPRF,
		Address: "ACT", Model: "HmIP-PS",
	})
	actCh := act.AddChannel("ACT:1", 1, "SWITCH", hmenum.ParamsetKeyValues)
	// A pure receiver: it can be a link target for a SWITCH sender.
	actCh.SetLinkRoles(nil, []string{"SWITCH"})
	c.ModelRegistry.Put(act)

	sensor := device.New(device.Config{
		InterfaceID: "HmIP-RF", Interface: hmenum.InterfaceHmIPRF,
		Address: "WSENSOR", Model: "HmIP-SWO",
	})
	senCh := sensor.AddChannel("WSENSOR:1", 1, "WEATHER_TRANSMITTER", hmenum.ParamsetKeyValues)
	// A pure sender: it can be a link source for a WEATHER receiver.
	senCh.SetLinkRoles([]string{"WEATHER"}, nil)
	c.ModelRegistry.Put(sensor)

	return NewLinksDomain(reg, client.NewValueWriter(), nil)
}

func addrsOf(chs []hmapi.LinkableChannel) map[string]bool {
	out := map[string]bool{}
	for _, ch := range chs {
		out[ch.Address] = true
	}
	return out
}

// TestLinkableChannels_RoleDirectionsDiffer is the core V02 regression:
// role=sender and role=receiver must return disjoint, correctly-filtered
// candidate sets — not the identical over-broad list of the old filter.
func TestLinkableChannels_RoleDirectionsDiffer(t *testing.T) {
	t.Parallel()
	d := buildRoleMatchFixture(t)
	ctx := context.Background()

	sender, err := d.LinkableChannels(ctx, "HmIP-RF", "SRC:1", "sender", "en")
	if err != nil {
		t.Fatalf("sender: %v", err)
	}
	receiver, err := d.LinkableChannels(ctx, "HmIP-RF", "SRC:1", "receiver", "en")
	if err != nil {
		t.Fatalf("receiver: %v", err)
	}

	s := addrsOf(sender)
	r := addrsOf(receiver)
	// Source as sender → only the receiver actuator (SWITCH target).
	if !s["ACT:1"] || s["WSENSOR:1"] {
		t.Errorf("sender set wrong: %v (want ACT:1 only)", s)
	}
	// Source as receiver → only the weather sender.
	if !r["WSENSOR:1"] || r["ACT:1"] {
		t.Errorf("receiver set wrong: %v (want WSENSOR:1 only)", r)
	}
	if len(sender) == len(receiver) && s["ACT:1"] == r["ACT:1"] && s["WSENSOR:1"] == r["WSENSOR:1"] {
		t.Error("sender and receiver returned identical sets — role filter not applied")
	}
}

// TestLinkableChannels_RolelessSourceExcludedStrict covers rule B: a
// present source channel with no roles for the requested direction can
// not act in it, so the candidate list is empty.
func TestLinkableChannels_RolelessSourceExcludedStrict(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-rm2"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}
	src := device.New(device.Config{
		InterfaceID: "HmIP-RF", Interface: hmenum.InterfaceHmIPRF,
		Address: "SRC", Model: "HmIP-PS",
	})
	// Pure receiver source: empty LinkSourceRoles → cannot be a sender.
	sc := src.AddChannel("SRC:1", 1, "SWITCH", hmenum.ParamsetKeyValues)
	sc.SetLinkRoles(nil, []string{"SWITCH"})
	c.ModelRegistry.Put(src)

	act := device.New(device.Config{
		InterfaceID: "HmIP-RF", Interface: hmenum.InterfaceHmIPRF,
		Address: "ACT", Model: "HmIP-PS",
	})
	ac := act.AddChannel("ACT:1", 1, "SWITCH", hmenum.ParamsetKeyValues)
	ac.SetLinkRoles(nil, []string{"SWITCH"})
	c.ModelRegistry.Put(act)

	d := NewLinksDomain(reg, client.NewValueWriter(), nil)
	got, err := d.LinkableChannels(context.Background(), "HmIP-RF", "SRC:1", "sender", "en")
	if err != nil {
		t.Fatalf("LinkableChannels: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("a role-less-for-direction source must yield no candidates, got %v", addrsOf(got))
	}
}

// TestLinkableChannels_EmptyRoleMatchesAll covers the WS device-level
// probe: an unrecognised role ("") preserves the permissive match-all.
func TestLinkableChannels_EmptyRoleMatchesAll(t *testing.T) {
	t.Parallel()
	d := buildRoleMatchFixture(t)
	got, err := d.LinkableChannels(context.Background(), "HmIP-RF", "SRC:1", "", "en")
	if err != nil {
		t.Fatalf("LinkableChannels: %v", err)
	}
	a := addrsOf(got)
	if !a["ACT:1"] || !a["WSENSOR:1"] {
		t.Errorf("empty role must match all candidates, got %v", a)
	}
}
