// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package outputs

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// Bounds of the output layer. The per-activation ceiling mirrors S1;
// optical-only activations may run longer (no noise constraint) but
// stay finite.
const (
	// MaxAcousticSeconds is the hard per-activation ceiling for any
	// acoustic output. Not configurable upward.
	MaxAcousticSeconds = 600
	// DefaultOpticalSeconds bounds one optical-only activation when
	// the output does not configure one.
	DefaultOpticalSeconds = 600
	// MaxOpticalSeconds is the hard ceiling for optical activations.
	MaxOpticalSeconds = 3600
	// stopVerifyInterval spaces the read-back/retry attempts of the
	// stop watchdog.
	stopVerifyInterval = 10 * time.Second
	// chirpMinGap rate-limits chirp emissions per output; ticks that
	// arrive faster are dropped (S5: chirps degrade first).
	chirpMinGap = 2 * time.Second
)

// OutputConfig is the per-output configuration document stored in
// alarm_outputs.config_json.
type OutputConfig struct {
	// Modes lists the protection levels this output fires for; empty
	// means every mode.
	Modes []hmenum.AlarmMode `json:"modes,omitempty"`
	// Outdoor marks outdoor sirens for the per-mode indoor/outdoor
	// split.
	Outdoor bool `json:"outdoor,omitempty"`
	// SharedWithCCU declares a third-party owner (CCU programs):
	// reconciliation never auto-stops this output while its area is
	// disarmed (S4).
	SharedWithCCU bool `json:"shared_with_ccu,omitempty"`
	// DurationSeconds bounds one acoustic activation; 0 selects the
	// engine default. Clamped to MaxAcousticSeconds.
	DurationSeconds int `json:"duration_s,omitempty"`
	// OpticalSeconds bounds one optical activation; 0 selects
	// DefaultOpticalSeconds. Clamped to MaxOpticalSeconds.
	OpticalSeconds int `json:"optical_duration_s,omitempty"`
	// AcousticTone / OpticalPattern are device value-list labels; an
	// empty tone lets the device play its default alarm tone.
	AcousticTone   string `json:"acoustic_tone,omitempty"`
	OpticalPattern string `json:"optical_pattern,omitempty"`
	// Level is the dimmer level for actuator-backed outputs (0..1);
	// nil selects the device's last level.
	Level *float64 `json:"level,omitempty"`
	// Chirp tone labels per kind (ASIR confirmation-tone value-list
	// labels). Empty labels skip that kind on this output.
	ChirpArmTone    string `json:"chirp_arm_tone,omitempty"`
	ChirpDisarmTone string `json:"chirp_disarm_tone,omitempty"`
	ChirpTickTone   string `json:"chirp_tick_tone,omitempty"`
	// SoundfileIndex / Volume drive MP3-player chirp outputs.
	SoundfileIndex int      `json:"soundfile_index,omitempty"`
	Volume         *float64 `json:"volume,omitempty"`
	// SysvarName is the CCU system variable a sysvar-mirror output
	// maintains.
	SysvarName string `json:"sysvar_name,omitempty"`
}

// InMode reports whether the output participates in mode (empty Modes
// list = all modes).
func (c OutputConfig) InMode(mode hmenum.AlarmMode) bool {
	if len(c.Modes) == 0 {
		return true
	}
	for _, m := range c.Modes {
		if m == mode {
			return true
		}
	}
	return false
}

// ParseOutputConfig decodes an alarm_outputs.config_json document.
func ParseOutputConfig(raw string) (OutputConfig, error) {
	var cfg OutputConfig
	if raw == "" {
		raw = "{}"
	}
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return OutputConfig{}, fmt.Errorf("outputs: parse output config: %w", err)
	}
	return cfg, nil
}

// acousticDuration returns the bounded acoustic activation length,
// never zero, never above the hard ceiling (S1).
func (c OutputConfig) acousticDuration(engineDefault time.Duration) time.Duration {
	d := time.Duration(c.DurationSeconds) * time.Second
	if d <= 0 {
		d = engineDefault
	}
	if d <= 0 {
		d = 180 * time.Second
	}
	if max := MaxAcousticSeconds * time.Second; d > max {
		d = max
	}
	return d
}

// opticalDuration returns the bounded optical activation length.
func (c OutputConfig) opticalDuration() time.Duration {
	d := time.Duration(c.OpticalSeconds) * time.Second
	if d <= 0 {
		d = DefaultOpticalSeconds * time.Second
	}
	if max := MaxOpticalSeconds * time.Second; d > max {
		d = max
	}
	return d
}
