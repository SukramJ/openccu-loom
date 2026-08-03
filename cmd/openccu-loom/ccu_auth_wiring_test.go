// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/build"
	"github.com/SukramJ/openccu-loom/internal/config"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// ── parseCCURoleMapping ───────────────────────────────────────────────────────

func TestParseCCURoleMapping_ValidEntries(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.DiscardHandler)
	m := parseCCURoleMapping(map[string]string{
		"8": "admin",
		"2": "operator",
	}, logger)
	if m[8] != auth.RoleAdmin {
		t.Errorf("level 8: role = %q, want %q", m[8], auth.RoleAdmin)
	}
	if m[2] != auth.RoleOperator {
		t.Errorf("level 2: role = %q, want %q", m[2], auth.RoleOperator)
	}
}

func TestParseCCURoleMapping_InvalidKeySkipped(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.DiscardHandler)
	m := parseCCURoleMapping(map[string]string{
		"x": "admin",
		"8": "admin",
	}, logger)
	if len(m) != 1 {
		t.Errorf("len = %d, want 1 (bad key skipped)", len(m))
	}
	if m[8] != auth.RoleAdmin {
		t.Errorf("level 8: role = %q, want %q", m[8], auth.RoleAdmin)
	}
}

func TestParseCCURoleMapping_InvalidRoleSkipped(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.DiscardHandler)
	m := parseCCURoleMapping(map[string]string{
		"8": "superuser",
		"2": "operator",
	}, logger)
	if _, ok := m[8]; ok {
		t.Error("level 8 should have been skipped (bad role)")
	}
	if m[2] != auth.RoleOperator {
		t.Errorf("level 2: role = %q, want %q", m[2], auth.RoleOperator)
	}
}

func TestParseCCURoleMapping_EmptyInputReturnsNil(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.DiscardHandler)
	m := parseCCURoleMapping(nil, logger)
	if m != nil {
		t.Errorf("expected nil for empty input, got %v", m)
	}
}

// ── loginChainWithCCU ─────────────────────────────────────────────────────────

// stubUserStore is a minimal [auth.UserStore] that returns a fixed identity
// for one username and ErrUnauthenticated for all others.
type stubUserStore struct {
	name     string
	username string
	identity auth.Identity
}

func (s *stubUserStore) AuthenticateBasic(_ context.Context, username, _ string) (auth.Identity, error) {
	if username == s.username {
		return s.identity, nil
	}
	return auth.Identity{}, auth.ErrUnauthenticated
}

func TestLoginChainWithCCU_NoCCU_LocalChainOnly(t *testing.T) {
	t.Parallel()
	sqUsers := &stubUserStore{
		name:     "sqlite",
		username: "alice",
		identity: auth.Identity{Subject: "alice", Scheme: auth.SchemeBasic, Role: auth.RoleAdmin},
	}
	memUsers := &stubUserStore{
		name:     "mem",
		username: "bob",
		identity: auth.Identity{Subject: "bob", Scheme: auth.SchemeBasic, Role: auth.RoleOperator},
	}
	chain := loginChainWithCCU(sqUsers, memUsers, nil, false)

	id, err := chain.AuthenticateBasic(context.Background(), "alice", "pass")
	if err != nil || id.Subject != "alice" {
		t.Errorf("sqlite user: got (%v, %v), want alice/nil", id, err)
	}
	id, err = chain.AuthenticateBasic(context.Background(), "bob", "pass")
	if err != nil || id.Subject != "bob" {
		t.Errorf("mem user: got (%v, %v), want bob/nil", id, err)
	}
	_, err = chain.AuthenticateBasic(context.Background(), "carol", "pass")
	if !errors.Is(err, auth.ErrUnauthenticated) {
		t.Errorf("unknown user: got %v, want ErrUnauthenticated", err)
	}
}

func TestLoginChainWithCCU_WithCCU_LocalTakesPrecedence(t *testing.T) {
	t.Parallel()
	sqUsers := &stubUserStore{
		name:     "sqlite",
		username: "alice",
		identity: auth.Identity{Subject: "alice", Scheme: auth.SchemeBasic, Role: auth.RoleAdmin},
	}
	memUsers := &stubUserStore{
		name:     "mem",
		username: "bob",
		identity: auth.Identity{Subject: "bob", Scheme: auth.SchemeBasic, Role: auth.RoleOperator},
	}
	// CCU recognises alice with a lower role; local should win.
	ccuStore := &stubUserStore{
		name:     "ccu",
		username: "alice",
		identity: auth.Identity{Subject: "alice", Scheme: auth.SchemeBasic, Role: auth.RoleViewer},
	}
	chain := loginChainWithCCU(sqUsers, memUsers, ccuStore, false)

	id, err := chain.AuthenticateBasic(context.Background(), "alice", "pass")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id.Role != auth.RoleAdmin {
		t.Errorf("local should win: role = %q, want %q", id.Role, auth.RoleAdmin)
	}
}

func TestLoginChainWithCCU_WithCCU_FallsThroughToCCU(t *testing.T) {
	t.Parallel()
	sqUsers := &stubUserStore{
		name:     "sqlite",
		username: "alice",
		identity: auth.Identity{Subject: "alice", Scheme: auth.SchemeBasic, Role: auth.RoleAdmin},
	}
	memUsers := &stubUserStore{
		name:     "mem",
		username: "bob",
		identity: auth.Identity{Subject: "bob", Scheme: auth.SchemeBasic, Role: auth.RoleOperator},
	}
	ccuStore := &stubUserStore{
		name:     "ccu",
		username: "carol",
		identity: auth.Identity{Subject: "carol", Scheme: auth.SchemeBasic, Role: auth.RoleViewer},
	}
	chain := loginChainWithCCU(sqUsers, memUsers, ccuStore, false)

	id, err := chain.AuthenticateBasic(context.Background(), "carol", "pass")
	if err != nil {
		t.Fatalf("unexpected error for CCU-only user: %v", err)
	}
	if id.Subject != "carol" {
		t.Errorf("subject = %q, want %q", id.Subject, "carol")
	}
	if id.Role != auth.RoleViewer {
		t.Errorf("role = %q, want %q", id.Role, auth.RoleViewer)
	}
}

func TestLoginChainWithCCU_WithCCU_UnknownUserDenied(t *testing.T) {
	t.Parallel()
	sqUsers := &stubUserStore{name: "sqlite", username: "alice"}
	memUsers := &stubUserStore{name: "mem", username: "bob"}
	ccuStore := &stubUserStore{name: "ccu", username: "carol"}
	chain := loginChainWithCCU(sqUsers, memUsers, ccuStore, false)

	_, err := chain.AuthenticateBasic(context.Background(), "dave", "pass")
	if !errors.Is(err, auth.ErrUnauthenticated) {
		t.Errorf("unknown user: got %v, want ErrUnauthenticated", err)
	}
}

// ── ccuAuthEnabled ────────────────────────────────────────────────────────────

func ptrBool(b bool) *bool { return &b }

func TestCCUAuthEnabled_ExplicitTrue(t *testing.T) {
	t.Parallel()
	cc := config.CCUAuthConfig{Enabled: ptrBool(true)}
	if !ccuAuthEnabled(cc) {
		t.Error("expected true when Enabled=&true")
	}
}

func TestCCUAuthEnabled_ExplicitFalse(t *testing.T) {
	t.Parallel()
	cc := config.CCUAuthConfig{Enabled: ptrBool(false)}
	if ccuAuthEnabled(cc) {
		t.Error("expected false when Enabled=&false")
	}
}

func TestCCUAuthEnabled_NilFallsBackToBuildStamp(t *testing.T) {
	t.Parallel()
	cc := config.CCUAuthConfig{Enabled: nil}
	got := ccuAuthEnabled(cc)
	if got != build.IsAddon() {
		t.Errorf("nil Enabled: got %v, want %v (build.IsAddon())", got, build.IsAddon())
	}
}

// ── ccuAuthPrimary ────────────────────────────────────────────────────────────

func TestCCUAuthPrimary_NilDefaultsToTrue(t *testing.T) {
	t.Parallel()
	cc := config.CCUAuthConfig{Primary: nil}
	if !ccuAuthPrimary(cc) {
		t.Error("expected true when Primary=nil")
	}
}

func TestCCUAuthPrimary_ExplicitFalse(t *testing.T) {
	t.Parallel()
	cc := config.CCUAuthConfig{Primary: ptrBool(false)}
	if ccuAuthPrimary(cc) {
		t.Error("expected false when Primary=&false")
	}
}

func TestCCUAuthPrimary_ExplicitTrue(t *testing.T) {
	t.Parallel()
	cc := config.CCUAuthConfig{Primary: ptrBool(true)}
	if !ccuAuthPrimary(cc) {
		t.Error("expected true when Primary=&true")
	}
}

// ── loginChainWithCCU primary=true ────────────────────────────────────────────

func TestLoginChainWithCCU_Primary_CCUWinsForSharedUser(t *testing.T) {
	t.Parallel()
	// Both stores know "alice" but return different roles.
	sqUsers := &stubUserStore{
		name:     "sqlite",
		username: "alice",
		identity: auth.Identity{Subject: "alice-local", Scheme: auth.SchemeBasic, Role: auth.RoleAdmin},
	}
	memUsers := &stubUserStore{name: "mem", username: "nobody"}
	ccuStore := &stubUserStore{
		name:     "ccu",
		username: "alice",
		identity: auth.Identity{Subject: "alice-ccu", Scheme: auth.SchemeBasic, Role: auth.RoleViewer},
	}
	chain := loginChainWithCCU(sqUsers, memUsers, ccuStore, true)

	id, err := chain.AuthenticateBasic(context.Background(), "alice", "pass")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id.Subject != "alice-ccu" {
		t.Errorf("primary=true: subject = %q, want %q (CCU wins)", id.Subject, "alice-ccu")
	}
}

func TestLoginChainWithCCU_Primary_LocalBreakGlassWhenCCURejectsUser(t *testing.T) {
	t.Parallel()
	// Only the local store knows "alice"; CCU returns ErrUnauthenticated.
	sqUsers := &stubUserStore{
		name:     "sqlite",
		username: "alice",
		identity: auth.Identity{Subject: "alice", Scheme: auth.SchemeBasic, Role: auth.RoleAdmin},
	}
	memUsers := &stubUserStore{name: "mem", username: "nobody"}
	ccuStore := &stubUserStore{name: "ccu", username: "nobody"}
	chain := loginChainWithCCU(sqUsers, memUsers, ccuStore, true)

	id, err := chain.AuthenticateBasic(context.Background(), "alice", "pass")
	if err != nil {
		t.Fatalf("break-glass fallback failed: %v", err)
	}
	if id.Subject != "alice" {
		t.Errorf("subject = %q, want %q", id.Subject, "alice")
	}
}

func TestLoginChainWithCCU_Primary_NilCCUPureLoca(t *testing.T) {
	t.Parallel()
	sqUsers := &stubUserStore{
		name:     "sqlite",
		username: "alice",
		identity: auth.Identity{Subject: "alice", Scheme: auth.SchemeBasic, Role: auth.RoleAdmin},
	}
	memUsers := &stubUserStore{name: "mem", username: "nobody"}
	chain := loginChainWithCCU(sqUsers, memUsers, nil, true)

	id, err := chain.AuthenticateBasic(context.Background(), "alice", "pass")
	if err != nil {
		t.Fatalf("unexpected error with nil CCU: %v", err)
	}
	if id.Subject != "alice" {
		t.Errorf("subject = %q, want %q", id.Subject, "alice")
	}
}

// ── newCCUAuthCentralResolver ─────────────────────────────────────────────────

// buildCentralsTestStore opens a fresh migrated SQLite DB and returns its
// *sqlitestore.CentralsStore, mirroring buildPurgeTestStores's
// gooseMigrateMu-guarded open pattern (central_adopt_test.go) to avoid
// goose's migration race when tests run in parallel across the package.
func buildCentralsTestStore(t *testing.T) *sqlitestore.CentralsStore {
	t.Helper()
	ctx := context.Background()

	dsn := "file:" + t.TempDir() + "/ccu_auth_resolver_test.db?_pragma=journal_mode(WAL)"
	gooseMigrateMu.Lock()
	db, err := sqlitestore.Open(ctx, dsn)
	gooseMigrateMu.Unlock()
	if err != nil {
		t.Fatalf("sqlitestore.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	return sqlitestore.NewCentralsStore(db)
}

// TestCCUAuthCentralResolverNoStoreFallback verifies that with a nil
// centrals store the resolver falls back to the boot-time cfg.Centrals
// snapshot, replicating the pre-store centralConfig rule: named hit,
// empty name selects the first entry, unknown name misses.
func TestCCUAuthCentralResolverNoStoreFallback(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{Centrals: []config.CentralConfig{
		{Name: "ccu1", Host: "192.0.2.1"},
		{Name: "ccu2", Host: "192.0.2.2"},
	}}
	resolve := newCCUAuthCentralResolver(cfg, nil)

	if cc, ok := resolve(context.Background(), "ccu2"); !ok || cc.Host != "192.0.2.2" {
		t.Errorf("named hit: got %+v ok=%v", cc, ok)
	}
	if cc, ok := resolve(context.Background(), ""); !ok || cc.Name != "ccu1" {
		t.Errorf("empty name: expected first entry ccu1, got %+v ok=%v", cc, ok)
	}
	if _, ok := resolve(context.Background(), "unknown"); ok {
		t.Error("unknown name: expected miss")
	}
}

// TestCCUAuthCentralResolverStoreBackedRuntimeAdopted verifies the
// headline PR4 behaviour: a central that exists ONLY in the SQLite
// centrals store (simulating a runtime adopt that never touched
// cfg.Centrals) resolves successfully by name.
func TestCCUAuthCentralResolverStoreBackedRuntimeAdopted(t *testing.T) {
	t.Parallel()
	store := buildCentralsTestStore(t)
	ctx := context.Background()

	row := sqlitestore.CentralRow{Name: "runtime-ccu", Host: "192.0.2.50", Enabled: true}
	if err := store.Put(ctx, row); err != nil {
		t.Fatalf("Put: %v", err)
	}

	// cfg.Centrals intentionally does NOT contain "runtime-ccu" — it was
	// adopted live after boot.
	cfg := &config.Config{Centrals: []config.CentralConfig{{Name: "boot-ccu", Host: "192.0.2.1"}}}
	resolve := newCCUAuthCentralResolver(cfg, store)

	cc, ok := resolve(ctx, "runtime-ccu")
	if !ok {
		t.Fatal("expected runtime-adopted central to resolve via the store")
	}
	if cc.Host != "192.0.2.50" {
		t.Errorf("expected host 192.0.2.50, got %q", cc.Host)
	}
}

// TestCCUAuthCentralResolverStoreEmptyNameFirstEnabled verifies that an
// empty name resolves to the first ENABLED row (store.List order),
// skipping any disabled row.
func TestCCUAuthCentralResolverStoreEmptyNameFirstEnabled(t *testing.T) {
	t.Parallel()
	store := buildCentralsTestStore(t)
	ctx := context.Background()

	// "a-disabled" sorts before "b-enabled" (store.List orders by name),
	// so an implementation that ignores Enabled would pick the wrong one.
	if err := store.Put(ctx, sqlitestore.CentralRow{Name: "a-disabled", Host: "192.0.2.9", Enabled: false}); err != nil {
		t.Fatalf("Put(a-disabled): %v", err)
	}
	if err := store.Put(ctx, sqlitestore.CentralRow{Name: "b-enabled", Host: "192.0.2.10", Enabled: true}); err != nil {
		t.Fatalf("Put(b-enabled): %v", err)
	}

	resolve := newCCUAuthCentralResolver(&config.Config{}, store)
	cc, ok := resolve(ctx, "")
	if !ok {
		t.Fatal("expected an enabled default central to resolve")
	}
	if cc.Name != "b-enabled" {
		t.Errorf("expected first enabled row b-enabled, got %q", cc.Name)
	}
}

// TestCCUAuthCentralResolverStoreDisabledFailsClosed verifies that a
// disabled central (present in the store) fails closed by name, and
// that a store with no enabled centrals fails closed for an empty name.
func TestCCUAuthCentralResolverStoreDisabledFailsClosed(t *testing.T) {
	t.Parallel()
	store := buildCentralsTestStore(t)
	ctx := context.Background()

	if err := store.Put(ctx, sqlitestore.CentralRow{Name: "parked", Host: "192.0.2.20", Enabled: false}); err != nil {
		t.Fatalf("Put: %v", err)
	}

	resolve := newCCUAuthCentralResolver(&config.Config{}, store)
	if _, ok := resolve(ctx, "parked"); ok {
		t.Error("expected a disabled central to fail closed by name")
	}
	if _, ok := resolve(ctx, ""); ok {
		t.Error("expected empty name to fail closed when no central is enabled")
	}
}

// TestCCUAuthCentralResolverStoreUnknownNameFailsClosed verifies a name
// absent from a POPULATED store fails closed rather than falling back to
// cfg.Centrals — once the centrals table is in use it is the sole source
// of truth for named lookups.
func TestCCUAuthCentralResolverStoreUnknownNameFailsClosed(t *testing.T) {
	t.Parallel()
	store := buildCentralsTestStore(t)
	ctx := context.Background()

	if err := store.Put(ctx, sqlitestore.CentralRow{Name: "db-ccu", Host: "192.0.2.30", Enabled: true}); err != nil {
		t.Fatalf("Put: %v", err)
	}

	cfg := &config.Config{Centrals: []config.CentralConfig{{Name: "yaml-ccu", Host: "192.0.2.1"}}}
	resolve := newCCUAuthCentralResolver(cfg, store)

	if _, ok := resolve(ctx, "yaml-ccu"); ok {
		t.Error("expected a name absent from the populated store to fail closed even though it exists in cfg.Centrals")
	}
}

// TestCCUAuthCentralResolverEmptyStoreFallsBackToYAML pins the tier rule
// configstore.layerCentrals applies at boot: an EMPTY centrals table means
// the DB tier is unused, so the YAML-configured centrals are authoritative.
// Without this a config.yaml-only deployment (no SPA-adopted central) could
// never authenticate anyone against the CCU — the resolver failed closed on
// a store that simply had nothing to say.
func TestCCUAuthCentralResolverEmptyStoreFallsBackToYAML(t *testing.T) {
	t.Parallel()
	store := buildCentralsTestStore(t)
	cfg := &config.Config{Centrals: []config.CentralConfig{
		{Name: "yaml-ccu", Host: "192.0.2.1"},
		{Name: "yaml-ccu2", Host: "192.0.2.2"},
	}}
	resolve := newCCUAuthCentralResolver(cfg, store)

	if cc, ok := resolve(context.Background(), "yaml-ccu"); !ok || cc.Host != "192.0.2.1" {
		t.Errorf("named YAML central: got %+v ok=%v, want host 192.0.2.1", cc, ok)
	}
	if cc, ok := resolve(context.Background(), ""); !ok || cc.Name != "yaml-ccu" {
		t.Errorf("empty name: expected the first YAML central, got %+v ok=%v", cc, ok)
	}
	if _, ok := resolve(context.Background(), "unknown"); ok {
		t.Error("unknown name: expected a miss even with the YAML fallback in play")
	}
}
