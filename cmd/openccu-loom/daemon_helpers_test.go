// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

// daemon_coverage9_test.go — low-hanging coverage wins (Welle HH):
//   - splitListenPort: valid IPv4, IPv6, port-only, port 65535, port 0,
//     out-of-range port, non-numeric port, unix socket path, empty string
//   - buildRateLimitConfig: disabled (nil) and enabled (non-nil) paths
//   - runtimeCapabilityDetector: HasMQTTDiscovery, HasMatterBridge, HasOIDC
//   - newLoggerStack / newFullLoggerStack: valid, with overrides, invalid override
//   - systemCCUAdapter.List + interfaceNames: nil fields, empty centrals,
//     central absent from registry, central present in registry
//   - wireValuesCacheStore: nil cfg, disabled-by-config, bad DataDir
//   - newValuesCacheHandlerAdapter: nil store, non-nil store
//   - valuesCacheHandlerAdapter.DeleteAll / DeleteDevice / Stats / Metrics
//   - newDeviceLookupAdapter: nil reg, non-nil reg
//   - deviceLookupAdapter.LocateDevice: nil receiver, empty registry,
//     registry with central but no matching device
//   - caseResumptionStoreAdapter.GetByID: nil manager

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/SukramJ/go-fabric/secure/operational"
	matterstore "github.com/SukramJ/go-fabric/store"

	"github.com/SukramJ/openccu-loom/internal/config"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// ── splitListenPort ───────────────────────────────────────────────────────────

func TestSplitListenPort_ColonPort(t *testing.T) {
	t.Parallel()
	p, ok := splitListenPort(":8119")
	if !ok || p != 8119 {
		t.Errorf("splitListenPort(%q) = (%d, %v), want (8119, true)", ":8119", p, ok)
	}
}

func TestSplitListenPort_IPv4(t *testing.T) {
	t.Parallel()
	p, ok := splitListenPort("0.0.0.0:9000")
	if !ok || p != 9000 {
		t.Errorf("splitListenPort(%q) = (%d, %v), want (9000, true)", "0.0.0.0:9000", p, ok)
	}
}

func TestSplitListenPort_IPv6(t *testing.T) {
	t.Parallel()
	p, ok := splitListenPort("[::]:1234")
	if !ok || p != 1234 {
		t.Errorf("splitListenPort(%q) = (%d, %v), want (1234, true)", "[::]:1234", p, ok)
	}
}

func TestSplitListenPort_MaxPort(t *testing.T) {
	t.Parallel()
	p, ok := splitListenPort(":65535")
	if !ok || p != 65535 {
		t.Errorf("splitListenPort(%q) = (%d, %v), want (65535, true)", ":65535", p, ok)
	}
}

func TestSplitListenPort_PortZero_FalseOK(t *testing.T) {
	t.Parallel()
	_, ok := splitListenPort(":0")
	if ok {
		t.Error("splitListenPort(:0) should return ok=false for port 0")
	}
}

func TestSplitListenPort_OutOfRange_FalseOK(t *testing.T) {
	t.Parallel()
	_, ok := splitListenPort(":65536")
	if ok {
		t.Error("splitListenPort(:65536) should return ok=false for out-of-range port")
	}
}

func TestSplitListenPort_NonNumericPort_FalseOK(t *testing.T) {
	t.Parallel()
	_, ok := splitListenPort(":abc")
	if ok {
		t.Error("splitListenPort(:abc) should return ok=false")
	}
}

func TestSplitListenPort_UnixSocketPath_FalseOK(t *testing.T) {
	t.Parallel()
	// A bare path without a colon cannot be split by net.SplitHostPort.
	_, ok := splitListenPort("/run/openccu-loom.sock")
	if ok {
		t.Error("splitListenPort(unix socket path) should return ok=false")
	}
}

func TestSplitListenPort_Empty_FalseOK(t *testing.T) {
	t.Parallel()
	_, ok := splitListenPort("")
	if ok {
		t.Error("splitListenPort(\"\") should return ok=false")
	}
}

// ── buildRateLimitConfig ──────────────────────────────────────────────────────

func TestBuildRateLimitConfig_Disabled_ReturnsNil(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.North.REST.RateLimit.Enabled = false
	if got := buildRateLimitConfig(cfg); got != nil {
		t.Errorf("expected nil when rate limiting disabled, got %+v", got)
	}
}

func TestBuildRateLimitConfig_Enabled_ReturnsConfig(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.North.REST.RateLimit.Enabled = true
	cfg.North.REST.RateLimit.RequestsPerSecond = 50.0
	cfg.North.REST.RateLimit.Burst = 100
	got := buildRateLimitConfig(cfg)
	if got == nil {
		t.Fatal("expected non-nil rate limit config when enabled")
	}
	if got.RequestsPerSecond != 50.0 {
		t.Errorf("RequestsPerSecond: got %v, want 50.0", got.RequestsPerSecond)
	}
	if got.Burst != 100 {
		t.Errorf("Burst: got %d, want 100", got.Burst)
	}
}

// ── runtimeCapabilityDetector ─────────────────────────────────────────────────

func TestRuntimeCapabilityDetector_AllFalse(t *testing.T) {
	t.Parallel()
	d := runtimeCapabilityDetector{mqtt: false, matter: false, oidc: false}
	if d.HasMQTTDiscovery() {
		t.Error("HasMQTTDiscovery should be false")
	}
	if d.HasMatterBridge() {
		t.Error("HasMatterBridge should be false")
	}
	if d.HasOIDC() {
		t.Error("HasOIDC should be false")
	}
}

func TestRuntimeCapabilityDetector_AllTrue(t *testing.T) {
	t.Parallel()
	d := runtimeCapabilityDetector{mqtt: true, matter: true, oidc: true}
	if !d.HasMQTTDiscovery() {
		t.Error("HasMQTTDiscovery should be true")
	}
	if !d.HasMatterBridge() {
		t.Error("HasMatterBridge should be true")
	}
	if !d.HasOIDC() {
		t.Error("HasOIDC should be true")
	}
}

func TestRuntimeCapabilityDetector_MixedCapabilities(t *testing.T) {
	t.Parallel()
	d := runtimeCapabilityDetector{mqtt: true, matter: false, oidc: true}
	if !d.HasMQTTDiscovery() {
		t.Error("HasMQTTDiscovery should be true")
	}
	if d.HasMatterBridge() {
		t.Error("HasMatterBridge should be false")
	}
	if !d.HasOIDC() {
		t.Error("HasOIDC should be true")
	}
}

// ── newLoggerStack / newFullLoggerStack ───────────────────────────────────────

func TestNewLoggerStack_ValidConfig_ReturnsLogger(t *testing.T) {
	t.Parallel()
	lc := config.LoggingConfig{Level: "info", Format: "json"}
	logger, levels, err := newLoggerStack(lc, io.Discard)
	if err != nil {
		t.Fatalf("newLoggerStack: %v", err)
	}
	if logger == nil {
		t.Error("expected non-nil logger")
	}
	if levels == nil {
		t.Error("expected non-nil LevelRegistry")
	}
}

func TestNewLoggerStack_ValidOverrides_Applies(t *testing.T) {
	t.Parallel()
	lc := config.LoggingConfig{
		Level:  "info",
		Format: "text",
		Overrides: map[string]string{
			"some.subsystem": "debug",
		},
	}
	var buf bytes.Buffer
	logger, levels, err := newLoggerStack(lc, &buf)
	if err != nil {
		t.Fatalf("newLoggerStack with overrides: %v", err)
	}
	if logger == nil || levels == nil {
		t.Error("expected non-nil logger and levels")
	}
}

func TestNewFullLoggerStack_InvalidOverride_ReturnsError(t *testing.T) {
	t.Parallel()
	// "trace" is not a valid slog level — ApplyConfig should return an error.
	lc := config.LoggingConfig{
		Level:  "info",
		Format: "json",
		Overrides: map[string]string{
			"subsystem.x": "trace",
		},
	}
	_, err := newFullLoggerStack(lc, io.Discard)
	if err == nil {
		t.Fatal("expected error for invalid override level 'trace'")
	}
}

func TestNewFullLoggerStack_NoOverrides_NoError(t *testing.T) {
	t.Parallel()
	lc := config.LoggingConfig{Level: "warn", Format: "json"}
	stack, err := newFullLoggerStack(lc, io.Discard)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stack.Logger == nil {
		t.Error("expected non-nil stack.Logger")
	}
}

// ── newCallbackAllowlist ─────────────────────────────────────────────────────

// TestCallbackAllowlistRestrictDisabledReturnsNil verifies the
// default open-LAN behaviour: with RestrictSourceIPs unset (false), the
// allowlist is nil (accept-all) regardless of configured centrals.
func TestCallbackAllowlistRestrictDisabledReturnsNil(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.Callback.RestrictSourceIPs = false
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-01", Host: "192.168.1.50"}}
	logger := slog.New(slog.DiscardHandler)

	got := newCallbackAllowlist(context.Background(), cfg, nil, logger)
	if got != nil {
		t.Errorf("expected nil allowlist when RestrictSourceIPs=false, got %v", got)
	}
}

// TestCallbackAllowlistIncludesLoopbackAndCentralIPLiteral
// verifies that enabling RestrictSourceIPs always seeds the allowlist with
// loopback (IPv4 + IPv6) and adds a /32 for every central whose Host is an
// IP literal. Hostnames are deliberately not exercised here — they would
// route through net.LookupIP and make the test depend on DNS.
func TestCallbackAllowlistIncludesLoopbackAndCentralIPLiteral(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.Callback.RestrictSourceIPs = true
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-01", Host: "192.168.1.50"}}
	logger := slog.New(slog.DiscardHandler)

	got := resolveTestAllowlist(context.Background(), cfg, nil, logger)

	want := []netip.Prefix{
		netip.MustParsePrefix("127.0.0.0/8"),
		netip.MustParsePrefix("::1/128"),
		netip.MustParsePrefix("192.168.1.50/32"),
	}
	if len(got) != len(want) {
		t.Fatalf("allowlist = %v, want exactly %v", got, want)
	}
	for _, w := range want {
		found := false
		for _, g := range got {
			if g == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("allowlist %v is missing expected prefix %v", got, w)
		}
	}
}

// TestCallbackAllowlistWithoutCentralsStillIncludesLoopback
// verifies loopback is always present even with zero configured centrals —
// a co-located CCU pushing from 127.0.0.1 must keep working.
func TestCallbackAllowlistWithoutCentralsStillIncludesLoopback(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.Callback.RestrictSourceIPs = true
	cfg.Centrals = nil
	logger := slog.New(slog.DiscardHandler)

	got := resolveTestAllowlist(context.Background(), cfg, nil, logger)
	want := []netip.Prefix{
		netip.MustParsePrefix("127.0.0.0/8"),
		netip.MustParsePrefix("::1/128"),
	}
	if len(got) != len(want) {
		t.Fatalf("allowlist = %v, want exactly %v", got, want)
	}
	for _, w := range want {
		found := false
		for _, g := range got {
			if g == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("allowlist %v is missing expected prefix %v", got, w)
		}
	}
}

// resolveTestAllowlist returns one resolution of the live callback allowlist,
// which is what the listeners read on every accepted connection.
func resolveTestAllowlist(ctx context.Context, cfg *config.Config, centrals *sqlitestore.CentralsStore, logger *slog.Logger) []netip.Prefix {
	return (&callbackAllowlist{cfg: cfg, centrals: centrals, logger: logger}).resolve(ctx)
}

// TestCallbackAllowlistPicksUpACentralAddedAtRuntime pins the reason the
// allowlist is resolved per connection instead of captured at boot: adopting a
// CCU through the admin surface is an explicitly restart-free operation, and
// it writes the SQLite centrals row without touching cfg.Centrals. A listener
// holding the boot-time prefix set drops every callback from that CCU — at
// DEBUG level, with no polling fallback — so the central comes up, reports
// live, and then never updates a value until the daemon is restarted.
func TestCallbackAllowlistPicksUpACentralAddedAtRuntime(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	db := openMigratedTestDB(t, "callback_allowlist.db")
	centrals := sqlitestore.NewCentralsStore(db)

	cfg := config.Default()
	cfg.Callback.RestrictSourceIPs = true
	cfg.Centrals = nil

	allowlist := newLiveCallbackAllowlist(ctx, cfg, centrals, slog.New(slog.DiscardHandler), 5*time.Millisecond)
	if allowlist == nil {
		t.Fatal("newLiveCallbackAllowlist returned nil with RestrictSourceIPs enabled")
	}
	adopted := netip.MustParsePrefix("192.0.2.77/32")
	if slices.Contains(allowlist(), adopted) {
		t.Fatal("precondition: the adopted CCU must not be allowed before its row exists")
	}

	if err := centrals.Put(ctx, sqlitestore.CentralRow{
		Name: "ccu-adopted", Host: "192.0.2.77", Enabled: true,
	}); err != nil {
		t.Fatalf("centrals.Put: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for !slices.Contains(allowlist(), adopted) {
		if time.Now().After(deadline) {
			t.Fatalf("allowlist = %v, want it to contain %v; a CCU adopted at runtime "+
				"is blackholed until the daemon restarts", allowlist(), adopted)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// TestCallbackAllowlistSkipsDisabledCentralRows verifies a central the
// operator switched off does not keep its push privilege.
func TestCallbackAllowlistSkipsDisabledCentralRows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	db := openMigratedTestDB(t, "callback_allowlist_disabled.db")
	centrals := sqlitestore.NewCentralsStore(db)
	if err := centrals.Put(ctx, sqlitestore.CentralRow{
		Name: "ccu-off", Host: "192.0.2.9", Enabled: false,
	}); err != nil {
		t.Fatalf("centrals.Put: %v", err)
	}

	cfg := config.Default()
	cfg.Callback.RestrictSourceIPs = true
	got := resolveTestAllowlist(ctx, cfg, centrals, slog.New(slog.DiscardHandler))
	if slices.Contains(got, netip.MustParsePrefix("192.0.2.9/32")) {
		t.Errorf("allowlist %v includes a disabled central's host", got)
	}
}

// ── interfaceNames ────────────────────────────────────────────────────────────

func TestInterfaceNames_EmptyInterfaces(t *testing.T) {
	t.Parallel()
	cc := config.CentralConfig{Name: "ccu", Interfaces: nil}
	got := interfaceNames(cc)
	if len(got) != 0 {
		t.Errorf("expected empty slice, got %v", got)
	}
}

func TestInterfaceNames_MultipleInterfaces(t *testing.T) {
	t.Parallel()
	cc := config.CentralConfig{
		Name: "ccu",
		Interfaces: []config.InterfaceSpec{
			{Name: "HmIP-RF"},
			{Name: "BidCos-RF"},
			{Name: "CUxD"},
		},
	}
	got := interfaceNames(cc)
	if len(got) != 3 {
		t.Fatalf("expected 3 interface names, got %d: %v", len(got), got)
	}
	if got[0] != "HmIP-RF" || got[1] != "BidCos-RF" || got[2] != "CUxD" {
		t.Errorf("unexpected interface names: %v", got)
	}
}

// ── systemCCUAdapter.List ─────────────────────────────────────────────────────

func TestSystemCCUAdapter_List_NilReceiverFields_ReturnsNil(t *testing.T) {
	t.Parallel()
	a := &systemCCUAdapter{reg: nil, resolve: nil}
	got := a.List(context.Background())
	if got != nil {
		t.Errorf("expected nil for nil reg, got %v", got)
	}
}

func TestSystemCCUAdapter_List_EmptyRegistry_EmptyResult(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t)
	a := newSystemCCUAdapter(reg, resolverFromCentrals())
	got := a.List(context.Background())
	if len(got) != 0 {
		t.Errorf("expected empty result for no registered centrals, got %v", got)
	}
}

// TestSystemCCUAdapter_List_UnresolvableCentral_StillIncluded asserts a
// central present in the live registry but unresolvable via the config
// resolver (e.g. a store race, or disabled between registration and this
// call) still emits an entry — with an empty Host and no configured
// interfaces — rather than being dropped from the list.
func TestSystemCCUAdapter_List_UnresolvableCentral_StillIncluded(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "orphan-ccu")
	a := newSystemCCUAdapter(reg, resolverFromCentrals())
	got := a.List(context.Background())
	if len(got) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(got))
	}
	if got[0].Available {
		t.Error("expected Available=false for a freshly-registered unit with no health data")
	}
	if got[0].Name != "orphan-ccu" {
		t.Errorf("Name: got %q, want %q", got[0].Name, "orphan-ccu")
	}
	if got[0].Host != "" || len(got[0].ConfiguredInterfaces) != 0 {
		t.Errorf("expected empty Host/ConfiguredInterfaces for an unresolvable central, got %+v", got[0])
	}
}

// TestSystemCCUAdapter_List_CentralInRegistry_FieldsPopulated asserts a
// registered central resolvable via the live config source surfaces its
// Host and ConfiguredInterfaces from the resolver.
func TestSystemCCUAdapter_List_CentralInRegistry_FieldsPopulated(t *testing.T) {
	t.Parallel()
	const centralName = "registered-ccu"
	reg := buildTestRegistry(t, centralName)
	resolve := resolverFromCentrals(config.CentralConfig{
		Name: centralName,
		Host: "10.0.0.1",
		Interfaces: []config.InterfaceSpec{
			{Name: "HmIP-RF"},
			{Name: "BidCos-RF"},
		},
	})
	a := newSystemCCUAdapter(reg, resolve)
	got := a.List(context.Background())
	if len(got) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(got))
	}
	if got[0].Name != centralName {
		t.Errorf("Name: got %q, want %q", got[0].Name, centralName)
	}
	if got[0].Host != "10.0.0.1" {
		t.Errorf("Host: got %q, want %q", got[0].Host, "10.0.0.1")
	}
	if len(got[0].ConfiguredInterfaces) != 2 {
		t.Errorf("ConfiguredInterfaces len: got %d, want 2", len(got[0].ConfiguredInterfaces))
	}
}

// TestSystemCCUAdapter_List_SortedByName_RegistrationOrderIndependent
// pins the ordering guarantee: entries come out sorted by central name
// (mirroring [central.Registry.List]) regardless of registration order —
// this is what keeps the runtime-adopted-central case deterministic.
func TestSystemCCUAdapter_List_SortedByName_RegistrationOrderIndependent(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "zulu", "alpha", "mike")
	a := newSystemCCUAdapter(reg, resolverFromCentrals())
	got := a.List(context.Background())
	if len(got) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(got))
	}
	names := []string{got[0].Name, got[1].Name, got[2].Name}
	want := []string{"alpha", "mike", "zulu"}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("entries[%d].Name = %q, want %q (got order %v)", i, names[i], want[i], names)
			break
		}
	}
}

// ── wireValuesCacheStore ──────────────────────────────────────────────────────

func TestWireValuesCacheStore_NilConfig_ReturnsNil(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.DiscardHandler)
	got := wireValuesCacheStore(nil, logger)
	if got != nil {
		t.Error("expected nil for nil config")
	}
}

func TestWireValuesCacheStore_DisabledByConfig_ReturnsNil(t *testing.T) {
	t.Parallel()
	disabled := false
	cfg := config.Default()
	cfg.Persistence.ValuesCache.Enabled = &disabled
	logger := slog.New(slog.DiscardHandler)
	got := wireValuesCacheStore(cfg, logger)
	if got != nil {
		t.Error("expected nil when values cache explicitly disabled")
	}
}

func TestWireValuesCacheStore_BadDataDir_ReturnsNil(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	// Place a regular file where the directory would be so sqlite.Open fails.
	tmp := t.TempDir()
	blockingFile := filepath.Join(tmp, "openccu-loom-blocked")
	if err := os.WriteFile(blockingFile, []byte("block"), 0o644); err != nil {
		t.Skipf("could not create blocking file: %v", err)
	}
	// DataDir points at the blocking file, not a directory — sqlite cannot
	// create openccu-loom.db inside a file.
	cfg.DataDir = blockingFile
	logger := slog.New(slog.DiscardHandler)
	gooseMigrateMu.Lock()
	got := wireValuesCacheStore(cfg, logger)
	gooseMigrateMu.Unlock()
	if got != nil {
		t.Error("expected nil when DataDir is a regular file")
	}
}

// ── newValuesCacheHandlerAdapter ──────────────────────────────────────────────

func TestNewValuesCacheHandlerAdapter_NilStore_ReturnsNil(t *testing.T) {
	t.Parallel()
	got := newValuesCacheHandlerAdapter(nil)
	if got != nil {
		t.Error("expected nil for nil store")
	}
}

func TestNewValuesCacheHandlerAdapter_NonNilStore_ReturnsAdapter(t *testing.T) {
	t.Parallel()
	store := openTestValuesCacheStore(t)
	got := newValuesCacheHandlerAdapter(store)
	if got == nil {
		t.Error("expected non-nil adapter for non-nil store")
	}
}

// ── valuesCacheHandlerAdapter methods ─────────────────────────────────────────

func TestValuesCacheHandlerAdapter_DeleteAll_NoError(t *testing.T) {
	t.Parallel()
	store := openTestValuesCacheStore(t)
	a := newValuesCacheHandlerAdapter(store)
	if err := a.DeleteAll(context.Background()); err != nil {
		t.Fatalf("DeleteAll: %v", err)
	}
}

func TestValuesCacheHandlerAdapter_DeleteDevice_NoError(t *testing.T) {
	t.Parallel()
	store := openTestValuesCacheStore(t)
	a := newValuesCacheHandlerAdapter(store)
	if err := a.DeleteDevice(context.Background(), "test-central", "HmIP-RF", "00012345ABCDEF:0"); err != nil {
		t.Fatalf("DeleteDevice: %v", err)
	}
}

func TestValuesCacheHandlerAdapter_Stats_ReturnsValue(t *testing.T) {
	t.Parallel()
	store := openTestValuesCacheStore(t)
	a := newValuesCacheHandlerAdapter(store)
	stats, err := a.Stats(context.Background())
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.Rows < 0 {
		t.Errorf("Stats.Rows should be >= 0, got %d", stats.Rows)
	}
}

func TestValuesCacheHandlerAdapter_Metrics_ReturnsValue(t *testing.T) {
	t.Parallel()
	store := openTestValuesCacheStore(t)
	a := newValuesCacheHandlerAdapter(store)
	// Zero-value counters on a fresh store; just confirm no panic.
	_ = a.Metrics()
}

// ── newDeviceLookupAdapter ────────────────────────────────────────────────────

func TestNewDeviceLookupAdapter_NilReg_ReturnsNil(t *testing.T) {
	t.Parallel()
	got := newDeviceLookupAdapter(nil)
	if got != nil {
		t.Error("expected nil for nil registry")
	}
}

func TestNewDeviceLookupAdapter_NonNilReg_ReturnsAdapter(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t)
	got := newDeviceLookupAdapter(reg)
	if got == nil {
		t.Error("expected non-nil adapter for non-nil registry")
	}
}

// ── deviceLookupAdapter.LocateDevice ─────────────────────────────────────────

func TestDeviceLookupAdapter_LocateDevice_NilReceiver_ReturnsFalse(t *testing.T) {
	t.Parallel()
	var a *deviceLookupAdapter
	_, _, ok := a.LocateDevice("00012345:0")
	if ok {
		t.Error("nil receiver: expected ok=false")
	}
}

func TestDeviceLookupAdapter_LocateDevice_EmptyRegistry_ReturnsFalse(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t)
	a := newDeviceLookupAdapter(reg)
	_, _, ok := a.LocateDevice("00012345:0")
	if ok {
		t.Error("empty registry: expected ok=false")
	}
}

func TestDeviceLookupAdapter_LocateDevice_RegistryWithCentralNoDevice_ReturnsFalse(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "ccu-01")
	a := newDeviceLookupAdapter(reg)
	// Registry has a central but its ModelRegistry has no matching device address.
	_, _, ok := a.LocateDevice("00012345:0")
	if ok {
		t.Error("central with empty model registry: expected ok=false")
	}
}

// ── caseResumptionStoreAdapter.GetByID: nil manager ──────────────────────────

func TestCaseResumptionStoreAdapter_GetByID_NilManager_ReturnsNilNil(t *testing.T) {
	t.Parallel()
	a := caseResumptionStoreAdapter{mgr: nil}
	rec, err := a.GetByID([]byte{0x01, 0x02, 0x03})
	if err != nil {
		t.Errorf("expected nil error for nil manager, got %v", err)
	}
	if rec != nil {
		t.Errorf("expected nil record for nil manager, got %v", rec)
	}
}

// TestCaseResumptionStoreAdapter_GetByID_MapsFabricPeerAndCATsThrough
// verifies that GetByID's manual field-by-field copy
// (caseResumptionStoreAdapter.GetByID in daemon_matter.go) carries
// FabricIndex, PeerNodeID and PeerCATs through from the persisted
// operational.Manager record into the sigma.ResumptionRecord the
// resume path consumes — without these the Sigma2_Resume path in
// sigma.Responder.tryResume would adopt a zero-value fabric/peer
// identity for every resumed session.
func TestCaseResumptionStoreAdapter_GetByID_MapsFabricPeerAndCATsThrough(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	// buildTestOperationalManager + matterStoreFromManager open TWO
	// separate SQLite files (the latter is a store-only parallel DB for
	// tests that don't need manager+store to share state) — the
	// matter_resumption FOREIGN KEY on fabric_index requires the fabric
	// row to live in the SAME database the manager persists into, so
	// this test wires its own store+manager pair against one DSN.
	db := openMigratedTestDB(t, "matter_resumption_adapter_test.db")
	store := matterstore.New(db)
	mgr := operational.NewManager(store)

	resumptionID := bytes.Repeat([]byte{0x11}, 16)
	sharedSecret := bytes.Repeat([]byte{0x22}, 32)
	const fabricIndex uint8 = 3
	const peerNodeID uint64 = 0x1122334455667788
	cats := []uint32{0xAABBCCDD}

	// matter_resumption.fabric_index carries a FOREIGN KEY to
	// matter_fabrics(fabric_index) — the fabric row must exist before
	// PersistResumption can insert.
	if _, err := store.AddFabric(ctx, matterstore.FabricRecord{
		FabricIndex:   fabricIndex,
		FabricID:      0xF0F0F0F0,
		NodeID:        peerNodeID,
		RootPublicKey: bytes.Repeat([]byte{0x04}, 65),
	}); err != nil {
		t.Fatalf("AddFabric: %v", err)
	}

	if err := mgr.PersistResumption(ctx, fabricIndex, peerNodeID, resumptionID, sharedSecret, cats); err != nil {
		t.Fatalf("PersistResumption: %v", err)
	}

	a := caseResumptionStoreAdapter{mgr: mgr}
	rec, err := a.GetByID(resumptionID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if rec == nil {
		t.Fatal("expected non-nil record for a persisted resumption id")
	}
	if rec.FabricIndex != fabricIndex {
		t.Errorf("FabricIndex=%d, want %d", rec.FabricIndex, fabricIndex)
	}
	if rec.PeerNodeID != peerNodeID {
		t.Errorf("PeerNodeID=%#x, want %#x", rec.PeerNodeID, peerNodeID)
	}
	if len(rec.PeerCATs) != len(cats) {
		t.Fatalf("PeerCATs length=%d, want %d", len(rec.PeerCATs), len(cats))
	}
	for i, want := range cats {
		if rec.PeerCATs[i] != want {
			t.Errorf("PeerCATs[%d]=%#x, want %#x", i, rec.PeerCATs[i], want)
		}
	}
	if !bytes.Equal(rec.SharedSecret, sharedSecret) {
		t.Errorf("SharedSecret=%x, want %x", rec.SharedSecret, sharedSecret)
	}
	if !bytes.Equal(rec.ResumptionID, resumptionID) {
		t.Errorf("ResumptionID=%x, want %x", rec.ResumptionID, resumptionID)
	}
}

// ── shared test helper ────────────────────────────────────────────────────────

// openTestValuesCacheStore opens a real SQLite-backed ValuesCacheStore in a
// temp directory and registers cleanup. Returns nil and calls t.Skip when
// SQLite is unavailable.
func openTestValuesCacheStore(t *testing.T) *sqlitestore.ValuesCacheStore {
	t.Helper()
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	logger := slog.New(slog.DiscardHandler)
	gooseMigrateMu.Lock()
	store := wireValuesCacheStore(cfg, logger)
	gooseMigrateMu.Unlock()
	if store == nil {
		t.Skip("wireValuesCacheStore returned nil (SQLite unavailable in this env)")
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}
