// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
)

// publicURLHintFile is the basename of the hint file the daemon writes
// into its data dir. The CCU add-on's config.cgi reads it to link its
// "Open Config UI" button straight at the operator-configured external
// URL (packaging/ccu-addon/ccu/www/config.cgi).
const publicURLHintFile = "public_url"

// writeConfigUIHint persists the externally-reachable Config-UI URL to
// <dataDir>/public_url so a reverse-proxy install can link the add-on
// button at the proxy instead of the direct host:port heuristic. An
// empty url removes the hint, reverting the add-on to that heuristic.
//
// Best-effort by design: the hint only improves the add-on landing
// page, so a write/remove failure is logged and never fatal — the
// add-on degrades to its built-in fallback. The data-dir fallback to
// ./var mirrors the audit/backup wiring (audit_wiring.go).
func writeConfigUIHint(dataDir, url string, logger *slog.Logger) {
	if dataDir == "" {
		dataDir = "./var"
	}
	path := filepath.Join(dataDir, publicURLHintFile)
	if url == "" {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			logger.Warn("config_ui_hint.remove_failed",
				slog.String("path", path), slog.String("err", err.Error()))
		}
		return
	}
	if err := os.WriteFile(path, []byte(url+"\n"), 0o644); err != nil {
		logger.Warn("config_ui_hint.write_failed",
			slog.String("path", path), slog.String("err", err.Error()))
	}
}
