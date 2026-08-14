// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package configstore

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// nestedRESTSections are the sections whose sub-tree lives inside
// config.NorthREST. A north.rest row that also carries them shadows their own
// rows, because applySection merges into an already-populated struct and
// layerSections applies north.rest before its nested sections.
var nestedRESTSections = []Section{SectionOIDC, SectionCCUAuth, SectionHAIngress}

// TestRESTSectionRowOmitsNestedSections pins the persist half of the rule: the
// north.rest row describes only what north.rest owns. Seeding it with the
// nested auth blocks would duplicate every value into two rows, and the
// duplicate is the one that wins at the next boot.
func TestRESTSectionRowOmitsNestedSections(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.North.REST.PublicURL = "https://loom.example"
	cfg.North.REST.Auth.HAIngress.Enabled = boolPtr(true)
	cfg.North.REST.Auth.HAIngress.Role = "admin"
	cfg.North.REST.Auth.CCU.Central = "ccu1"
	cfg.North.REST.Auth.OIDC.Issuer = "https://idp.example"

	raw, ok, err := marshalSection(SectionREST, cfg)
	if !ok || err != nil {
		t.Fatalf("marshalSection: ok=%v err=%v", ok, err)
	}
	for _, key := range []string{"ha_ingress", "ccu", "oidc"} {
		if strings.Contains(string(raw), `"`+key+`"`) {
			t.Errorf("north.rest row must not carry the nested %q block: %s", key, raw)
		}
	}
	// The fields north.rest genuinely owns are still there.
	if !strings.Contains(string(raw), "https://loom.example") {
		t.Errorf("north.rest row lost its own public_url: %s", raw)
	}
}

// TestNestedSectionFieldResetSurvivesEffectiveRebuild reproduces the reported
// defect: an operator disables the HA-Ingress auth passthrough by resetting
// north.rest.auth.ha_ingress.enabled (the REST handler drops the leaf from the
// nested row), and the value comes back on the next Effective() because the
// north.rest row still carried a copy. The passthrough is a deliberate auth
// bypass (ADR 0044), so a reset that does not stick is a security defect.
func TestNestedSectionFieldResetSurvivesEffectiveRebuild(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	sl := newFakeSectionLoader()

	// First boot seeds every section from a YAML config with the passthrough on.
	seeded := config.Default()
	seeded.North.REST.Auth.HAIngress.Enabled = boolPtr(true)
	s := New(defaultBootstrap(), sl, nil)
	if _, err := s.SeedSectionsFromConfig(ctx, seeded, "yaml"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// The operator resets the single field: the leaf is removed from the
	// nested row, which is emptied and therefore dropped entirely.
	delete(sl.rows, string(SectionHAIngress))

	res, err := s.Effective(ctx)
	if err != nil {
		t.Fatalf("Effective: %v", err)
	}
	if got := res.Config.North.REST.Auth.HAIngress.Enabled; got != nil && *got {
		t.Fatalf("ha_ingress.enabled came back as true after the reset — the north.rest row shadowed it")
	}
	if src := res.Sources["north.rest.auth.ha_ingress.enabled"]; src == SourceDB {
		t.Errorf("reset field still attributed to the DB tier: %q", src)
	}
}

// TestNestedSectionValueDoesNotLeakFromRESTRow is the load-half guard for rows
// written before the nested sections owned their sub-trees: a stale north.rest
// row that still carries them must not resurrect the values.
func TestNestedSectionValueDoesNotLeakFromRESTRow(t *testing.T) {
	t.Parallel()

	sl := newFakeSectionLoader()
	sl.rows[string(SectionREST)] = sqlite.SectionRow{
		Section: string(SectionREST),
		ValueJSON: []byte(`{"public_url":"https://b.example","auth":{` +
			`"ha_ingress":{"enabled":true,"role":"admin"},` +
			`"ccu":{"enabled":true,"central":"ccu1"},` +
			`"oidc":{"issuer":"https://idp.example"}}}`),
	}

	s := New(defaultBootstrap(), sl, nil)
	res, err := s.Effective(context.Background())
	if err != nil {
		t.Fatalf("Effective: %v", err)
	}
	auth := res.Config.North.REST.Auth
	if auth.HAIngress.Enabled != nil {
		t.Errorf("stale north.rest row re-enabled the HA-Ingress passthrough: %v", *auth.HAIngress.Enabled)
	}
	if auth.CCU.Enabled != nil {
		t.Errorf("stale north.rest row re-enabled CCU-delegated login: %v", *auth.CCU.Enabled)
	}
	if auth.OIDC.Issuer != "" {
		t.Errorf("stale north.rest row set the OIDC issuer: %q", auth.OIDC.Issuer)
	}
	// The fields north.rest owns still apply.
	if res.Config.North.REST.PublicURL != "https://b.example" {
		t.Errorf("public_url=%q want https://b.example", res.Config.North.REST.PublicURL)
	}
}

// TestNestedSectionRowsStillCarryTheirOwnValues is the positive companion: the
// nested sections keep working through their own rows.
func TestNestedSectionRowsStillCarryTheirOwnValues(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	sl := newFakeSectionLoader()
	seeded := config.Default()
	seeded.North.REST.Auth.HAIngress.Enabled = boolPtr(true)
	seeded.North.REST.Auth.HAIngress.Role = "operator"
	seeded.North.REST.Auth.CCU.Central = "ccu1"
	seeded.North.REST.Auth.OIDC.Issuer = "https://idp.example"

	s := New(defaultBootstrap(), sl, nil)
	if _, err := s.SeedSectionsFromConfig(ctx, seeded, "yaml"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	for _, sec := range nestedRESTSections {
		if _, ok := sl.rows[string(sec)]; !ok {
			t.Fatalf("section %q was not seeded", sec)
		}
	}

	res, err := s.Effective(ctx)
	if err != nil {
		t.Fatalf("Effective: %v", err)
	}
	auth := res.Config.North.REST.Auth
	if auth.HAIngress.Enabled == nil || !*auth.HAIngress.Enabled {
		t.Error("ha_ingress.enabled lost — the nested row must still apply")
	}
	if auth.HAIngress.Role != "operator" {
		t.Errorf("ha_ingress.role=%q want operator", auth.HAIngress.Role)
	}
	if auth.CCU.Central != "ccu1" {
		t.Errorf("ccu.central=%q want ccu1", auth.CCU.Central)
	}
	if auth.OIDC.Issuer != "https://idp.example" {
		t.Errorf("oidc.issuer=%q want https://idp.example", auth.OIDC.Issuer)
	}
}

// TestApplySectionReplacesMapValuedFields pins the overlay rule for maps:
// a key the payload carries is authoritative, so the payload's map
// replaces the stored one instead of merging into it. Go's encoding/json
// keeps the entries of a non-nil destination map, which made deleting a
// single entry impossible through the section PUT — the operator saw a
// success and the entry back on the next load.
func TestApplySectionReplacesMapValuedFields(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.North.REST.Auth.CCU.RoleMapping = map[string]string{"8": "admin", "1": "admin"}
	cfg.North.REST.Auth.CCU.MinUserLevel = 2

	if err := ApplySectionToConfig(SectionCCUAuth, []byte(`{"role_mapping":{"8":"admin"}}`), cfg); err != nil {
		t.Fatalf("ApplySectionToConfig: %v", err)
	}
	got := cfg.North.REST.Auth.CCU.RoleMapping
	if len(got) != 1 || got["8"] != "admin" {
		t.Errorf("role_mapping=%v, want exactly {8:admin}", got)
	}
	// A key the payload omits still keeps its stored value.
	if cfg.North.REST.Auth.CCU.MinUserLevel != 2 {
		t.Errorf("min_user_level=%d, want the stored 2", cfg.North.REST.Auth.CCU.MinUserLevel)
	}
}

// TestApplySectionKeepsMapWhenPayloadOmitsIt is the other half of the
// rule: an absent key means "no opinion", so the stored map survives.
func TestApplySectionKeepsMapWhenPayloadOmitsIt(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.North.REST.Auth.CCU.RoleMapping = map[string]string{"8": "admin"}

	if err := ApplySectionToConfig(SectionCCUAuth, []byte(`{"min_user_level":3}`), cfg); err != nil {
		t.Fatalf("ApplySectionToConfig: %v", err)
	}
	if len(cfg.North.REST.Auth.CCU.RoleMapping) != 1 {
		t.Errorf("role_mapping=%v, want the stored map untouched", cfg.North.REST.Auth.CCU.RoleMapping)
	}
}

// TestApplySectionReplacesNestedMapValuedFields covers a map that sits
// below the section root: north.ui.profiles maps a profile name to the
// per-surface states, and dropping a profile must not resurrect it.
func TestApplySectionReplacesNestedMapValuedFields(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.North.UI.Profiles = map[string]map[string]config.SurfaceState{
		config.ProfileStandalone: {"devices": config.SurfaceHidden},
		config.ProfileEmbedded:   {"devices": config.SurfaceHidden},
	}

	raw := []byte(`{"profiles":{"` + config.ProfileStandalone + `":{"devices":"hidden"}}}`)
	if err := ApplySectionToConfig(SectionUI, raw, cfg); err != nil {
		t.Fatalf("ApplySectionToConfig: %v", err)
	}
	if _, still := cfg.North.UI.Profiles[config.ProfileEmbedded]; still {
		t.Errorf("profiles=%v, want the dropped profile gone", cfg.North.UI.Profiles)
	}
}

// TestForeignRelPathsCoverEveryNestedSection keeps the strip list derived from
// AllSections rather than hand-maintained: a section added underneath another
// one is stripped from its parent automatically.
func TestForeignRelPathsCoverEveryNestedSection(t *testing.T) {
	t.Parallel()

	for _, parent := range AllSections() {
		rels := make(map[string]struct{}, len(foreignRelPaths(parent)))
		for _, r := range foreignRelPaths(parent) {
			rels[r] = struct{}{}
		}
		for _, child := range AllSections() {
			if child == parent {
				continue
			}
			rel, nested := strings.CutPrefix(string(child), string(parent)+".")
			if !nested {
				continue
			}
			if _, ok := rels[rel]; !ok {
				t.Errorf("section %q does not strip its nested section %q (relative path %q)", parent, child, rel)
			}
		}
	}
}

// TestMarshalSectionRoundTripsThroughApply proves the exported marshal/apply
// pair is symmetric for every config-backed section — the property the REST
// section PUT relies on when it persists the candidate it validated.
func TestMarshalSectionRoundTripsThroughApply(t *testing.T) {
	t.Parallel()

	src := config.Default()
	src.North.MQTT.Enabled = true
	src.North.MQTT.BrokerURL = "tcp://broker.example:1883"
	src.North.MQTT.TopicBase = "homematic"
	src.Alarm.DefaultSirenSeconds = 42

	for _, sec := range AllSections() {
		raw, ok, err := MarshalSection(sec, src)
		if err != nil {
			t.Fatalf("MarshalSection(%q): %v", sec, err)
		}
		if !ok {
			continue
		}
		dst := config.Default()
		if err := ApplySectionToConfig(sec, raw, dst); err != nil {
			t.Fatalf("ApplySectionToConfig(%q): %v", sec, err)
		}
		again, _, err := MarshalSection(sec, dst)
		if err != nil {
			t.Fatalf("MarshalSection(%q) second pass: %v", sec, err)
		}
		if !jsonEqual(t, raw, again) {
			t.Errorf("section %q is not marshal/apply symmetric:\n first: %s\nsecond: %s", sec, raw, again)
		}
	}
}

func jsonEqual(t *testing.T, a, b []byte) bool {
	t.Helper()
	var ma, mb any
	if err := json.Unmarshal(a, &ma); err != nil {
		t.Fatalf("unmarshal %s: %v", a, err)
	}
	if err := json.Unmarshal(b, &mb); err != nil {
		t.Fatalf("unmarshal %s: %v", b, err)
	}
	ra, _ := json.Marshal(ma)
	rb, _ := json.Marshal(mb)
	return bytes.Equal(ra, rb)
}
