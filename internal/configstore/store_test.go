// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package configstore

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// ---------------------------------------------------------------------------
// Fakes
// ---------------------------------------------------------------------------

// fakeSectionLoader is a fake SectionLoader backed by an in-memory map.
// getErr lets a test make one section unreadable, which is what a sealed
// row whose master key is gone looks like to the Store.
type fakeSectionLoader struct {
	rows   map[string]sqlite.SectionRow
	getErr map[string]error
}

func newFakeSectionLoader() *fakeSectionLoader {
	return &fakeSectionLoader{
		rows:   make(map[string]sqlite.SectionRow),
		getErr: make(map[string]error),
	}
}

func (f *fakeSectionLoader) Get(_ context.Context, section string) (sqlite.SectionRow, error) {
	if err, ok := f.getErr[section]; ok {
		return sqlite.SectionRow{}, err
	}
	r, ok := f.rows[section]
	if !ok {
		return sqlite.SectionRow{}, sqlite.ErrSectionNotFound
	}
	return r, nil
}

func (f *fakeSectionLoader) Put(_ context.Context, section string, valueJSON []byte, updatedBy string) (sqlite.SectionRow, error) {
	r := sqlite.SectionRow{Section: section, ValueJSON: valueJSON, UpdatedBy: updatedBy}
	f.rows[section] = r
	return r, nil
}

func (f *fakeSectionLoader) Delete(_ context.Context, section string) error {
	if _, ok := f.rows[section]; !ok {
		return sqlite.ErrSectionNotFound
	}
	delete(f.rows, section)
	return nil
}

func (f *fakeSectionLoader) List(_ context.Context) ([]sqlite.SectionRow, error) {
	out := make([]sqlite.SectionRow, 0, len(f.rows))
	for _, r := range f.rows {
		out = append(out, r)
	}
	return out, nil
}

// fakeCentralLoader is a fake CentralLoader that returns a fixed slice.
type fakeCentralLoader struct {
	rows []sqlite.CentralRow
}

func (f *fakeCentralLoader) List(_ context.Context) ([]sqlite.CentralRow, error) {
	return f.rows, nil
}

// defaultBootstrap returns a minimal BootstrapConfig with known field
// values, suitable as the bootstrap tier for tests.
func defaultBootstrap() *config.BootstrapConfig {
	return &config.BootstrapConfig{
		DataDir: "/tmp/test",
		Logging: config.LoggingConfig{Level: "info", Format: "json"},
		Listen: config.BootstrapListen{
			REST: ":9080",
		},
	}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestStoreEffectiveNoLoaders verifies that a Store with nil section
// and central loaders returns the bootstrap values on the bootstrap
// fields and SourceDefault for unset fields.
func TestStoreEffectiveNoLoaders(t *testing.T) {
	t.Parallel()
	bs := defaultBootstrap()
	s := New(bs, nil, nil)
	res, err := s.Effective(context.Background())
	if err != nil {
		t.Fatalf("Effective: %v", err)
	}
	if res.Config.DataDir != "/tmp/test" {
		t.Errorf("DataDir=%q want /tmp/test", res.Config.DataDir)
	}
	if res.Config.North.REST.Listen != ":9080" {
		t.Errorf("North.REST.Listen=%q want :9080", res.Config.North.REST.Listen)
	}
	if res.Sources["data_dir"] != SourceBootstrap {
		t.Errorf("data_dir source=%q want bootstrap", res.Sources["data_dir"])
	}
	if res.Sources["logging"] != SourceBootstrap {
		t.Errorf("logging source=%q want bootstrap", res.Sources["logging"])
	}
	if res.Sources["north.rest.listen"] != SourceBootstrap {
		t.Errorf("north.rest.listen source=%q want bootstrap", res.Sources["north.rest.listen"])
	}
}

// TestStoreEffectiveBootstrapFieldSources verifies all four hardcoded
// bootstrap-tier source attributions are SourceBootstrap, not SourceDB
// or SourceDefault.
func TestStoreEffectiveBootstrapFieldSources(t *testing.T) {
	t.Parallel()
	s := New(defaultBootstrap(), nil, nil)
	res, err := s.Effective(context.Background())
	if err != nil {
		t.Fatalf("Effective: %v", err)
	}
	bootstrapFields := []string{"data_dir", "logging", "north.rest.listen"}
	for _, f := range bootstrapFields {
		if got := res.Sources[f]; got != SourceBootstrap {
			t.Errorf("Sources[%q]=%q want %q", f, got, SourceBootstrap)
		}
	}
}

// TestStoreEffectiveCarriesBootstrapSafety pins that the hardening toggle
// survives the assembly the daemon's reload and the SPA's config snapshot
// both run through. The toggle is bootstrap-tier: an effective config that
// dropped it would re-open the unauthenticated /setup surface on the next
// re-assembly of a hardened deployment.
func TestStoreEffectiveCarriesBootstrapSafety(t *testing.T) {
	t.Parallel()
	bs := defaultBootstrap()
	bs.Bootstrap.AllowFirstRunSetup = new(false)
	s := New(bs, nil, nil)
	res, err := s.Effective(context.Background())
	if err != nil {
		t.Fatalf("Effective: %v", err)
	}
	if res.Config.Bootstrap.FirstRunSetupAllowed() {
		t.Error("effective config must carry allow_first_run_setup: false")
	}
	if got := res.Sources["bootstrap.allow_first_run_setup"]; got != SourceBootstrap {
		t.Errorf("Sources[bootstrap.allow_first_run_setup]=%q want %q", got, SourceBootstrap)
	}
	if _, ok := UnmanagedFieldPaths()["bootstrap.allow_first_run_setup"]; !ok {
		t.Error("bootstrap.allow_first_run_setup must not be editable through the section editor")
	}
}

// TestStoreEffectiveAppliesSectionMQTT verifies that a north.mqtt row
// in the section loader is applied to cfg.North.MQTT and attributed
// to SourceDB.
func TestStoreEffectiveAppliesSectionMQTT(t *testing.T) {
	t.Parallel()
	sl := newFakeSectionLoader()

	mqtt := config.NorthMQTT{
		Enabled:   true,
		BrokerURL: "tcp://broker.example.com:1883",
		ClientID:  "openccu",
	}
	raw, _ := json.Marshal(mqtt)
	sl.rows[string(SectionMQTT)] = sqlite.SectionRow{
		Section:   string(SectionMQTT),
		ValueJSON: raw,
	}

	s := New(defaultBootstrap(), sl, nil)
	res, err := s.Effective(context.Background())
	if err != nil {
		t.Fatalf("Effective: %v", err)
	}
	if !res.Config.North.MQTT.Enabled {
		t.Error("North.MQTT.Enabled: want true")
	}
	if res.Config.North.MQTT.BrokerURL != "tcp://broker.example.com:1883" {
		t.Errorf("BrokerURL=%q want tcp://broker.example.com:1883", res.Config.North.MQTT.BrokerURL)
	}
	if res.Sources[string(SectionMQTT)] != SourceDB {
		t.Errorf("Sources[north.mqtt]=%q want db", res.Sources[string(SectionMQTT)])
	}
	// Per-FIELD attribution: the SPA's source pill keys on the full
	// field path (e.g. "north.mqtt.enabled"), not the bare section
	// name. A persisted section must therefore attribute each of its
	// fields to the DB tier so the dot renders green instead of grey.
	for _, path := range []string{"north.mqtt.enabled", "north.mqtt.broker_url", "north.mqtt.client_id"} {
		if res.Sources[path] != SourceDB {
			t.Errorf("Sources[%q]=%q want db", path, res.Sources[path])
		}
	}
}

// TestStoreEffectivePerFieldSourceAttribution verifies that persisting
// one section attributes its own fields to the DB tier without leaking
// into a sibling section, that the longest-prefix rule credits OIDC
// fields to the OIDC section (not REST), and that bootstrap-owned paths
// keep their bootstrap attribution even when their owning section row
// is present.
func TestStoreEffectivePerFieldSourceAttribution(t *testing.T) {
	t.Parallel()
	sl := newFakeSectionLoader()

	rest := config.NorthREST{PublicURL: "https://loom.example.com"}
	rraw, _ := json.Marshal(rest)
	sl.rows[string(SectionREST)] = sqlite.SectionRow{Section: string(SectionREST), ValueJSON: rraw}

	s := New(defaultBootstrap(), sl, nil)
	res, err := s.Effective(context.Background())
	if err != nil {
		t.Fatalf("Effective: %v", err)
	}
	// A field inside the persisted REST section flips to db.
	if res.Sources["north.rest.public_url"] != SourceDB {
		t.Errorf("Sources[north.rest.public_url]=%q want db", res.Sources["north.rest.public_url"])
	}
	// north.rest.listen is a bootstrap-owned path nested under the REST
	// section struct; its bootstrap attribution must survive.
	if res.Sources["north.rest.listen"] != SourceBootstrap {
		t.Errorf("Sources[north.rest.listen]=%q want bootstrap", res.Sources["north.rest.listen"])
	}
	// The OIDC section was never persisted, so its fields stay default
	// rather than being swept up by the REST section's prefix.
	if got := res.Sources["north.rest.auth.oidc.client_secret"]; got == SourceDB {
		t.Errorf("Sources[north.rest.auth.oidc.client_secret]=%q must not be db (OIDC section absent)", got)
	}
	// A sibling section that was never persisted stays default.
	if got := res.Sources["north.mqtt.enabled"]; got == SourceDB {
		t.Errorf("Sources[north.mqtt.enabled]=%q must not be db (MQTT section absent)", got)
	}
}

// TestStoreEffectivePrunedFieldNotSourceDB verifies that a field which
// was pruned from a stored section (e.g. by a per-field revert) is no
// longer attributed to the DB tier, even though the section row still
// exists for its remaining fields. This is the source-pill half of the
// per-field revert: the value falls back to its built-in default, so the
// dot must read default, not db.
func TestStoreEffectivePrunedFieldNotSourceDB(t *testing.T) {
	t.Parallel()
	sl := newFakeSectionLoader()

	// Stored row after a revert of allow_writes: only enabled+path remain.
	sl.rows[string(SectionMCP)] = sqlite.SectionRow{
		Section:   string(SectionMCP),
		ValueJSON: []byte(`{"enabled":true,"path":"/mcp"}`),
	}

	s := New(defaultBootstrap(), sl, nil)
	res, err := s.Effective(context.Background())
	if err != nil {
		t.Fatalf("Effective: %v", err)
	}
	// Present fields stay db.
	if res.Sources["north.mcp.enabled"] != SourceDB {
		t.Errorf("Sources[north.mcp.enabled]=%q want db", res.Sources["north.mcp.enabled"])
	}
	if res.Sources["north.mcp.path"] != SourceDB {
		t.Errorf("Sources[north.mcp.path]=%q want db", res.Sources["north.mcp.path"])
	}
	// The pruned field must NOT read as db — it fell back to default.
	if got := res.Sources["north.mcp.allow_writes"]; got == SourceDB {
		t.Errorf("Sources[north.mcp.allow_writes]=%q must not be db after prune", got)
	}
	if res.Config.North.MCP.AllowWrites {
		t.Error("North.MCP.AllowWrites: want false (reverted to default)")
	}
}

// TestStoreEffectiveAppliesSectionMCP verifies that a north.mcp row in
// the section loader is applied to cfg.North.MCP and attributed to
// SourceDB — the persistence path behind the SPA's MCP settings tab.
func TestStoreEffectiveAppliesSectionMCP(t *testing.T) {
	t.Parallel()
	sl := newFakeSectionLoader()

	mcp := config.NorthMCP{Enabled: true, AllowWrites: true, Path: "/agents"}
	raw, _ := json.Marshal(mcp)
	sl.rows[string(SectionMCP)] = sqlite.SectionRow{
		Section:   string(SectionMCP),
		ValueJSON: raw,
	}

	s := New(defaultBootstrap(), sl, nil)
	res, err := s.Effective(context.Background())
	if err != nil {
		t.Fatalf("Effective: %v", err)
	}
	if !res.Config.North.MCP.Enabled {
		t.Error("North.MCP.Enabled: want true")
	}
	if !res.Config.North.MCP.AllowWrites {
		t.Error("North.MCP.AllowWrites: want true")
	}
	if res.Config.North.MCP.Path != "/agents" {
		t.Errorf("North.MCP.Path=%q want /agents", res.Config.North.MCP.Path)
	}
	if res.Sources[string(SectionMCP)] != SourceDB {
		t.Errorf("Sources[north.mcp]=%q want db", res.Sources[string(SectionMCP)])
	}
	if res.Sources["north.mcp.allow_writes"] != SourceDB {
		t.Errorf("Sources[north.mcp.allow_writes]=%q want db", res.Sources["north.mcp.allow_writes"])
	}
}

// TestStoreEffectiveSourceDBForPresentSection verifies that any
// section present in the DB gets SourceDB attribution.
func TestStoreEffectiveSourceDBForPresentSection(t *testing.T) {
	t.Parallel()
	sl := newFakeSectionLoader()
	sl.rows[string(SectionCallback)] = sqlite.SectionRow{
		Section:   string(SectionCallback),
		ValueJSON: []byte(`{}`),
	}

	s := New(defaultBootstrap(), sl, nil)
	res, err := s.Effective(context.Background())
	if err != nil {
		t.Fatalf("Effective: %v", err)
	}
	if res.Sources[string(SectionCallback)] != SourceDB {
		t.Errorf("Sources[callback]=%q want db", res.Sources[string(SectionCallback)])
	}
}

// TestStoreEffectiveEnvOverridesMQTTPassword verifies that
// OPENCCU_LOOM_MQTT_PASSWORD from the env lookup overrides the DB
// value and sets SourceEnv.
func TestStoreEffectiveEnvOverridesMQTTPassword(t *testing.T) {
	t.Parallel()
	sl := newFakeSectionLoader()

	// Seed a north.mqtt section with a password.
	mqtt := config.NorthMQTT{Password: "db-secret"}
	raw, _ := json.Marshal(mqtt)
	sl.rows[string(SectionMQTT)] = sqlite.SectionRow{
		Section:   string(SectionMQTT),
		ValueJSON: raw,
	}

	lookup := func(key string) string {
		if key == "OPENCCU_LOOM_MQTT_PASSWORD" {
			return "env-secret"
		}
		return ""
	}
	s := New(defaultBootstrap(), sl, nil, WithEnvLookup(lookup))
	res, err := s.Effective(context.Background())
	if err != nil {
		t.Fatalf("Effective: %v", err)
	}
	if res.Config.North.MQTT.Password != "env-secret" {
		t.Errorf("MQTT.Password=%q want env-secret", res.Config.North.MQTT.Password)
	}
	if res.Sources["north.mqtt.password"] != SourceEnv {
		t.Errorf("Sources[north.mqtt.password]=%q want env", res.Sources["north.mqtt.password"])
	}
}

// TestStoreEffectiveWithCentralsLoader verifies that enabled centrals
// from the CentralLoader are materialized into cfg.Centrals and
// attributed to SourceDB.
func TestStoreEffectiveWithCentralsLoader(t *testing.T) {
	t.Parallel()
	cl := &fakeCentralLoader{
		rows: []sqlite.CentralRow{
			{
				Name:    "ccu1",
				Host:    "192.168.1.10",
				Port:    2001,
				Enabled: true,
			},
			{
				Name:    "ccu2",
				Host:    "192.168.1.11",
				Port:    2002,
				Enabled: true,
			},
		},
	}

	s := New(defaultBootstrap(), nil, cl)
	res, err := s.Effective(context.Background())
	if err != nil {
		t.Fatalf("Effective: %v", err)
	}
	if len(res.Config.Centrals) != 2 {
		t.Fatalf("Centrals len=%d want 2", len(res.Config.Centrals))
	}
	if res.Sources["centrals"] != SourceDB {
		t.Errorf("Sources[centrals]=%q want db", res.Sources["centrals"])
	}
	if res.Config.Centrals[0].Name != "ccu1" {
		t.Errorf("Centrals[0].Name=%q want ccu1", res.Config.Centrals[0].Name)
	}
}

// yamlBaseWithCentral returns a YAML-tier base config carrying one central,
// mirroring a daemon booted from a config.yaml whose `centrals:` entry was
// copied into the centrals table on first run. Without such a base the
// central-tier tests assert against an already-empty slice and pass for a
// reason unrelated to the code under test.
func yamlBaseWithCentral() *config.Config {
	cfg := config.Default()
	cfg.Centrals = []config.CentralConfig{{
		Name:       "ccu1",
		Host:       "10.0.0.9",
		Port:       2010,
		Interfaces: []config.InterfaceSpec{{Name: "HmIP-RF"}},
	}}
	return cfg
}

// TestStoreEffectiveDisabledCentralsFiltered verifies that disabled
// centrals are silently excluded from cfg.Centrals — including when the
// YAML base still lists a central of its own, which the DB tier replaces
// wholesale.
func TestStoreEffectiveDisabledCentralsFiltered(t *testing.T) {
	t.Parallel()
	cl := &fakeCentralLoader{
		rows: []sqlite.CentralRow{
			{Name: "active", Host: "10.0.0.1", Enabled: true},
			{Name: "parked", Host: "10.0.0.2", Enabled: false},
		},
	}

	s := New(defaultBootstrap(), nil, cl, WithBaseConfig(yamlBaseWithCentral()))
	res, err := s.Effective(context.Background())
	if err != nil {
		t.Fatalf("Effective: %v", err)
	}
	if len(res.Config.Centrals) != 1 {
		t.Fatalf("Centrals len=%d want 1 (disabled must be excluded)", len(res.Config.Centrals))
	}
	if res.Config.Centrals[0].Name != "active" {
		t.Errorf("Centrals[0].Name=%q want active", res.Config.Centrals[0].Name)
	}
}

// TestStoreEffectiveAllCentralsDisabledYieldsEmptyCentrals verifies that a
// centrals table holding only parked rows yields no centrals at all, even
// though the YAML base the daemon booted from still lists one. A table with
// rows is the authoritative tier: "every central is parked" must mean "no
// central", not "fall back to the config file" — otherwise disabling the
// last CCU in the SPA reconnects to it on the next restart.
func TestStoreEffectiveAllCentralsDisabledYieldsEmptyCentrals(t *testing.T) {
	t.Parallel()
	cl := &fakeCentralLoader{
		rows: []sqlite.CentralRow{
			{Name: "parked1", Host: "10.0.0.1", Enabled: false},
			{Name: "parked2", Host: "10.0.0.2", Enabled: false},
		},
	}

	s := New(defaultBootstrap(), nil, cl, WithBaseConfig(yamlBaseWithCentral()))
	res, err := s.Effective(context.Background())
	if err != nil {
		t.Fatalf("Effective: %v", err)
	}
	if len(res.Config.Centrals) != 0 {
		t.Fatalf("Centrals len=%d want 0 (all disabled)", len(res.Config.Centrals))
	}
	if res.Sources["centrals"] != SourceDB {
		t.Errorf("Sources[centrals]=%q want db (the table is in use)", res.Sources["centrals"])
	}
}

// TestStoreEffectiveEmptyCentralsTableKeepsYAMLCentrals verifies the other
// half of the tier rule: a centrals table with no rows at all means the DB
// tier has nothing to say, so the YAML `centrals:` list stays authoritative.
func TestStoreEffectiveEmptyCentralsTableKeepsYAMLCentrals(t *testing.T) {
	t.Parallel()
	s := New(defaultBootstrap(), nil, &fakeCentralLoader{}, WithBaseConfig(yamlBaseWithCentral()))
	res, err := s.Effective(context.Background())
	if err != nil {
		t.Fatalf("Effective: %v", err)
	}
	if len(res.Config.Centrals) != 1 || res.Config.Centrals[0].Name != "ccu1" {
		t.Fatalf("Centrals=%+v want the YAML entry to survive an empty table", res.Config.Centrals)
	}
	if got := res.Sources["centrals"]; got == SourceDB {
		t.Errorf("Sources[centrals]=%q must not be db while the table is empty", got)
	}
}

// TestStoreEffectiveMalformedSectionJSON verifies that Effective
// returns an error (not a panic) when a section row contains invalid
// JSON.
func TestStoreEffectiveMalformedSectionJSON(t *testing.T) {
	t.Parallel()
	sl := newFakeSectionLoader()
	sl.rows[string(SectionMQTT)] = sqlite.SectionRow{
		Section:   string(SectionMQTT),
		ValueJSON: []byte(`{broken json`),
	}

	s := New(defaultBootstrap(), sl, nil)
	_, err := s.Effective(context.Background())
	if err == nil {
		t.Error("Effective with malformed JSON: want error, got nil")
	}
}

// TestStoreEffectiveOIDCEnvOverride verifies that
// OPENCCU_LOOM_OIDC_CLIENT_SECRET from the env lookup sets
// SourceEnv on the OIDC client_secret key.
func TestStoreEffectiveOIDCEnvOverride(t *testing.T) {
	t.Parallel()
	lookup := func(key string) string {
		if key == "OPENCCU_LOOM_OIDC_CLIENT_SECRET" {
			return "my-oidc-secret"
		}
		return ""
	}
	s := New(defaultBootstrap(), nil, nil, WithEnvLookup(lookup))
	res, err := s.Effective(context.Background())
	if err != nil {
		t.Fatalf("Effective: %v", err)
	}
	if res.Config.North.REST.Auth.OIDC.ClientSecret != "my-oidc-secret" {
		t.Errorf("OIDC.ClientSecret=%q want my-oidc-secret", res.Config.North.REST.Auth.OIDC.ClientSecret)
	}
	if res.Sources["north.rest.auth.oidc.client_secret"] != SourceEnv {
		t.Errorf("Sources[north.rest.auth.oidc.client_secret]=%q want env",
			res.Sources["north.rest.auth.oidc.client_secret"])
	}
}

// TestStoreEffectiveSectionNotFoundIsIgnored verifies that sections
// not present in the loader are simply skipped (no error, no source
// entry).
func TestStoreEffectiveSectionNotFoundIsIgnored(t *testing.T) {
	t.Parallel()
	// Empty fake section loader — no sections stored.
	sl := newFakeSectionLoader()
	s := New(defaultBootstrap(), sl, nil)
	res, err := s.Effective(context.Background())
	if err != nil {
		t.Fatalf("Effective: %v", err)
	}
	// No DB sections should be present in sources map.
	for _, sec := range AllSections() {
		if src, ok := res.Sources[string(sec)]; ok {
			t.Errorf("Sources[%q]=%q; want absent (section was not stored)", sec, src)
		}
	}
}

// TestStoreEffectiveSectionLoaderError verifies that a SectionLoader
// that returns a non-ErrSectionNotFound error propagates through
// Effective as an error.
func TestStoreEffectiveSectionLoaderError(t *testing.T) {
	t.Parallel()
	errDB := errors.New("connection reset")
	sl := &errSectionLoader{err: errDB}
	s := New(defaultBootstrap(), sl, nil)
	_, err := s.Effective(context.Background())
	if err == nil {
		t.Fatal("Effective with loader error: want error, got nil")
	}
	if !errors.Is(err, errDB) {
		t.Errorf("error=%v does not wrap errDB", err)
	}
}

// errSectionLoader returns a fixed error for every Get call (except
// for ErrSectionNotFound which is treated as "not present").
type errSectionLoader struct {
	err error
}

func (e *errSectionLoader) Get(_ context.Context, _ string) (sqlite.SectionRow, error) {
	return sqlite.SectionRow{}, e.err
}

func (e *errSectionLoader) Put(_ context.Context, _ string, _ []byte, _ string) (sqlite.SectionRow, error) {
	return sqlite.SectionRow{}, e.err
}

func (e *errSectionLoader) Delete(_ context.Context, _ string) error {
	return e.err
}

func (e *errSectionLoader) List(_ context.Context) ([]sqlite.SectionRow, error) {
	return nil, e.err
}

// boolPtr is a small helper used by tests that need to construct *bool
// values inline. Reuse this whenever a *bool field must be checked.
func boolPtr(b bool) *bool { return &b }

// TestStoreEffectiveAppliesSectionCCUAuth verifies that a
// north.rest.auth.ccu row in the section loader is applied to
// cfg.North.REST.Auth.CCU and attributed to SourceDB — the persistence
// path behind the SPA's CCU-auth settings tab.
func TestStoreEffectiveAppliesSectionCCUAuth(t *testing.T) {
	t.Parallel()
	sl := newFakeSectionLoader()

	ccuAuth := config.CCUAuthConfig{
		Enabled:      boolPtr(true),
		Primary:      boolPtr(false),
		Central:      "ccu1",
		MinUserLevel: 2,
		RoleMapping:  map[string]string{"8": "admin"},
	}
	raw, _ := json.Marshal(ccuAuth)
	sl.rows[string(SectionCCUAuth)] = sqlite.SectionRow{
		Section:   string(SectionCCUAuth),
		ValueJSON: raw,
	}

	s := New(defaultBootstrap(), sl, nil)
	res, err := s.Effective(context.Background())
	if err != nil {
		t.Fatalf("Effective: %v", err)
	}
	got := res.Config.North.REST.Auth.CCU
	if got.Enabled == nil {
		t.Fatal("North.REST.Auth.CCU.Enabled: want non-nil")
	}
	if !*got.Enabled {
		t.Error("North.REST.Auth.CCU.Enabled: want true")
	}
	if got.Primary == nil {
		t.Fatal("North.REST.Auth.CCU.Primary: want non-nil")
	}
	if *got.Primary {
		t.Error("North.REST.Auth.CCU.Primary: want false")
	}
	if got.Central != "ccu1" {
		t.Errorf("North.REST.Auth.CCU.Central=%q want ccu1", got.Central)
	}
	if got.MinUserLevel != 2 {
		t.Errorf("North.REST.Auth.CCU.MinUserLevel=%d want 2", got.MinUserLevel)
	}
	if got.RoleMapping["8"] != "admin" {
		t.Errorf("North.REST.Auth.CCU.RoleMapping[8]=%q want admin", got.RoleMapping["8"])
	}
	if res.Sources[string(SectionCCUAuth)] != SourceDB {
		t.Errorf("Sources[north.rest.auth.ccu]=%q want db", res.Sources[string(SectionCCUAuth)])
	}
}

// TestRowToCentralConfigPlaintextPassword verifies that a row with
// PasswordPlain set and no PasswordEnv configured passes the plaintext
// password through unchanged and reports usedEnv == false.
func TestRowToCentralConfigPlaintextPassword(t *testing.T) {
	t.Parallel()
	row := sqlite.CentralRow{
		Name:          "ccu1",
		PasswordPlain: "plaintext-secret",
	}
	cc, usedEnv := RowToCentralConfig(row, func(string) string { return "" })
	if cc.Password != "plaintext-secret" {
		t.Errorf("Password=%q want plaintext-secret", cc.Password)
	}
	if usedEnv {
		t.Error("usedEnv=true, want false (no PasswordEnv configured)")
	}
}

// TestRowToCentralConfigEnvPasswordResolved verifies that a row with
// PasswordEnv set, resolved by envLookup to a non-empty value, prefers
// the env value over PasswordPlain and reports usedEnv == true.
func TestRowToCentralConfigEnvPasswordResolved(t *testing.T) {
	t.Parallel()
	row := sqlite.CentralRow{
		Name:          "ccu1",
		PasswordPlain: "plaintext-fallback",
		PasswordEnv:   "CCU1_PASSWORD",
	}
	lookup := func(key string) string {
		if key == "CCU1_PASSWORD" {
			return "env-resolved-secret"
		}
		return ""
	}
	cc, usedEnv := RowToCentralConfig(row, lookup)
	if cc.Password != "env-resolved-secret" {
		t.Errorf("Password=%q want env-resolved-secret", cc.Password)
	}
	if !usedEnv {
		t.Error("usedEnv=false, want true (env var resolved)")
	}
}

// TestRowToCentralConfigEnvPasswordUnsetFallsBackToPlaintext verifies
// that a row with PasswordEnv set, but envLookup returning "" (var
// unset in the process environment), falls back to PasswordPlain and
// reports usedEnv == false.
func TestRowToCentralConfigEnvPasswordUnsetFallsBackToPlaintext(t *testing.T) {
	t.Parallel()
	row := sqlite.CentralRow{
		Name:          "ccu1",
		PasswordPlain: "plaintext-fallback",
		PasswordEnv:   "CCU1_PASSWORD_UNSET",
	}
	cc, usedEnv := RowToCentralConfig(row, func(string) string { return "" })
	if cc.Password != "plaintext-fallback" {
		t.Errorf("Password=%q want plaintext-fallback", cc.Password)
	}
	if usedEnv {
		t.Error("usedEnv=true, want false (env var unset)")
	}
}

// TestRowToCentralConfigFieldsRoundTrip verifies that every
// non-password field on a [sqlite.CentralRow] round-trips unchanged
// into the returned [config.CentralConfig] — Name, Host, Port,
// JSONRPCPort, Username, Interfaces, Ports, TLS,
// TLSInsecureSkipVerify, PrimaryInterface, Visibility, and Behavior.
func TestRowToCentralConfigFieldsRoundTrip(t *testing.T) {
	t.Parallel()
	sysvarInterval := 90 * time.Second
	enableSysvarScan := true
	row := sqlite.CentralRow{
		Name:                  "distinctive-name",
		Host:                  "10.9.8.7",
		Port:                  2010,
		JSONRPCPort:           2011,
		Username:              "distinctive-user",
		PasswordPlain:         "distinctive-password",
		Interfaces:            []config.InterfaceSpec{{Name: "HmIP-RF", Port: 2010}, {Name: "BidCos-RF"}},
		Ports:                 map[string]int{"HmIP-RF": 2010, "BidCos-RF": 2000},
		TLS:                   true,
		TLSInsecureSkipVerify: true,
		PrimaryInterface:      "HmIP-RF",
		Visibility:            config.VisibilityConfig{UnIgnore: []string{"*:*:LOWBAT"}},
		Behavior: config.CentralBehavior{
			EnableSysvarScan:   &enableSysvarScan,
			SysvarScanInterval: sysvarInterval,
		},
		Enabled: true,
	}

	cc, usedEnv := RowToCentralConfig(row, func(string) string { return "" })
	if usedEnv {
		t.Error("usedEnv=true, want false (no PasswordEnv configured)")
	}

	want := config.CentralConfig{
		Name:                  "distinctive-name",
		Host:                  "10.9.8.7",
		Port:                  2010,
		JSONRPCPort:           2011,
		Username:              "distinctive-user",
		Password:              "distinctive-password",
		Interfaces:            []config.InterfaceSpec{{Name: "HmIP-RF", Port: 2010}, {Name: "BidCos-RF"}},
		Ports:                 map[string]int{"HmIP-RF": 2010, "BidCos-RF": 2000},
		TLS:                   true,
		TLSInsecureSkipVerify: true,
		PrimaryInterface:      "HmIP-RF",
		Visibility:            config.VisibilityConfig{UnIgnore: []string{"*:*:LOWBAT"}},
		Behavior: config.CentralBehavior{
			EnableSysvarScan:   &enableSysvarScan,
			SysvarScanInterval: sysvarInterval,
		},
	}
	if !reflect.DeepEqual(cc, want) {
		t.Errorf("RowToCentralConfig field mismatch:\n got: %+v\nwant: %+v", cc, want)
	}
}

// TestApplyMarshalCoverAllSections is an anti-regression guard that
// ensures every section in AllSections() has a corresponding case in
// both applySection and marshalSection. An empty object must apply
// without error; ok may be false only for SectionSecurity (no
// config.Config target), so we do not assert ok.
func TestApplyMarshalCoverAllSections(t *testing.T) {
	t.Parallel()
	for _, sec := range AllSections() {
		t.Run(string(sec), func(t *testing.T) {
			t.Parallel()
			if err := applySection(sec, []byte("{}"), &config.Config{}); err != nil {
				t.Errorf("applySection(%q, {}) returned error (missing case?): %v", sec, err)
			}
			if _, _, err := marshalSection(sec, &config.Config{}); err != nil {
				t.Errorf("marshalSection(%q) returned error (missing case?): %v", sec, err)
			}
		})
	}
}

// TestOverlayIntoLeavesConfigUntouchedOnSectionFailure pins the
// all-or-nothing contract of the boot overlay. A section that cannot be
// read — the shape a sealed row takes once its master key is gone — used to
// abort the merge half-way: the sections layered before the failure were
// already written into the caller's config, the ones after it silently
// reverted to their YAML values, and ApplyDefaults never ran. The daemon
// then kept running on that half-merged config. After a failure the
// caller's config must be exactly the YAML tier it handed in.
func TestOverlayIntoLeavesConfigUntouchedOnSectionFailure(t *testing.T) {
	t.Parallel()
	sl := newFakeSectionLoader()
	// locale and north.mqtt are layered before north.matter (AllSections
	// order), so both would already have been applied when matter fails.
	sl.rows[string(SectionLocale)] = sqlite.SectionRow{
		Section:   string(SectionLocale),
		ValueJSON: []byte(`{"locale":"de"}`),
	}
	sl.rows[string(SectionMQTT)] = sqlite.SectionRow{
		Section:   string(SectionMQTT),
		ValueJSON: []byte(`{"enabled":true,"broker_url":"tcp://db:1883"}`),
	}
	sl.getErr[string(SectionMatter)] = errors.New("open secrets: encrypted value but no master key available")

	cfg := config.Default()
	cfg.Locale = "en"
	cfg.North.REST.PublicURL = "https://yaml.example.com"
	before := config.Clone(cfg)

	s := New(defaultBootstrap(), sl, nil)
	if _, err := s.OverlayInto(context.Background(), cfg); err == nil {
		t.Fatal("OverlayInto: want error from the unreadable section, got nil")
	}
	if !reflect.DeepEqual(cfg, before) {
		t.Errorf("config was mutated by a failed overlay:\n got: %+v\nwant: %+v", cfg, before)
	}
}

// TestOverlayIntoCommitsEveryLayeredSection is the positive half of the
// same contract: on success the caller's config carries every section that
// was layered, plus the defaults ApplyDefaults fills in.
func TestOverlayIntoCommitsEveryLayeredSection(t *testing.T) {
	t.Parallel()
	sl := newFakeSectionLoader()
	sl.rows[string(SectionLocale)] = sqlite.SectionRow{
		Section:   string(SectionLocale),
		ValueJSON: []byte(`{"locale":"de"}`),
	}
	sl.rows[string(SectionMQTT)] = sqlite.SectionRow{
		Section:   string(SectionMQTT),
		ValueJSON: []byte(`{"enabled":true,"broker_url":"tcp://db:1883"}`),
	}

	cfg := config.Default()
	cfg.Locale = "en"
	s := New(defaultBootstrap(), sl, nil)
	srcs, err := s.OverlayInto(context.Background(), cfg)
	if err != nil {
		t.Fatalf("OverlayInto: %v", err)
	}
	if cfg.Locale != "de" {
		t.Errorf("Locale=%q want de", cfg.Locale)
	}
	if cfg.North.MQTT.BrokerURL != "tcp://db:1883" {
		t.Errorf("BrokerURL=%q want tcp://db:1883", cfg.North.MQTT.BrokerURL)
	}
	if cfg.North.MQTT.TopicBase == "" {
		t.Error("TopicBase empty: ApplyDefaults must run after the merge")
	}
	if srcs[string(SectionMQTT)] != SourceDB {
		t.Errorf("Sources[north.mqtt]=%q want db", srcs[string(SectionMQTT)])
	}
}

// TestPlaintextSecretsAllowed pins the read side of the security section:
// the flag governs whether a central password may be persisted in the clear
// when no master key is available, so an unreadable, absent or malformed
// row must read as "not allowed" (the documented default).
func TestPlaintextSecretsAllowed(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		row  string
		err  error
		want bool
	}{
		{name: "absent row defaults to false"},
		{name: "explicit false", row: `{"allow_plaintext_secrets":false}`},
		{name: "explicit true", row: `{"allow_plaintext_secrets":true}`, want: true},
		{name: "malformed row defaults to false", row: `{broken`},
		{name: "unreadable row defaults to false", err: errors.New("open secrets")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sl := newFakeSectionLoader()
			if tc.row != "" {
				sl.rows[string(SectionSecurity)] = sqlite.SectionRow{
					Section:   string(SectionSecurity),
					ValueJSON: []byte(tc.row),
				}
			}
			if tc.err != nil {
				sl.getErr[string(SectionSecurity)] = tc.err
			}
			s := New(defaultBootstrap(), sl, nil)
			if got := s.PlaintextSecretsAllowed(context.Background()); got != tc.want {
				t.Errorf("PlaintextSecretsAllowed()=%v want %v", got, tc.want)
			}
		})
	}
}

// TestPlaintextSecretsAllowedWithoutSectionStore pins the degraded path: a
// Store with no section loader cannot read the operator's opt-in, so it must
// report the safe default rather than assuming consent.
func TestPlaintextSecretsAllowedWithoutSectionStore(t *testing.T) {
	t.Parallel()
	s := New(defaultBootstrap(), nil, nil)
	if s.PlaintextSecretsAllowed(context.Background()) {
		t.Error("PlaintextSecretsAllowed() = true without a section store, want false")
	}
}
