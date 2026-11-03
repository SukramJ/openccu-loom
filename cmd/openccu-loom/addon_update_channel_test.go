// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/configstore"
)

// TestAddonUpdateChannelResolverReadsSavedSection pins the channel
// resolver wireAddonUpdate hands the updater: it reads the DB-tier
// addon_update section on every call, so a channel the operator saves
// in the SPA after boot reaches the next check, and the YAML base value
// it overlays is not what wins.
func TestAddonUpdateChannelResolverReadsSavedSection(t *testing.T) {
	t.Parallel()

	yamlBase := config.Default()
	yamlBase.AddonUpdate.IncludePrereleases = false
	bootstrap := &config.BootstrapConfig{
		DataDir: yamlBase.DataDir,
		Logging: yamlBase.Logging,
		Listen:  config.BootstrapListen{REST: yamlBase.North.REST.Listen},
	}
	sections := &oneSectionLoader{
		section: string(configstore.SectionAddonUpdate),
		value:   []byte(`{"include_prereleases":false}`),
	}
	store := configstore.New(bootstrap, sections, emptyCentralLoader{},
		configstore.WithBaseConfig(yamlBase))
	ov := &auditOverlay{configStore: store, yamlBase: yamlBase}

	resolve := addonUpdateChannelResolver(config.Clone(yamlBase), ov, nil)
	if resolve(context.Background()) {
		t.Fatal("saved include_prereleases:false must resolve to the final channel")
	}

	// The operator opts in after boot; no restart, no new resolver.
	sections.value = []byte(`{"include_prereleases":true}`)
	if !resolve(context.Background()) {
		t.Fatal("a channel saved after boot must reach the next check")
	}
}

// TestAddonUpdateChannelResolverWithoutStoreUsesBootConfig pins the
// fallback for a daemon without a config store: the booted value holds.
func TestAddonUpdateChannelResolverWithoutStoreUsesBootConfig(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.AddonUpdate.IncludePrereleases = true
	if !addonUpdateChannelResolver(cfg, nil, nil)(context.Background()) {
		t.Fatal("without a config store the booted include_prereleases must hold")
	}
}
