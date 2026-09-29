// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"database/sql"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/SukramJ/godevccu/pkg/litefake"

	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// startOnboardFixture prepares everything the production entry reads: a
// migrated centrals store, a fake box, the /VERSION marker and the
// minted token file, wired through the same environment overrides the
// e2e harness uses.
func startOnboardFixture(t *testing.T) (*sqlitestore.CentralsStore, *litefake.Fake) {
	t.Helper()
	dir := t.TempDir()
	db, err := sql.Open(sqlitestore.DriverName, sqlitestore.FileDSN(filepath.Join(dir, "onboard.db")))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqlitestore.Migrate(context.Background(), db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := sqlitestore.NewCentralsStore(db)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	fake, err := litefake.Start(ctx, litefake.Options{})
	if err != nil {
		t.Fatalf("litefake: %v", err)
	}
	t.Cleanup(func() { _ = fake.Close() })

	version := filepath.Join(dir, "VERSION")
	if err := os.WriteFile(version, []byte("VERSION=1.0.0\nVARIANT=lite\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	token := filepath.Join(dir, "openccu-loom.api")
	// occulited writes the secret with a trailing newline.
	if err := os.WriteFile(token, []byte(litefake.DefaultToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(liteAddonVersionPathEnv, version)
	t.Setenv(liteAddonTokenPathEnv, token)
	t.Setenv(liteAddonBaseURLEnv, fake.URL())
	return store, fake
}

// TestLiteAddonOnboardingAdoptsTheLocalBox pins the first-boot promise of
// ADR 0077 through the production entry point: an openccu-lite host that
// minted this add-on a token and has no centrals configured ends up with
// the local box persisted as a central — named after the box's hostname,
// authenticated by the token file, its interfaces read from the box
// rather than assumed.
func TestLiteAddonOnboardingAdoptsTheLocalBox(t *testing.T) {
	store, fake := startOnboardFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	maybeStartLiteAddonOnboarding(ctx, store, slog.New(slog.DiscardHandler))

	var rows []sqlitestore.CentralRow
	deadline := time.Now().Add(20 * time.Second)
	for {
		var err error
		rows, err = store.List(ctx)
		if err == nil && len(rows) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no central onboarded (rows=%v err=%v)", rows, err)
		}
		time.Sleep(100 * time.Millisecond)
	}

	row := rows[0]
	if row.Name != litefake.DefaultHostname {
		t.Errorf("name = %q, want the box hostname %q", row.Name, litefake.DefaultHostname)
	}
	if row.SystemType != "openccu-lite" || !row.Enabled {
		t.Errorf("system_type/enabled = %q/%v", row.SystemType, row.Enabled)
	}
	if row.APITokenFile == "" || row.APITokenPlain != "" {
		t.Errorf("credential: token_file=%q plain=%q — the file is the credential, nothing is stored", row.APITokenFile, row.APITokenPlain)
	}
	u, err := url.Parse(fake.URL())
	if err != nil {
		t.Fatal(err)
	}
	if row.Host != u.Hostname() {
		t.Errorf("host = %q, want %q", row.Host, u.Hostname())
	}
	if wantPort := u.Port(); wantPort != "" && strconv.Itoa(row.JSONRPCPort) != wantPort {
		t.Errorf("json_rpc_port = %d, want %s", row.JSONRPCPort, wantPort)
	}
	names := make([]string, 0, len(row.Interfaces))
	for _, in := range row.Interfaces {
		names = append(names, in.Name)
	}
	want := litefake.DefaultInterfaces()
	if len(names) != len(want) {
		t.Errorf("interfaces = %v, want the box's %v", names, want)
	}
}

// TestLiteAddonOnboardingRespectsConfiguredCentrals pins the guard rail:
// as soon as any central exists — including a deleted-and-recreated
// history reduced to one row — the onboarding does nothing, so an
// operator's deletion of the auto-central is never fought.
func TestLiteAddonOnboardingRespectsConfiguredCentrals(t *testing.T) {
	store, _ := startOnboardFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	existing := sqlitestore.CentralRow{Name: "mine", Host: "192.0.2.9", Enabled: true}
	if err := store.Put(ctx, existing); err != nil {
		t.Fatalf("seed: %v", err)
	}

	maybeStartLiteAddonOnboarding(ctx, store, slog.New(slog.DiscardHandler))
	time.Sleep(2 * time.Second)

	rows, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 1 || rows[0].Name != "mine" {
		t.Errorf("rows = %v, want only the operator's central", rows)
	}
}

// TestCentralNameFromHostname pins the sanitizer: the hostname is taken
// once, reduced to the callback router's allowlist, and an unusable
// result falls back to the fixed name.
func TestCentralNameFromHostname(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"openccu-lite-fake", "openccu-lite-fake"},
		{"Wohnung 3 OG", "Wohnung-3-OG"},
		{"küche", "k-che"},
		{"---", liteAddonFallbackName},
		{"", liteAddonFallbackName},
		{"box.local", "box-local"},
	}
	for _, tc := range cases {
		if got := centralNameFromHostname(tc.in); got != tc.want {
			t.Errorf("centralNameFromHostname(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
