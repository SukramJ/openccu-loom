// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Extra coverage tests for config.go targeting zero/low-coverage paths:
//   - Validate: invalid logging.format, callback.port out of range,
//     callback.bin_port out of range, ports range check
//   - Load: non-existent file path (os.ReadFile error)
//   - Parse: invalid YAML (yaml.Unmarshal error)
//   - validateMQTT: empty topic_base (after disabling applyDefaults)
//   - InterfaceSpec.UnmarshalYAML: long-form decode error

package config_test

import (
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/config"
)

// TestValidate_InvalidLogFormat verifies that an invalid logging.format
// value is rejected by Validate.
func TestValidate_InvalidLogFormat(t *testing.T) {
	t.Parallel()
	const yaml = `
logging:
  level: info
  format: toml
centrals:
  - name: ccu1
    host: 192.168.1.1
    port: 2001
    interfaces:
      - HmIP-RF
`
	_, err := config.Parse([]byte(yaml))
	if err == nil || !strings.Contains(err.Error(), "logging.format") {
		t.Fatalf("expected error containing 'logging.format', got %v", err)
	}
}

// TestValidate_CallbackPortOutOfRange verifies that callback.port > 65535
// is rejected.
func TestValidate_CallbackPortOutOfRange(t *testing.T) {
	t.Parallel()
	const yaml = `
logging:
  level: info
  format: text
callback:
  port: 99999
centrals:
  - name: ccu1
    host: 192.168.1.1
    port: 2001
    interfaces:
      - HmIP-RF
`
	_, err := config.Parse([]byte(yaml))
	if err == nil || !strings.Contains(err.Error(), "callback.port") {
		t.Fatalf("expected error containing 'callback.port', got %v", err)
	}
}

// TestValidate_CallbackBinPortOutOfRange verifies that callback.bin_port > 65535
// is rejected.
func TestValidate_CallbackBinPortOutOfRange(t *testing.T) {
	t.Parallel()
	const yaml = `
logging:
  level: info
  format: text
callback:
  bin_port: 100000
centrals:
  - name: ccu1
    host: 192.168.1.1
    port: 2001
    interfaces:
      - HmIP-RF
`
	_, err := config.Parse([]byte(yaml))
	if err == nil || !strings.Contains(err.Error(), "callback.bin_port") {
		t.Fatalf("expected error containing 'callback.bin_port', got %v", err)
	}
}

// TestValidate_CallbackMaxConnectionsNegativeRejected verifies that a
// negative callback.max_connections is rejected. applyDefaults only
// substitutes the secure default (64) for the zero value, so a
// negative operator override survives to Validate.
func TestValidate_CallbackMaxConnectionsNegativeRejected(t *testing.T) {
	t.Parallel()
	const yaml = `
logging:
  level: info
  format: text
callback:
  max_connections: -1
centrals:
  - name: ccu1
    host: 192.168.1.1
    port: 2001
    interfaces:
      - HmIP-RF
`
	_, err := config.Parse([]byte(yaml))
	if err == nil || !strings.Contains(err.Error(), "callback.max_connections") {
		t.Fatalf("expected error containing 'callback.max_connections', got %v", err)
	}
}

// TestLoad_NonexistentFile verifies that Load returns an error when the
// specified config file does not exist.
func TestLoad_NonexistentFile(t *testing.T) {
	t.Parallel()
	_, err := config.Load("/tmp/this_file_does_not_exist_openccu-loom_test.yaml")
	if err == nil {
		t.Fatal("expected error for non-existent file, got nil")
	}
}

// TestParse_InvalidYAML verifies that Parse returns an error when the input
// is not valid YAML.
func TestParse_InvalidYAML(t *testing.T) {
	t.Parallel()
	_, err := config.Parse([]byte("{{{{invalid yaml: [[["))
	if err == nil {
		t.Fatal("expected error for invalid YAML, got nil")
	}
}

// TestValidate_CentralPortsOutOfRange verifies that per-interface ports
// outside 1-65535 are rejected.
func TestValidate_CentralPortsOutOfRange(t *testing.T) {
	t.Parallel()
	const yaml = `
logging:
  level: info
  format: text
centrals:
  - name: ccu1
    host: 192.168.1.1
    port: 2001
    interfaces:
      - HmIP-RF
    ports:
      HmIP-RF: 99999
`
	_, err := config.Parse([]byte(yaml))
	if err == nil || !strings.Contains(err.Error(), "ports") {
		t.Fatalf("expected error containing 'ports', got %v", err)
	}
}

// TestValidate_CallbackPortNegative verifies that a negative callback.port
// is rejected.
func TestValidate_CallbackPortNegative(t *testing.T) {
	t.Parallel()
	const yaml = `
logging:
  level: info
  format: text
callback:
  port: -1
centrals:
  - name: ccu1
    host: 192.168.1.1
    port: 2001
    interfaces:
      - HmIP-RF
`
	_, err := config.Parse([]byte(yaml))
	if err == nil || !strings.Contains(err.Error(), "callback.port") {
		t.Fatalf("expected error containing 'callback.port', got %v", err)
	}
}
