// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package cover

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

// haNum reads a numeric key out of a flattened body.
//
// Numbers come back as float64 because the body is real JSON — the same shape
// Home Assistant receives. Asserting on a Go `int` would be asserting on how
// the body happened to be built, which is exactly what the typed builders
// changed and what the wire never saw: an int 100 and a float64 100 both
// marshal to `100`.
func haNum(t *testing.T, body map[string]any, key string) float64 {
	t.Helper()
	v, ok := body[key]
	if !ok {
		t.Fatalf("body has no %q", key)
	}
	n, ok := v.(float64)
	if !ok {
		t.Fatalf("body[%q] = %v (%T), want a number", key, v, v)
	}
	return n
}
