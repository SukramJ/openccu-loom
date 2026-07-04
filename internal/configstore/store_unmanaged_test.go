// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package configstore

import (
	"context"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// TestListenSurvivesSecondBootWithStoredRESTSection is the store-level proof of
// the OPENCCU_LOOM_REST_LISTEN fix: north.rest.listen is bootstrap-tier, so it
// is never persisted into the REST section on the first boot and the
// bootstrap/env value always wins on every subsequent boot — even when a REST
// section row exists.
func TestListenSurvivesSecondBootWithStoredRESTSection(t *testing.T) {
	t.Parallel()
	sl := newFakeSectionLoader()
	ctx := context.Background()

	// First boot: seed the sections from a config whose listen is the OLD value.
	first := config.Default()
	first.North.REST.Listen = ":8119"
	first.North.REST.PublicURL = "https://a.example"
	s1 := New(&config.BootstrapConfig{
		DataDir: "/tmp/x",
		Logging: config.LoggingConfig{Level: "info", Format: "json"},
		Listen:  config.BootstrapListen{REST: ":8119"},
	}, sl, nil)
	if _, err := s1.SeedSectionsFromConfig(ctx, first, "yaml"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// The persisted REST row must not carry the bootstrap-tier listen.
	row, ok := sl.rows[string(SectionREST)]
	if !ok {
		t.Fatal("REST section was not seeded")
	}
	if strings.Contains(string(row.ValueJSON), "listen") {
		t.Errorf("REST section must not persist the bootstrap-tier listen: %s", row.ValueJSON)
	}

	// Second boot with a NEW bootstrap listen (e.g. from OPENCCU_LOOM_REST_LISTEN).
	s2 := New(&config.BootstrapConfig{
		DataDir: "/tmp/x",
		Logging: config.LoggingConfig{Level: "info", Format: "json"},
		Listen:  config.BootstrapListen{REST: ":9090"},
	}, sl, nil)
	res, err := s2.Effective(ctx)
	if err != nil {
		t.Fatalf("Effective: %v", err)
	}
	if res.Config.North.REST.Listen != ":9090" {
		t.Errorf("second-boot listen=%q want :9090 (env/bootstrap must win over stored section)", res.Config.North.REST.Listen)
	}
	if res.Sources["north.rest.listen"] != SourceBootstrap {
		t.Errorf("north.rest.listen source=%q want bootstrap", res.Sources["north.rest.listen"])
	}
	// The real REST field the operator set is still applied from the section.
	if res.Config.North.REST.PublicURL != "https://a.example" {
		t.Errorf("public_url=%q want https://a.example (real REST section field must still apply)", res.Config.North.REST.PublicURL)
	}
}

// TestOverlayIntoPreservesBootstrapListen verifies the daemon's boot overlay
// path keeps the env/YAML listen even when a stored REST section carries a
// stale one, and attributes the field to the bootstrap tier for the SPA pill.
func TestOverlayIntoPreservesBootstrapListen(t *testing.T) {
	t.Parallel()
	sl := newFakeSectionLoader()
	// A stored REST row that (from a legacy seed) still carries a stale listen.
	sl.rows[string(SectionREST)] = sqlite.SectionRow{
		Section:   string(SectionREST),
		ValueJSON: []byte(`{"listen":":7777","public_url":"https://b.example"}`),
	}
	// The YAML/env-loaded base config the daemon starts from.
	cfg := config.Default()
	cfg.North.REST.Listen = ":9999" // e.g. from OPENCCU_LOOM_REST_LISTEN

	s := New(defaultBootstrap(), sl, nil)
	srcs, err := s.OverlayInto(context.Background(), cfg)
	if err != nil {
		t.Fatalf("OverlayInto: %v", err)
	}
	if cfg.North.REST.Listen != ":9999" {
		t.Errorf("listen=%q want :9999 (stale stored section must not override env)", cfg.North.REST.Listen)
	}
	if cfg.North.REST.PublicURL != "https://b.example" {
		t.Errorf("public_url=%q want https://b.example", cfg.North.REST.PublicURL)
	}
	if srcs["north.rest.listen"] != SourceBootstrap {
		t.Errorf("north.rest.listen source=%q want bootstrap", srcs["north.rest.listen"])
	}
}

// TestRESTSectionDoesNotCarryAuthCredentials verifies auth.users / auth.tokens
// never round-trip through the REST section: marshalSection strips them so a
// seed never persists them, and applySection strips them so a stale stored row
// can neither inject nor wipe the credentials that now live only in SQLite.
func TestRESTSectionDoesNotCarryAuthCredentials(t *testing.T) {
	t.Parallel()

	// marshalSection must not emit auth credentials.
	cfg := config.Default()
	cfg.North.REST.Auth.Users = map[string]string{"admin": "hash"}
	cfg.North.REST.Auth.Tokens = map[string]string{"tok": "admin"}
	raw, ok, err := marshalSection(SectionREST, cfg)
	if err != nil || !ok {
		t.Fatalf("marshalSection: ok=%v err=%v", ok, err)
	}
	if strings.Contains(string(raw), `"users"`) || strings.Contains(string(raw), `"tokens"`) {
		t.Errorf("marshalled REST section must not carry auth credentials: %s", raw)
	}

	// applySection must not inject a stored row's auth credentials, and must
	// preserve credentials already present in the target (the section cannot
	// wipe them).
	target := config.Default()
	target.North.REST.Auth.Users = map[string]string{"existing": "keep"}
	stored := []byte(`{"cors":["https://x"],"auth":{"users":{"evil":"x"},"tokens":{"t":"admin"}}}`)
	if err := applySection(SectionREST, stored, target); err != nil {
		t.Fatalf("applySection: %v", err)
	}
	if len(target.North.REST.CORS) != 1 || target.North.REST.CORS[0] != "https://x" {
		t.Errorf("real REST field cors must apply: %#v", target.North.REST.CORS)
	}
	if _, evil := target.North.REST.Auth.Users["evil"]; evil {
		t.Errorf("stored section must not inject auth.users: %#v", target.North.REST.Auth.Users)
	}
	if got := target.North.REST.Auth.Users["existing"]; got != "keep" {
		t.Errorf("existing auth.users must be preserved (section can't wipe them): %#v", target.North.REST.Auth.Users)
	}
}

// TestUnmanagedFieldPaths pins the exact set of paths excluded from the
// editable section surface so a future struct change cannot silently expose a
// bootstrap-tier or SQLite-managed field to the section editor.
func TestUnmanagedFieldPaths(t *testing.T) {
	t.Parallel()
	got := UnmanagedFieldPaths()
	want := []string{"north.rest.listen", "north.rest.auth.users", "north.rest.auth.tokens"}
	if len(got) != len(want) {
		t.Fatalf("UnmanagedFieldPaths size=%d want %d: %v", len(got), len(want), got)
	}
	for _, p := range want {
		if _, ok := got[p]; !ok {
			t.Errorf("UnmanagedFieldPaths missing %q", p)
		}
	}
}
