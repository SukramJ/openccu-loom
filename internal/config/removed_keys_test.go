// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package config

import (
	"errors"
	"strings"
	"testing"
)

// TestParseRefusesTheRemovedHistoryBlock pins that a config file still
// setting persistence.history does not load. The decoder would otherwise
// ignore the block and the daemon would start with forwarding off.
func TestParseRefusesTheRemovedHistoryBlock(t *testing.T) {
	t.Parallel()

	_, err := Parse([]byte(`
persistence:
  history:
    enabled: true
    export:
      enabled: true
      endpoint: http://influx:8086
`))
	if !errors.Is(err, ErrRemovedKey) {
		t.Fatalf("Parse error = %v, want ErrRemovedKey", err)
	}
	for _, want := range []string{"persistence.history", "north.measurements"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %s — the operator needs both the old key and its new home", err, want)
		}
	}
}

// TestParseAcceptsPersistenceWithoutHistory is the control: the block the
// daemon still reads, and the new home of the settings, load.
func TestParseAcceptsPersistenceWithoutHistory(t *testing.T) {
	t.Parallel()

	cfg, err := Parse([]byte(`
persistence:
  values_cache:
    enabled: true
north:
  measurements:
    enabled: true
    endpoint: http://influx:8086
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !cfg.North.Measurements.IsEnabled() {
		t.Error("north.measurements.enabled was not read")
	}
}
