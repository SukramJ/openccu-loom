// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package config

import (
	"os"
	"path/filepath"
)

// ConfigEnvVar is the environment variable that, when set, names a config
// file to use before the conventional search locations are probed.
const ConfigEnvVar = "OPENCCU_LOOM_CONFIG"

// SearchPaths returns the ordered candidate locations the daemon probes
// for a config file when no explicit --config flag is given. The first
// existing file wins; if none exists the daemon runs on built-in
// defaults. Order (highest precedence first):
//
//  1. $OPENCCU_LOOM_CONFIG          — explicit operator override
//  2. ./config.yaml                 — current working directory (in the
//     container WORKDIR /app, i.e. /app/config.yaml)
//  3. <user-config>/openccu-loom/config.yaml — $XDG_CONFIG_HOME or
//     ~/.config (per-user, for local CLI use)
//  4. /etc/openccu-loom/config.yaml — system-wide
//
// A path is included even when the file is absent; [DiscoverConfigPath]
// is responsible for picking the first that exists.
func SearchPaths() []string {
	var paths []string
	if p := os.Getenv(ConfigEnvVar); p != "" {
		paths = append(paths, p)
	}
	paths = append(paths, "config.yaml")
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		paths = append(paths, filepath.Join(x, "openccu-loom", "config.yaml"))
	} else if home, err := os.UserHomeDir(); err == nil && home != "" {
		paths = append(paths, filepath.Join(home, ".config", "openccu-loom", "config.yaml"))
	}
	// System-wide Unix location. A plain literal (not filepath.Join) keeps
	// gocritic happy and the path is a Unix-only convention anyway.
	paths = append(paths, "/etc/openccu-loom/config.yaml")
	return paths
}

// DiscoverConfigPath returns the first existing, regular file from
// [ConfigSearchPaths], or "" when none of them exists. Callers use the
// empty result to fall back to [Default].
func DiscoverConfigPath() string {
	for _, p := range SearchPaths() {
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
			return p
		}
	}
	return ""
}
