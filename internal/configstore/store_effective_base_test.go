// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package configstore

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// yamlTierConfig builds the kind of config a daemon loads from disk: a
// handful of fields the operator set, spread over a section that exists
// (north.mqtt), a section-less block (backup) and a top-level leaf
// (locale).
func yamlTierConfig() *config.Config {
	c := config.Default()
	c.Locale = "de"
	c.Backup.Schedule = 12 * time.Hour
	c.Backup.KeepLast = 4
	c.North.MQTT.Enabled = true
	c.North.MQTT.BrokerURL = "tcp://broker.example:1883"
	c.North.Webhook.ParameterGlob = "*TEMPERATURE*"
	return c
}

// TestEffectiveKeepsFieldsThatExistOnlyInTheYAMLTier is the guard for the
// disagreement between the two config assemblies: the daemon boots from
// the YAML tier and overlays the database on top, while Effective used to
// start from the built-in defaults. Every field an operator had set in
// YAML and never touched in the SPA therefore came back from
// GET /api/v1/config as its default.
//
// backup.schedule is the case with teeth: it is restart-required and no
// editable section carries it, so the seed cannot put it in the database
// and nothing could ever make the two sides agree again.
func TestEffectiveKeepsFieldsThatExistOnlyInTheYAMLTier(t *testing.T) {
	t.Parallel()

	base := yamlTierConfig()
	s := New(defaultBootstrap(), newFakeSectionLoader(), &fakeCentralLoader{}, WithBaseConfig(base))

	res, err := s.Effective(context.Background())
	if err != nil {
		t.Fatalf("Effective: %v", err)
	}
	got := res.Config

	if got.Backup.Schedule != base.Backup.Schedule {
		t.Errorf("backup.schedule: want %s from the YAML tier, got %s", base.Backup.Schedule, got.Backup.Schedule)
	}
	if got.Backup.KeepLast != base.Backup.KeepLast {
		t.Errorf("backup.keep_last: want %d, got %d", base.Backup.KeepLast, got.Backup.KeepLast)
	}
	if got.Locale != "de" {
		t.Errorf("locale: want %q, got %q", "de", got.Locale)
	}
	if got.North.MQTT.BrokerURL != base.North.MQTT.BrokerURL {
		t.Errorf("north.mqtt.broker_url: want %q, got %q", base.North.MQTT.BrokerURL, got.North.MQTT.BrokerURL)
	}
	if got.North.Webhook.ParameterGlob != base.North.Webhook.ParameterGlob {
		t.Errorf("north.webhook.parameter_glob: want %q, got %q",
			base.North.Webhook.ParameterGlob, got.North.Webhook.ParameterGlob)
	}
}

// TestEffectiveAgainstTheYAMLTierReportsNoPendingRestart states the
// symptom the operator saw: with no saved section at all, the daemon's
// running config and the assembled effective config must be identical on
// every restart-required field, so the restart banner stays dark.
//
// The provider computes exactly this diff, so a non-empty result here is
// a restart prompt that no operator action can clear.
func TestEffectiveAgainstTheYAMLTierReportsNoPendingRestart(t *testing.T) {
	t.Parallel()

	base := yamlTierConfig()
	s := New(defaultBootstrap(), newFakeSectionLoader(), &fakeCentralLoader{}, WithBaseConfig(base))

	res, err := s.Effective(context.Background())
	if err != nil {
		t.Fatalf("Effective: %v", err)
	}
	// The running daemon applies the bootstrap tier to its own config the
	// same way Effective does, so the comparison baseline has to carry it.
	boot := config.Clone(base)
	boot.DataDir = defaultBootstrap().DataDir
	boot.Logging = defaultBootstrap().Logging
	boot.North.REST.Listen = defaultBootstrap().Listen.REST
	boot.ApplyDefaults()

	if pending := config.RestartRequiredDiff(boot, res.Config); len(pending) != 0 {
		t.Errorf("nothing was saved, yet the restart banner would report %v as pending", pending)
	}
}

// TestEffectiveLetsAStoredSectionWinOverTheYAMLTier keeps the fix from
// overshooting: the YAML tier is the base, not the authority. A section
// the operator saved in the SPA must still override the file.
func TestEffectiveLetsAStoredSectionWinOverTheYAMLTier(t *testing.T) {
	t.Parallel()

	base := yamlTierConfig()
	sections := newFakeSectionLoader()
	row, err := json.Marshal(map[string]any{
		"enabled":    true,
		"broker_url": "tcp://saved.example:1883",
		"topic_base": "openccu-loom",
	})
	if err != nil {
		t.Fatalf("marshal section: %v", err)
	}
	sections.rows[string(SectionMQTT)] = sqlite.SectionRow{Section: string(SectionMQTT), ValueJSON: row}

	s := New(defaultBootstrap(), sections, &fakeCentralLoader{}, WithBaseConfig(base))
	res, err := s.Effective(context.Background())
	if err != nil {
		t.Fatalf("Effective: %v", err)
	}
	if got := res.Config.North.MQTT.BrokerURL; got != "tcp://saved.example:1883" {
		t.Errorf("broker_url: the stored section must win over the YAML tier, got %q", got)
	}
	if src := res.Sources["north.mqtt.broker_url"]; src != SourceDB {
		t.Errorf("north.mqtt.broker_url source: want %q, got %q", SourceDB, src)
	}
	// A field only the file carries keeps reporting where it came from,
	// rather than being mislabelled as a built-in default.
	if src := res.Sources["backup.schedule"]; src != SourceBootstrap {
		t.Errorf("backup.schedule source: want %q, got %q", SourceBootstrap, src)
	}
}

// TestEffectiveWithoutABaseFallsBackToDefaults covers the daemon that
// booted without a config file at all: there is no YAML tier to pin, and
// the assembly must still produce a usable config rather than a zero one.
func TestEffectiveWithoutABaseFallsBackToDefaults(t *testing.T) {
	t.Parallel()

	s := New(defaultBootstrap(), newFakeSectionLoader(), &fakeCentralLoader{})
	res, err := s.Effective(context.Background())
	if err != nil {
		t.Fatalf("Effective: %v", err)
	}
	if res.Config.Locale != config.Default().Locale {
		t.Errorf("locale: want the built-in default %q, got %q", config.Default().Locale, res.Config.Locale)
	}
	if res.Config.Callback.Port != config.Default().Callback.Port {
		t.Errorf("callback.port: want %d, got %d", config.Default().Callback.Port, res.Config.Callback.Port)
	}
}

// TestWithBaseConfigClonesItsInput pins the store against a later
// in-place reload of the daemon's config: Effective must keep replaying
// the tier the daemon booted from, not follow whatever that config
// object has become since.
func TestWithBaseConfigClonesItsInput(t *testing.T) {
	t.Parallel()

	base := yamlTierConfig()
	s := New(defaultBootstrap(), newFakeSectionLoader(), &fakeCentralLoader{}, WithBaseConfig(base))
	base.Backup.Schedule = 99 * time.Hour

	res, err := s.Effective(context.Background())
	if err != nil {
		t.Fatalf("Effective: %v", err)
	}
	if res.Config.Backup.Schedule != 12*time.Hour {
		t.Errorf("backup.schedule: want the pinned 12h, got %s", res.Config.Backup.Schedule)
	}
}
