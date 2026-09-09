// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package siren

import (
	"encoding/json"
	"testing"

	hadiscovery "github.com/SukramJ/go-hamqtt/discovery"
)

// haBody flattens a typed discovery component into the object Home Assistant
// receives, and returns it in the shape the assertions below were written
// against.
//
// The tests keep asserting on the flat body on purpose: that is what goes on
// the wire, so a typed field that stops reaching it — a wrong `json` tag, a
// zero value swallowed by omitempty — still fails a test. Asserting on the
// struct fields instead would pass in exactly that case.
func haBody(t *testing.T, comp hadiscovery.Component) (platform string, body map[string]any) {
	t.Helper()
	if comp.Platform == "" {
		return "", nil
	}
	raw, err := json.Marshal(comp)
	if err != nil {
		t.Fatalf("marshal component: %v", err)
	}
	out := map[string]any{}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal component: %v", err)
	}
	delete(out, "platform")
	return string(comp.Platform), out
}
