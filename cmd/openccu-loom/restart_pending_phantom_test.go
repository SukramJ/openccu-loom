// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/configstore"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// emptySectionLoader is a config_sections table with no rows — the state
// of a daemon whose operator has never saved anything in the SPA. Put
// and Delete are unreachable from the restart-pending path; they answer
// plausibly so the type satisfies the loader contract.
type emptySectionLoader struct{}

func (emptySectionLoader) Get(context.Context, string) (sqlite.SectionRow, error) {
	return sqlite.SectionRow{}, sqlite.ErrSectionNotFound
}

func (emptySectionLoader) Put(_ context.Context, section string, valueJSON []byte, updatedBy string) (sqlite.SectionRow, error) {
	return sqlite.SectionRow{Section: section, ValueJSON: valueJSON, UpdatedBy: updatedBy}, nil
}

func (emptySectionLoader) Delete(context.Context, string) error { return nil }

func (emptySectionLoader) List(context.Context) ([]sqlite.SectionRow, error) { return nil, nil }

// emptyCentralLoader is the centrals table of the same daemon.
type emptyCentralLoader struct{}

func (emptyCentralLoader) List(context.Context) ([]sqlite.CentralRow, error) { return nil, nil }

// storeBackedConfigService adapts the real configstore facade to the
// handler interface the provider consumes. Going through the real store
// is the point: a hand-written double would answer whatever the test
// wanted and could never reproduce the disagreement between the daemon's
// boot assembly and the store's own.
type storeBackedConfigService struct{ store *configstore.Store }

func (s storeBackedConfigService) Effective(ctx context.Context) (*configstore.EffectiveResult, error) {
	return s.store.Effective(ctx)
}

func (s storeBackedConfigService) GetSection(context.Context, configstore.Section) (sqlite.SectionRow, error) {
	return sqlite.SectionRow{}, sqlite.ErrSectionNotFound
}

func (s storeBackedConfigService) PutSection(_ context.Context, sec configstore.Section, v []byte, by string) (sqlite.SectionRow, error) {
	return sqlite.SectionRow{Section: string(sec), ValueJSON: v, UpdatedBy: by}, nil
}

func (s storeBackedConfigService) DeleteSection(context.Context, configstore.Section) error {
	return nil
}

// TestRestartPendingReportsNothingForAConfigOnlySetInYAML reproduces the
// banner an operator could not clear.
//
// backup.schedule is restart-required and no editable section carries it,
// so a value set in the config file existed on the daemon's side of the
// comparison and nowhere on the store's. The provider read that as a
// staged change and reported a restart as pending on every poll, forever,
// with no save to revert and no restart that would help.
func TestRestartPendingReportsNothingForAConfigOnlySetInYAML(t *testing.T) {
	t.Parallel()

	// The YAML tier the daemon booted from.
	yamlBase := config.Default()
	yamlBase.Backup.Schedule = 24 * time.Hour
	yamlBase.Backup.KeepLast = 7
	yamlBase.Callback.Port = 9120
	yamlBase.Centrals = []config.CentralConfig{{
		Name: "ccu1", Host: "192.0.2.10",
		Interfaces: []config.InterfaceSpec{{Name: "HmIP-RF"}},
	}}

	bootstrap := &config.BootstrapConfig{
		DataDir: yamlBase.DataDir,
		Logging: yamlBase.Logging,
		Listen:  config.BootstrapListen{REST: yamlBase.North.REST.Listen},
	}
	store := configstore.New(bootstrap, emptySectionLoader{}, emptyCentralLoader{},
		configstore.WithBaseConfig(yamlBase))

	// The daemon's own boot config: the YAML tier with the DB overlaid,
	// which for an empty database changes nothing.
	boot := config.Clone(yamlBase)
	if _, err := store.OverlayInto(context.Background(), boot); err != nil {
		t.Fatalf("OverlayInto: %v", err)
	}

	p := newRestartPendingProvider(boot, storeBackedConfigService{store: store})
	pending, fields, err := p.Pending(context.Background())
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if pending {
		t.Errorf("nothing was saved, yet a restart is reported as pending for %v", fields)
	}
}

// TestRestartPendingStillReportsARealChange keeps the fix from silencing
// the feature it repairs: a section the operator actually saved on a
// restart-required field must still light the banner.
func TestRestartPendingStillReportsARealChange(t *testing.T) {
	t.Parallel()

	yamlBase := config.Default()
	yamlBase.North.MCP.Enabled = false

	bootstrap := &config.BootstrapConfig{
		DataDir: yamlBase.DataDir,
		Logging: yamlBase.Logging,
		Listen:  config.BootstrapListen{REST: yamlBase.North.REST.Listen},
	}
	sections := &oneSectionLoader{
		section: string(configstore.SectionMCP),
		value:   []byte(`{"enabled":true,"allow_writes":false,"path":"/mcp"}`),
	}
	store := configstore.New(bootstrap, sections, emptyCentralLoader{},
		configstore.WithBaseConfig(yamlBase))

	boot := config.Clone(yamlBase)
	boot.ApplyDefaults()

	p := newRestartPendingProvider(boot, storeBackedConfigService{store: store})
	pending, fields, err := p.Pending(context.Background())
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if !pending {
		t.Fatal("a saved north.mcp.enabled change must be reported as pending")
	}
	if !contains(fields, "north.mcp.enabled") {
		t.Errorf("pending fields %v must name north.mcp.enabled", fields)
	}
}

// oneSectionLoader serves exactly one stored section row.
type oneSectionLoader struct {
	section string
	value   []byte
}

func (l *oneSectionLoader) Get(_ context.Context, section string) (sqlite.SectionRow, error) {
	if section != l.section {
		return sqlite.SectionRow{}, sqlite.ErrSectionNotFound
	}
	return sqlite.SectionRow{Section: section, ValueJSON: l.value}, nil
}

func (l *oneSectionLoader) Put(_ context.Context, section string, v []byte, by string) (sqlite.SectionRow, error) {
	return sqlite.SectionRow{Section: section, ValueJSON: v, UpdatedBy: by}, nil
}

func (l *oneSectionLoader) Delete(context.Context, string) error { return nil }

func (l *oneSectionLoader) List(context.Context) ([]sqlite.SectionRow, error) {
	return []sqlite.SectionRow{{Section: l.section, ValueJSON: l.value}}, nil
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
