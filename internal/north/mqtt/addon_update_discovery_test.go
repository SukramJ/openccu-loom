// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import "testing"

// TestBuildAddonUpdateDiscovery_NoValueOrInProgressTemplate pins that
// the add-on self-update entity lets Home Assistant parse its state
// topic natively, the same requirement as the CCU firmware-update
// entity (see TestBuildHubUpdateDiscovery_NoValueOrInProgressTemplate):
// `value_template` narrows the payload to a bare version string before
// HA's schema check runs, and `in_progress_template` is not an MQTT
// `update` option at all.
func TestBuildAddonUpdateDiscovery_NoValueOrInProgressTemplate(t *testing.T) {
	t.Parallel()
	db := newHubBuilder()
	item := db.BuildAddonUpdateDiscovery()
	if !item.OK {
		t.Fatal("BuildAddonUpdateDiscovery returned ok=false")
	}
	if item.Component != string(HAComponentUpdate) {
		t.Fatalf("component=%q want update", item.Component)
	}
	m := jsonMap(t, item)
	if _, ok := m["value_template"]; ok {
		t.Error("value_template must not be set: it prevents HA from reading in_progress out of the state JSON")
	}
	if _, ok := m["in_progress_template"]; ok {
		t.Error("in_progress_template is not an HA MQTT update option and is silently dropped")
	}
	if st, _ := m["state_topic"].(string); st == "" {
		t.Error("state_topic must still be set so HA can parse installed_version/latest_version/in_progress natively")
	}
	if lvt, _ := m["latest_version_topic"].(string); lvt == "" {
		t.Error("latest_version_topic must still be set")
	}
}
