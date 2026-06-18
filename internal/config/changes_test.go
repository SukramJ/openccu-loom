// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package config

import (
	"slices"
	"testing"
)

func TestChangedFields(t *testing.T) {
	t.Parallel()

	t.Run("identical", func(t *testing.T) {
		t.Parallel()
		got := ChangedFields(&Config{}, &Config{})
		if len(got) != 0 {
			t.Errorf("expected no changed fields for two identical zero-value configs, got %v", got)
		}
	})

	t.Run("single_field_mqtt_broker_url", func(t *testing.T) {
		t.Parallel()
		boot := &Config{}
		eff := &Config{}
		eff.North.MQTT.BrokerURL = "mqtt://host:1883"
		got := ChangedFields(boot, eff)
		if len(got) != 1 {
			t.Fatalf("expected 1 changed field, got %d: %v", len(got), got)
		}
		if !slices.Contains(got, "north.mqtt.broker_url") {
			t.Errorf("expected path %q in result, got %v", "north.mqtt.broker_url", got)
		}
	})

	t.Run("multiple_fields", func(t *testing.T) {
		t.Parallel()
		boot := &Config{}
		eff := &Config{}
		eff.North.MQTT.BrokerURL = "mqtt://host:1883"
		eff.Locale = "de"
		got := ChangedFields(boot, eff)
		if len(got) != 2 {
			t.Fatalf("expected 2 changed fields, got %d: %v", len(got), got)
		}
		if !slices.Contains(got, "north.mqtt.broker_url") {
			t.Errorf("expected path %q in result, got %v", "north.mqtt.broker_url", got)
		}
		if !slices.Contains(got, "locale") {
			t.Errorf("expected path %q in result, got %v", "locale", got)
		}
	})

	t.Run("reverted_to_boot", func(t *testing.T) {
		t.Parallel()
		boot := &Config{}
		boot.North.MQTT.BrokerURL = "mqtt://host:1883"
		eff := &Config{}
		eff.North.MQTT.BrokerURL = "mqtt://host:1883"
		got := ChangedFields(boot, eff)
		if len(got) != 0 {
			t.Errorf("expected no changed fields when eff matches boot, got %v", got)
		}
	})

	t.Run("nil_boot", func(t *testing.T) {
		t.Parallel()
		got := ChangedFields(nil, &Config{})
		if len(got) != 0 {
			t.Errorf("expected nil/empty result for nil boot, got %v", got)
		}
	})

	t.Run("nil_eff", func(t *testing.T) {
		t.Parallel()
		got := ChangedFields(&Config{}, nil)
		if len(got) != 0 {
			t.Errorf("expected nil/empty result for nil eff, got %v", got)
		}
	})
}
