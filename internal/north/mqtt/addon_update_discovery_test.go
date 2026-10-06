// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

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
	// The status item carries the update document as its `val` (ADR 0083).
	// The template must hand that document over whole, as JSON, so HA's
	// update platform still parses in_progress natively; a template
	// narrowing it to a scalar (`value_json.val.installed_version`) would
	// silence the in-progress indication again.
	if vt, _ := m["value_template"].(string); vt != updateDocumentTemplate {
		t.Errorf("value_template = %q, want %q — the whole status-object value as JSON", vt, updateDocumentTemplate)
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
