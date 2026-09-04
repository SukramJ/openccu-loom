// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package config

import "testing"

// w2CfgLoggingCandidates are the level and format spellings the two
// tiers are asked about. The list deliberately mixes the documented
// values with the plausible next additions ("trace", "fatal",
// "text-plain") and near-misses, because the defect this guards is a
// one-sided edit: a value accepted by one tier and refused by the other.
var w2CfgLoggingCandidates = struct {
	levels  []string
	formats []string
}{
	levels:  []string{"debug", "info", "warn", "error", "trace", "fatal", "Info", "warning", ""},
	formats: []string{"json", "text", "text-color", "text-plain", "logfmt", "JSON", ""},
}

// TestW2CfgBothConfigTiersAcceptTheSameLoggingValues pins that
// [BootstrapConfig.Validate] and [Config.Validate] answer identically
// for every candidate spelling of logging.level and logging.format.
//
// Both tiers validate the same [LoggingConfig] value, at two moments of
// the same boot: the bootstrap tier before the database is open (it is
// what `openccu-loom backup` and the env-file lookup run on), the full
// tier on every load and every section save. While each tier carried its
// own literal chain, adding a level to one of them was invisible — the
// daemon booted on `logging.level: trace` while the backup command
// aborted with "config: invalid logging.level", and the bootstrap parse
// error at daemon start is discarded, so an env_file setting would go
// missing with no log line.
//
// The test asserts agreement, not a domain, so it keeps biting whichever
// side a future value is added to.
func TestW2CfgBothConfigTiersAcceptTheSameLoggingValues(t *testing.T) {
	t.Parallel()

	t.Run("level", func(t *testing.T) {
		t.Parallel()
		for _, level := range w2CfgLoggingCandidates.levels {
			full := Default()
			full.Logging.Level = level
			boot := DefaultBootstrap()
			boot.Logging.Level = level
			fullOK := full.Validate() == nil
			bootOK := boot.Validate() == nil
			if fullOK != bootOK {
				t.Errorf("logging.level %q: full tier accepts=%v, bootstrap tier accepts=%v — the two tiers disagree", level, fullOK, bootOK)
			}
		}
	})

	t.Run("format", func(t *testing.T) {
		t.Parallel()
		for _, format := range w2CfgLoggingCandidates.formats {
			full := Default()
			full.Logging.Format = format
			boot := DefaultBootstrap()
			boot.Logging.Format = format
			fullOK := full.Validate() == nil
			bootOK := boot.Validate() == nil
			if fullOK != bootOK {
				t.Errorf("logging.format %q: full tier accepts=%v, bootstrap tier accepts=%v — the two tiers disagree", format, fullOK, bootOK)
			}
		}
	})

	// Agreement alone would also hold if both tiers rejected everything,
	// so the documented values are pinned as accepted.
	t.Run("the documented values stay accepted", func(t *testing.T) {
		t.Parallel()
		for _, level := range []string{"debug", "info", "warn", "error"} {
			cfg := Default()
			cfg.Logging.Level = level
			assertAccepted(t, cfg.Validate())
		}
		for _, format := range []string{"json", "text", "text-color"} {
			cfg := Default()
			cfg.Logging.Format = format
			assertAccepted(t, cfg.Validate())
		}
	})
}

// TestW2CfgBothConfigTiersDefaultTheSameBootValues pins the four
// defaults both tiers fill in — the state directory, the REST bind
// address and the two logging leaves.
//
// hmcli and the daemon resolving different data directories is a failure
// this project has already had once (see [BootstrapConfig.OverlayFromEnv]),
// and it is silent: the daemon opens a database in one place while a CLI
// subcommand reads an empty one somewhere else.
func TestW2CfgBothConfigTiersDefaultTheSameBootValues(t *testing.T) {
	t.Parallel()
	full := Default()
	boot := DefaultBootstrap()
	cases := []struct {
		field string
		full  string
		boot  string
	}{
		{"data_dir", full.DataDir, boot.DataDir},
		{"listen.rest", full.North.REST.Listen, boot.Listen.REST},
		{"logging.level", full.Logging.Level, boot.Logging.Level},
		{"logging.format", full.Logging.Format, boot.Logging.Format},
	}
	for _, tc := range cases {
		if tc.full != tc.boot {
			t.Errorf("%s: full tier defaults to %q, bootstrap tier to %q — the two tiers disagree", tc.field, tc.full, tc.boot)
		}
	}
}
