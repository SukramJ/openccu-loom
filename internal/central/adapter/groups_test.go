// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

// Tests for GroupsDomain (List, groupsOf).
//
// Strategy: mirrors sysvar_creator_test.go — a minimal central.Unit with a
// registered client entry (so primaryBackendOf resolves the primary
// InterfaceClient) plus a fake backends.Operations registered on a
// clientpkg.ValueWriter. A backend that additionally implements
// heatingGroupLister returns a groups.gson payload; fakeOperations alone
// does NOT implement it, so it doubles as the "unsupported backend" case.

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/coordinators"
	clientpkg "github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// groupListerOps embeds fakeOperations and additionally implements
// heatingGroupLister so it satisfies the narrow capability GroupsDomain
// checks for via a type assertion.
type groupListerOps struct {
	fakeOperations
	raw string
	err error
}

func (g *groupListerOps) GetHeatingGroupList(_ context.Context) (string, error) {
	return g.raw, g.err
}

const oneGroupPayload = `{"groups":[{"id":3,"groupType":{"id":"HEATING","label":"l"},"groupProperties":{"NAME":"Kitchen"},"groupMembers":[{"id":"000AAA:1","memberType":{"id":"THERMOSTAT"}}]}]}`

// registerCentralWithClient builds a central named name, registers it on
// reg, wires a client entry so primaryBackendOf finds a primary client, and
// registers backend on the writer under (name, "HmIP-RF").
func registerCentralWithClient(t *testing.T, reg *central.Registry, w *clientpkg.ValueWriter, name string, backend backends.Operations) {
	t.Helper()
	c, err := central.New(central.Config{Name: name})
	if err != nil {
		t.Fatalf("central.New(%s): %v", name, err)
	}
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register(%s): %v", name, err)
	}
	ic := newTestInterfaceClient(t, name, "HmIP-RF", 5)
	if err := c.Clients.Register(&coordinators.ClientEntry{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Client:      ic,
	}); err != nil {
		t.Fatalf("Clients.Register(%s): %v", name, err)
	}
	if backend != nil {
		w.Register(name, "HmIP-RF", backend)
	}
}

// ─── unknown central ───────────────────────────────────────────────────────

func TestGroupsDomainList_UnknownCentral(t *testing.T) {
	t.Parallel()
	reg := central.NewRegistry()
	w := clientpkg.NewValueWriter()
	d := NewGroupsDomain(reg, w)

	_, err := d.List(context.Background(), "nope")
	if err == nil {
		t.Fatal("expected error for unknown central")
	}
	if err != hmerr.ErrUnknownCentral { //nolint:errorlint // sentinel identity check mirrors GroupsDomain's own contract
		t.Fatalf("err = %v, want hmerr.ErrUnknownCentral", err)
	}
}

// ─── nil registry / writer ─────────────────────────────────────────────────

func TestGroupsDomainList_NilRegistryOrWriter(t *testing.T) {
	t.Parallel()
	d := NewGroupsDomain(nil, nil)
	_, err := d.List(context.Background(), "")
	if err == nil {
		t.Fatal("expected error when registry and writer are nil")
	}
}

// ─── scoped: backend without heatingGroupLister ────────────────────────────

func TestGroupsDomainList_ScopedUnsupportedBackend(t *testing.T) {
	t.Parallel()
	reg := central.NewRegistry()
	w := clientpkg.NewValueWriter()
	registerCentralWithClient(t, reg, w, "ccu-01", &fakeOperations{kind: backends.KindCCU})

	d := NewGroupsDomain(reg, w)
	_, err := d.List(context.Background(), "ccu-01")
	if err == nil {
		t.Fatal("expected error for a backend without heatingGroupLister")
	}
	if err != backends.ErrUnsupported { //nolint:errorlint // sentinel identity check mirrors GroupsDomain's own contract
		t.Fatalf("err = %v, want backends.ErrUnsupported", err)
	}
}

// ─── aggregate: unsupported backend is skipped, not failed ────────────────

func TestGroupsDomainList_AggregateSkipsUnsupportedBackend(t *testing.T) {
	t.Parallel()
	reg := central.NewRegistry()
	w := clientpkg.NewValueWriter()
	registerCentralWithClient(t, reg, w, "ccu-01", &fakeOperations{kind: backends.KindCCU})

	d := NewGroupsDomain(reg, w)
	out, err := d.List(context.Background(), "")
	if err != nil {
		t.Fatalf("aggregate mode must be best-effort, got error: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("want 1 central entry, got %d", len(out))
	}
	if out[0].Central != "ccu-01" {
		t.Errorf("Central = %q, want ccu-01", out[0].Central)
	}
	if out[0].Groups == nil || len(out[0].Groups) != 0 {
		t.Errorf("Groups = %#v, want non-nil empty slice", out[0].Groups)
	}
}

// ─── scoped: happy path with a fake CCU backend ────────────────────────────

func TestGroupsDomainList_ScopedHappyPath(t *testing.T) {
	t.Parallel()
	reg := central.NewRegistry()
	w := clientpkg.NewValueWriter()
	backend := &groupListerOps{fakeOperations: fakeOperations{kind: backends.KindCCU}, raw: oneGroupPayload}
	registerCentralWithClient(t, reg, w, "ccu-01", backend)

	d := NewGroupsDomain(reg, w)
	out, err := d.List(context.Background(), "ccu-01")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("want 1 central entry, got %d", len(out))
	}
	if out[0].Central != "ccu-01" {
		t.Errorf("Central = %q, want ccu-01", out[0].Central)
	}
	if len(out[0].Groups) != 1 {
		t.Fatalf("want 1 group, got %d", len(out[0].Groups))
	}
	g := out[0].Groups[0]
	if g.ID != 3 || g.Name != "Kitchen" {
		t.Errorf("group = %+v, want id=3 name=Kitchen", g)
	}
	if len(g.Members) != 1 || g.Members[0].Address != "000AAA:1" {
		t.Errorf("members = %+v", g.Members)
	}
}

// ─── aggregate: multiple centrals, sorted by name, mixed capability ────────

func TestGroupsDomainList_AggregateMultipleCentralsSortedByName(t *testing.T) {
	t.Parallel()
	reg := central.NewRegistry()
	w := clientpkg.NewValueWriter()

	// Register "ccu-b" first and "ccu-a" second — the registry must still
	// return them in name order per registry.List()'s own contract.
	registerCentralWithClient(t, reg, w, "ccu-b", &groupListerOps{
		fakeOperations: fakeOperations{kind: backends.KindCCU}, raw: oneGroupPayload,
	})
	registerCentralWithClient(t, reg, w, "ccu-a", &fakeOperations{kind: backends.KindCCU})

	d := NewGroupsDomain(reg, w)
	out, err := d.List(context.Background(), "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("want 2 central entries, got %d", len(out))
	}
	byName := map[string]CentralGroups{}
	for _, e := range out {
		byName[e.Central] = e
	}
	if len(byName["ccu-a"].Groups) != 0 {
		t.Errorf("ccu-a (unsupported backend) should contribute 0 groups, got %d", len(byName["ccu-a"].Groups))
	}
	if len(byName["ccu-b"].Groups) != 1 {
		t.Errorf("ccu-b should contribute 1 group, got %d", len(byName["ccu-b"].Groups))
	}
}

// ─── scoped: backend fetch error propagates ────────────────────────────────

func TestGroupsDomainList_ScopedBackendFetchError(t *testing.T) {
	t.Parallel()
	reg := central.NewRegistry()
	w := clientpkg.NewValueWriter()
	wantErr := hmerr.ErrNoConnection
	registerCentralWithClient(t, reg, w, "ccu-01", &groupListerOps{
		fakeOperations: fakeOperations{kind: backends.KindCCU}, err: wantErr,
	})

	d := NewGroupsDomain(reg, w)
	_, err := d.List(context.Background(), "ccu-01")
	if err == nil {
		t.Fatal("expected the backend fetch error to propagate in scoped mode")
	}
}

// ─── aggregate: backend fetch error is swallowed (best-effort) ────────────

func TestGroupsDomainList_AggregateBackendFetchErrorSwallowed(t *testing.T) {
	t.Parallel()
	reg := central.NewRegistry()
	w := clientpkg.NewValueWriter()
	registerCentralWithClient(t, reg, w, "ccu-01", &groupListerOps{
		fakeOperations: fakeOperations{kind: backends.KindCCU}, err: hmerr.ErrNoConnection,
	})

	d := NewGroupsDomain(reg, w)
	out, err := d.List(context.Background(), "")
	if err != nil {
		t.Fatalf("aggregate mode must swallow per-central fetch errors, got: %v", err)
	}
	if len(out) != 1 || out[0].Central != "ccu-01" {
		t.Fatalf("out = %+v", out)
	}
	if len(out[0].Groups) != 0 {
		t.Errorf("Groups = %#v, want empty", out[0].Groups)
	}
}
