// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/cachereset"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// newTestConfig builds a two-central topology used by the offline
// scope-expansion tests: ccu1 has HmIP-RF + BidCos-RF, ccu2 has HmIP-RF.
func newTestConfig() *config.Config {
	return &config.Config{
		Centrals: []config.CentralConfig{
			{
				Name: "ccu1",
				Interfaces: []config.InterfaceSpec{
					{Name: "HmIP-RF"},
					{Name: "BidCos-RF"},
				},
			},
			{
				Name: "ccu2",
				Interfaces: []config.InterfaceSpec{
					{Name: "HmIP-RF"},
				},
			},
		},
	}
}

// ─── scope validation ─────────────────────────────────────────────────────────

func TestCacheClearUnknownScopeReturnsError(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	err := run([]string{"cache", "clear", "--scope", "bogus"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for unknown scope")
	}
	if !strings.Contains(err.Error(), "scope") {
		t.Errorf("error should mention scope, got: %v", err)
	}
}

func TestCacheClearCentralScopeRequiresCentral(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	err := run([]string{"cache", "clear", "--scope", "central"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error when --central missing for scope=central")
	}
	if !strings.Contains(err.Error(), "central") {
		t.Errorf("error should mention central, got: %v", err)
	}
}

func TestCacheClearInterfaceScopeRequiresBothQualifiers(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	err := run([]string{"cache", "clear", "--scope", "interface", "--central", "ccu1"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error when --interface missing for scope=interface")
	}
	if !strings.Contains(err.Error(), "interface") {
		t.Errorf("error should mention interface, got: %v", err)
	}
}

func TestCacheClearDeviceScopeRequiresAllQualifiers(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	err := run([]string{
		"cache", "clear", "--scope", "device",
		"--central", "ccu1", "--interface", "HmIP-RF",
	}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error when --device missing for scope=device")
	}
	if !strings.Contains(err.Error(), "device") {
		t.Errorf("error should mention device, got: %v", err)
	}
}

// ─── subcommand routing ───────────────────────────────────────────────────────

func TestCacheClearMissingOperationReturnsError(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	err := run([]string{"cache"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error when cache has no operation")
	}
	if !strings.Contains(err.Error(), "missing operation") {
		t.Errorf("error=%v, want 'missing operation'", err)
	}
}

func TestCacheClearUnknownCacheOperationReturnsError(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	err := run([]string{"cache", "frobnicate"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for unknown cache operation")
	}
}

// ─── online mode: flag → request body mapping ─────────────────────────────────

// capturedRequest records the JSON body a cache-clear test server received.
type capturedRequest struct {
	Kind      string `json:"kind"`
	Central   string `json:"central"`
	Interface string `json:"interface"`
	Device    string `json:"device"`
}

// newCacheClearServer returns a test server that captures the posted body and
// replies with a minimal report. The captured body is written through *got.
func newCacheClearServer(t *testing.T, got *capturedRequest) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method=%s, want POST", r.Method)
		}
		if r.URL.Path != "/api/v1/admin/cache/clear" {
			t.Errorf("path=%s, want /api/v1/admin/cache/clear", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, got); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"devices":1,"paramsets":2,"values":3,"master":4,"centrals_reinit":["ccu1"]}`))
	}))
}

func TestCacheClearOnlineGlobalPostsCorrectBody(t *testing.T) {
	t.Parallel()
	var got capturedRequest
	ts := newCacheClearServer(t, &got)
	defer ts.Close()

	var stdout, stderr bytes.Buffer
	if err := run([]string{"cache", "clear", "--scope", "global", "--url", ts.URL}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got.Kind != "global" {
		t.Errorf("kind=%q, want global", got.Kind)
	}
	if got.Central != "" || got.Interface != "" || got.Device != "" {
		t.Errorf("qualifiers should be empty for global, got %+v", got)
	}
	if !strings.Contains(stdout.String(), "scope=global") {
		t.Errorf("stdout missing scope=global: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "ccu1") {
		t.Errorf("stdout missing re-pulled central: %q", stdout.String())
	}
}

func TestCacheClearOnlineCentralPostsCorrectBody(t *testing.T) {
	t.Parallel()
	var got capturedRequest
	ts := newCacheClearServer(t, &got)
	defer ts.Close()

	var stdout, stderr bytes.Buffer
	err := run([]string{
		"cache", "clear", "--scope", "central",
		"--central", "ccu1", "--url", ts.URL,
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got.Kind != "central" {
		t.Errorf("kind=%q, want central", got.Kind)
	}
	if got.Central != "ccu1" {
		t.Errorf("central=%q, want ccu1", got.Central)
	}
}

func TestCacheClearOnlineInterfacePostsCorrectBody(t *testing.T) {
	t.Parallel()
	var got capturedRequest
	ts := newCacheClearServer(t, &got)
	defer ts.Close()

	var stdout, stderr bytes.Buffer
	err := run([]string{
		"cache", "clear", "--scope", "interface",
		"--central", "ccu1", "--interface", "HmIP-RF", "--url", ts.URL,
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got.Kind != "interface" || got.Central != "ccu1" || got.Interface != "HmIP-RF" {
		t.Errorf("body mismatch: %+v", got)
	}
}

func TestCacheClearOnlineDevicePostsCorrectBody(t *testing.T) {
	t.Parallel()
	var got capturedRequest
	ts := newCacheClearServer(t, &got)
	defer ts.Close()

	var stdout, stderr bytes.Buffer
	err := run([]string{
		"cache", "clear", "--scope", "device",
		"--central", "ccu1", "--interface", "HmIP-RF", "--device", "ABC123", "--url", ts.URL,
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got.Kind != "device" || got.Device != "ABC123" {
		t.Errorf("body mismatch: %+v", got)
	}
}

func TestCacheClearOnlineNon2xxReturnsError(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "service unready", http.StatusServiceUnavailable)
	}))
	defer ts.Close()

	var stdout, stderr bytes.Buffer
	err := run([]string{"cache", "clear", "--scope", "global", "--url", ts.URL}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error on non-2xx response")
	}
	if !strings.Contains(err.Error(), "503") {
		t.Errorf("error should mention HTTP 503, got: %v", err)
	}
}

func TestCacheClearOnlineSendsBearerToken(t *testing.T) {
	t.Parallel()
	var gotAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"devices":0,"paramsets":0,"values":0,"master":0}`))
	}))
	defer ts.Close()

	var stdout, stderr bytes.Buffer
	err := run([]string{
		"cache", "clear", "--scope", "global", "--url", ts.URL, "--token", "secret-tok",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if gotAuth != "Bearer secret-tok" {
		t.Errorf("Authorization=%q, want 'Bearer secret-tok'", gotAuth)
	}
}

func TestCacheClearOnlineSendsBasicAuthWhenNoToken(t *testing.T) {
	t.Parallel()
	var gotUser, gotPassword string
	var gotOK bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser, gotPassword, gotOK = r.BasicAuth()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"devices":0,"paramsets":0,"values":0,"master":0}`))
	}))
	defer ts.Close()

	var stdout, stderr bytes.Buffer
	err := run([]string{
		"cache", "clear", "--scope", "global", "--host", ts.URL,
		"--user", "alice", "--password", "s3cret",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run: %v\nstderr: %s", err, stderr.String())
	}
	if !gotOK {
		t.Fatal("request carried no basic-auth credentials")
	}
	if gotUser != "alice" || gotPassword != "s3cret" {
		t.Errorf("basic auth = %q/%q, want alice/s3cret", gotUser, gotPassword)
	}
}

func TestCacheClearOnlinePrefersBearerOverBasicAuth(t *testing.T) {
	t.Parallel()
	var gotAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"devices":0,"paramsets":0,"values":0,"master":0}`))
	}))
	defer ts.Close()

	var stdout, stderr bytes.Buffer
	err := run([]string{
		"cache", "clear", "--scope", "global", "--url", ts.URL,
		"--token", "secret-tok", "--user", "alice", "--password", "s3cret",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if gotAuth != "Bearer secret-tok" {
		t.Errorf("Authorization=%q, want the bearer token to win", gotAuth)
	}
}

// ─── offline scope → units expansion ──────────────────────────────────────────

func TestResolveOfflineUnitsInterfaceNeedsNoConfig(t *testing.T) {
	t.Parallel()
	units, err := resolveOfflineUnits("interface", "ccu1", "HmIP-RF", nil)
	if err != nil {
		t.Fatalf("resolveOfflineUnits: %v", err)
	}
	// The unit carries the canonical store id, not the bare name the
	// operator typed: every cached row is keyed by `<central>-<interface>`.
	if len(units) != 1 || units[0].central != "ccu1" || units[0].iface != "ccu1-HmIP-RF" {
		t.Fatalf("units=%+v", units)
	}
}

// TestResolveOfflineUnitsAcceptsCanonicalInterface pins that passing the
// canonical id (as an operator who copied it off a topic would) is not
// double-prefixed into an id no row carries.
func TestResolveOfflineUnitsAcceptsCanonicalInterface(t *testing.T) {
	t.Parallel()
	units, err := resolveOfflineUnits("interface", "ccu1", "ccu1-HmIP-RF", nil)
	if err != nil {
		t.Fatalf("resolveOfflineUnits: %v", err)
	}
	if len(units) != 1 || units[0].iface != "ccu1-HmIP-RF" {
		t.Fatalf("units=%+v", units)
	}
}

func TestResolveOfflineUnitsGlobalRequiresConfig(t *testing.T) {
	t.Parallel()
	if _, err := resolveOfflineUnits("global", "", "", nil); err == nil {
		t.Fatal("expected error: global scope without config cannot enumerate centrals")
	}
}

func TestResolveOfflineUnitsCentralUnknownNameErrors(t *testing.T) {
	t.Parallel()
	cfg := newTestConfig()
	if _, err := resolveOfflineUnits("central", "nope", "", cfg); err == nil {
		t.Fatal("expected error for unknown central name")
	}
}

func TestResolveOfflineUnitsGlobalEnumeratesAll(t *testing.T) {
	t.Parallel()
	cfg := newTestConfig()
	units, err := resolveOfflineUnits("global", "", "", cfg)
	if err != nil {
		t.Fatalf("resolveOfflineUnits: %v", err)
	}
	if len(units) != 3 { // ccu1: HmIP-RF, BidCos-RF ; ccu2: HmIP-RF
		t.Fatalf("want 3 units, got %d: %+v", len(units), units)
	}
}

func TestResolveOfflineUnitsCentralEnumeratesItsInterfaces(t *testing.T) {
	t.Parallel()
	cfg := newTestConfig()
	units, err := resolveOfflineUnits("central", "ccu1", "", cfg)
	if err != nil {
		t.Fatalf("resolveOfflineUnits: %v", err)
	}
	if len(units) != 2 {
		t.Fatalf("want 2 units for ccu1, got %d: %+v", len(units), units)
	}
}

// ─── offline DSN resolution ───────────────────────────────────────────────────

func TestResolveOfflineDSNRequiresConfigOrDB(t *testing.T) {
	t.Parallel()
	if _, _, _, err := resolveOfflineDSN("interface", "", ""); err == nil {
		t.Fatal("expected error: offline mode needs --config or --db")
	}
}

func TestResolveOfflineDSNDBOverrideWins(t *testing.T) {
	t.Parallel()
	dsn, dbFile, cfg, err := resolveOfflineDSN("interface", "", "/tmp/custom.db")
	if err != nil {
		t.Fatalf("resolveOfflineDSN: %v", err)
	}
	if cfg != nil {
		t.Errorf("config should be nil when only --db is given, got %+v", cfg)
	}
	if !strings.Contains(dsn, "/tmp/custom.db") {
		t.Errorf("dsn should use the override path, got %q", dsn)
	}
	if dbFile != "/tmp/custom.db" {
		t.Errorf("db file = %q, want the override path", dbFile)
	}
}

func TestResolveOfflineDSNGlobalNeedsConfigEvenWithDB(t *testing.T) {
	t.Parallel()
	if _, _, _, err := resolveOfflineDSN("global", "", "/tmp/custom.db"); err == nil {
		t.Fatal("expected error: global scope cannot enumerate interfaces without config")
	}
}

// TestResolveOfflineDSNAppliesDataDirEnvOverlay pins that the offline path
// resolves the database through the same env overlay the daemon applies. The
// container image and the HA add-on set OPENCCU_LOOM_DATA_DIR; resolving the
// YAML data_dir instead points the clear at a database the daemon never uses.
func TestResolveOfflineDSNAppliesDataDirEnvOverlay(t *testing.T) {
	envDir := t.TempDir()
	yamlDir := t.TempDir()
	t.Setenv("OPENCCU_LOOM_DATA_DIR", envDir)

	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("data_dir: "+yamlDir+"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, dbFile, _, err := resolveOfflineDSN("interface", cfgPath, "")
	if err != nil {
		t.Fatalf("resolveOfflineDSN: %v", err)
	}
	if want := filepath.Join(envDir, "openccu-loom.db"); dbFile != want {
		t.Errorf("dbFile = %q, want %q (OPENCCU_LOOM_DATA_DIR must win over data_dir)", dbFile, want)
	}
}

func TestRunCacheClearOfflineMissingConfigErrors(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	err := run([]string{"cache", "clear", "--scope", "global", "--offline"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for offline global clear without --config")
	}
}

// ─── offline clear: store failures reach the exit code ────────────────────────

// newOfflineCacheDB creates a migrated database file for the offline clear
// path. When dropCacheTables is set, the two cache tables the clear deletes
// from are removed afterwards, which makes every delete fail the way a corrupt
// or half-migrated database does on an operator's machine.
func newOfflineCacheDB(t *testing.T, dropCacheTables bool) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "openccu-loom.db")
	ctx := context.Background()
	db, err := sqlite.Open(ctx, sqlite.FileDSN(path))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if dropCacheTables {
		for _, table := range []string{"values_cache", "master_values"} {
			if _, err := db.ExecContext(ctx, "DROP TABLE "+table); err != nil {
				_ = db.Close()
				t.Fatalf("drop %s: %v", table, err)
			}
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}
	return path
}

func TestRunCacheClearOfflineInterfaceScopeFailsWhenStoreDeletesFail(t *testing.T) {
	t.Parallel()
	dbPath := newOfflineCacheDB(t, true)

	var stdout, stderr bytes.Buffer
	err := run([]string{
		"cache", "clear", "--offline",
		"--scope", "interface", "--central", "ccu1", "--interface", "HmIP-RF",
		"--db", dbPath,
	}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected a non-nil error when the cache deletes fail")
	}
	if !strings.Contains(err.Error(), "values[") || !strings.Contains(err.Error(), "master[") {
		t.Errorf("error should name both failing stores, got: %v", err)
	}
	if code := exitCodeFor(err); code != exitGeneral {
		t.Errorf("exit code = %d, want %d", code, exitGeneral)
	}
}

func TestRunCacheClearOfflineDeviceScopeFailsWhenStoreDeletesFail(t *testing.T) {
	t.Parallel()
	dbPath := newOfflineCacheDB(t, true)

	var stdout, stderr bytes.Buffer
	err := run([]string{
		"cache", "clear", "--offline",
		"--scope", "device", "--central", "ccu1", "--interface", "HmIP-RF", "--device", "VCU0000001",
		"--db", dbPath,
	}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected a non-nil error when the cache deletes fail")
	}
	if code := exitCodeFor(err); code != exitGeneral {
		t.Errorf("exit code = %d, want %d", code, exitGeneral)
	}
}

func TestRunCacheClearOfflineMissingDatabaseFailsWithoutCreatingOne(t *testing.T) {
	t.Parallel()
	// A parent directory that exists, so SQLite would happily create the file.
	missing := filepath.Join(t.TempDir(), "typo.db")

	var stdout, stderr bytes.Buffer
	err := run([]string{
		"cache", "clear", "--offline",
		"--scope", "interface", "--central", "ccu1", "--interface", "HmIP-RF",
		"--db", missing,
	}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected an error when the database does not exist")
	}
	if !strings.Contains(err.Error(), missing) {
		t.Errorf("error should name the resolved path, got: %v", err)
	}
	if _, statErr := os.Stat(missing); statErr == nil {
		t.Error("a fresh database was created at the wrong path")
	}
	if strings.Contains(stdout.String(), "Cache cleared") {
		t.Errorf("a failed clear must not report success, got: %q", stdout.String())
	}
}

func TestRunCacheClearOfflineSucceedsAgainstIntactDB(t *testing.T) {
	t.Parallel()
	dbPath := newOfflineCacheDB(t, false)

	var stdout, stderr bytes.Buffer
	if err := run([]string{
		"cache", "clear", "--offline",
		"--scope", "interface", "--central", "ccu1", "--interface", "HmIP-RF",
		"--db", dbPath,
	}, &stdout, &stderr); err != nil {
		t.Fatalf("clear against an intact db: %v", err)
	}
	if !strings.Contains(stdout.String(), "Cache cleared: scope=interface") {
		t.Errorf("summary missing from stdout: %q", stdout.String())
	}
}

// TestRunCacheClearOfflineEmptiesDeviceAndParamsetRows is the regression
// guard for the offline clear leaving persisted device / paramset
// descriptor rows behind: the online path clears four stores (values,
// master, devices, paramsets — see cachereset.Service.clearUnit), and the
// offline path must delete the same four so `hmcli cache clear --offline`
// followed by a restart re-pulls a genuinely empty cache, not one still
// carrying the previous pairing's stale device/paramset descriptions.
func TestRunCacheClearOfflineEmptiesDeviceAndParamsetRows(t *testing.T) {
	t.Parallel()
	dbPath := newOfflineCacheDB(t, false)
	ctx := context.Background()
	wireIface := cachereset.StoreInterfaceID("ccu1", "HmIP-RF")

	db, err := sqlite.Open(ctx, sqlite.FileDSN(dbPath))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	deviceStore := sqlite.NewDeviceStore(db)
	paramsetStore := sqlite.NewParamsetStore(db)
	if err := deviceStore.Upsert(ctx, sqlite.DeviceRecord{
		CentralName:  "ccu1",
		InterfaceID:  wireIface,
		Address:      "DEVICE1",
		Type:         "HmIP-PS",
		Model:        "HmIP-PS",
		Manufacturer: hmenum.ManufacturerEQ3,
		ProductGroup: hmenum.ProductGroupHmIP,
		Hash:         "h1",
		Description:  hmproto.DeviceDescription{Address: "DEVICE1", Type: "HmIP-PS"},
	}); err != nil {
		t.Fatalf("seed device row: %v", err)
	}
	if err := paramsetStore.Upsert(ctx, sqlite.ParamsetRecord{
		CentralName:    "ccu1",
		InterfaceID:    wireIface,
		ChannelAddress: "DEVICE1:1",
		ParamsetKey:    hmenum.ParamsetKeyValues,
		Hash:           "h1",
		Paramset:       hmproto.Paramset{"STATE": {Type: "BOOL"}},
	}); err != nil {
		t.Fatalf("seed paramset row: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	var stdout, stderr bytes.Buffer
	if err := run([]string{
		"cache", "clear", "--offline",
		"--scope", "interface", "--central", "ccu1", "--interface", "HmIP-RF",
		"--db", dbPath,
	}, &stdout, &stderr); err != nil {
		t.Fatalf("clear against an intact db: %v\nstderr: %s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "devices:    1") {
		t.Errorf("summary should report 1 device row cleared, got: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "paramsets:  1") {
		t.Errorf("summary should report 1 paramset row cleared, got: %q", stdout.String())
	}

	verifyDB, err := sqlite.Open(ctx, sqlite.FileDSN(dbPath))
	if err != nil {
		t.Fatalf("reopen db: %v", err)
	}
	defer func() { _ = verifyDB.Close() }()
	if _, err := sqlite.NewDeviceStore(verifyDB).Get(ctx, "ccu1", wireIface, "DEVICE1"); !errors.Is(err, sqlite.ErrDeviceNotFound) {
		t.Errorf("device row survived offline clear: err=%v, want ErrDeviceNotFound", err)
	}
	if _, err := sqlite.NewParamsetStore(verifyDB).Get(ctx, "ccu1", wireIface, "DEVICE1:1", hmenum.ParamsetKeyValues); !errors.Is(err, sqlite.ErrParamsetNotFound) {
		t.Errorf("paramset row survived offline clear: err=%v, want ErrParamsetNotFound", err)
	}
}
