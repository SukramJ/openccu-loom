// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package diagnostics

import (
	"bytes"
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
	// Anonymise controls whether the archive's operator-identifying
	// attributes are hashed — the `subject`, `user`, `username`, `remote`
	// and `remote_addr` keys; addresses and parameter names stay in clear
	// text. A body or file that omits the key means "yes" — see
	// [StartupCaptureConfig.UnmarshalJSON].
	Anonymise bool `json:"anonymise"`
}

// UnmarshalJSON decodes the config, defaulting Anonymise to true whenever the
// key is absent. Plain bool decoding would turn every payload that only flips
// `enabled` into a persisted `anonymise: false`, and the boot capture cannot
// recover from that: [Manager.Start]'s anonymise-by-default fallback only
// applies to captures with no trigger, while the startup path always passes
// one. The next boot would then archive raw device addresses without anyone
// having asked for it. An explicit `"anonymise": false` is still honoured.
//
// Unknown keys are rejected, mirroring the strictness the REST decoder applies
// to every other request body — a payload we cannot fully interpret must not
// silently configure what the daemon records about itself.
func (c *StartupCaptureConfig) UnmarshalJSON(data []byte) error {
	// The alias sheds this method so the nested decode does not recurse; the
	// pointer field shadows the embedded bool and records "key was present".
	type alias StartupCaptureConfig
	var raw struct {
		alias
		Anonymise *bool `json:"anonymise"`
	}
	raw.Anonymise = nil
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&raw); err != nil {
		return err
	}
	*c = StartupCaptureConfig(raw.alias)
	c.Anonymise = raw.Anonymise == nil || *raw.Anonymise
	return nil
}

// DefaultStartupCapture is the config a daemon that has never been configured
// runs with: no boot capture, and anonymised archives if one is ever enabled.
// It is also what the REST surface renders before the first write, so an
// operator who toggles `enabled` and sends the form back does not carry an
// accidental `anonymise: false` with it.
func DefaultStartupCapture() StartupCaptureConfig {
	return StartupCaptureConfig{Anonymise: true}
}

// LoadStartupCapture reads the persisted config from dataDir. A
// missing file is treated as [DefaultStartupCapture] rather than an error
// so a fresh daemon boot does not require any explicit init step.
func LoadStartupCapture(dataDir string) (StartupCaptureConfig, error) {
	if dataDir == "" {
		return DefaultStartupCapture(), nil
	}
	path := filepath.Join(dataDir, StartupCaptureFileName)
	raw, err := os.ReadFile(path) //nolint:gosec // path is composed from the operator-controlled data dir; see #20
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return DefaultStartupCapture(), nil
		}
		return DefaultStartupCapture(), fmt.Errorf("diagnostics: read startup capture: %w", err)
	}
	var cfg StartupCaptureConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return DefaultStartupCapture(), fmt.Errorf("diagnostics: parse startup capture: %w", err)
	}
	return cfg, nil
}

// SaveStartupCapture writes the config back to disk. The file is
// created with 0600 permissions because it pins behaviour that
// affects what the daemon collects about itself; operators on a
// shared host should not be able to flip it without admin access.
func SaveStartupCapture(dataDir string, cfg StartupCaptureConfig) error {
	if dataDir == "" {
		return errors.New("diagnostics: data dir required to persist startup capture")
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
