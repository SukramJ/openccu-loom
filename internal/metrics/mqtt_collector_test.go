// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package metrics

import "testing"

func TestNewMqttCollector(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()
	mc := NewMqttCollector(reg, "ccu1")
	if mc.MessagesSent == nil || mc.DiscoverySent == nil || mc.PublishErrors == nil {
		t.Fatal("MqttCollector fields must not be nil after construction")
	}
	mc.MessagesSent.Inc()
	mc.MessagesSent.Inc()
	mc.DiscoverySent.Inc()
	mc.PublishErrors.Inc()

	metrics := reg.Metrics()
	for _, m := range metrics {
		if m.Name == "mqtt_ccu1_messages_sent" && m.value.Load() != 2 {
			t.Errorf("messages_sent count=%d, want 2", m.value.Load())
		}
	}
}
