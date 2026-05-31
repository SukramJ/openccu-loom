// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package diagnostics

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// StartupCaptureFileName is the on-disk persistence file under the
// daemon's data directory.
const StartupCaptureFileName = "startup_capture.json"

// StartupCaptureConfig is the persisted shape that controls whether a
// capture is auto-started on the next daemon boot. Operators can
// edit this through the REST surface (UI toggle) without restarting
// the daemon themselves; the effective change applies on the next
// boot.
type StartupCaptureConfig struct {
	// Enabled toggles the boot-time capture. The daemon checks this
	// after the logger stack is up and before the CCU wiring begins,
	// so the bootstrap phase (the very thing operators usually want
	// to capture) is included in the archive.
	Enabled bool `json:"enabled"`
	// DurationS bounds the capture. Zero falls back to
	// [DefaultCaptureDuration]; values larger than
	// [MaxCaptureDuration] are clamped down to that cap.
	DurationS int `json:"duration_seconds"`
	// Anonymise controls whether device-address-shaped values in the
	// archive are hashed. Defaults to true on first write.
	Anonymise bool `json:"anonymise"`
}

// LoadStartupCapture reads the persisted config from dataDir. A
// missing file is treated as `{enabled: false}` rather than an error
// so a fresh daemon boot does not require any explicit init step.
func LoadStartupCapture(dataDir string) (StartupCaptureConfig, error) {
	if dataDir == "" {
		return StartupCaptureConfig{}, nil
	}
	path := filepath.Join(dataDir, StartupCaptureFileName)
	raw, err := os.ReadFile(path) //nolint:gosec // path is composed from the operator-controlled data dir
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return StartupCaptureConfig{}, nil
		}
		return StartupCaptureConfig{}, fmt.Errorf("diagnostics: read startup capture: %w", err)
	}
	var cfg StartupCaptureConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return StartupCaptureConfig{}, fmt.Errorf("diagnostics: parse startup capture: %w", err)
	}
	return cfg, nil
}

// SaveStartupCapture writes the config back to disk. The file is
// created with 0600 permissions because it pins behaviour that
// affects what the daemon collects about itself; operators on a
// shared host should not be able to flip it without admin access.
func SaveStartupCapture(dataDir string, cfg StartupCaptureConfig) error {
	if dataDir == "" {
		return fmt.Errorf("diagnostics: data dir required to persist startup capture")
	}
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return fmt.Errorf("diagnostics: mkdir data dir: %w", err)
	}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("diagnostics: marshal startup capture: %w", err)
	}
	path := filepath.Join(dataDir, StartupCaptureFileName)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("diagnostics: write startup capture: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("diagnostics: rename startup capture: %w", err)
	}
	return nil
}
