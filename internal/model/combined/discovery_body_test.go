// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package combined_test

import (
	"encoding/json"
	"testing"

	hadiscovery "github.com/SukramJ/go-hamqtt/discovery"
)

// haBody flattens a projected component into the object Home Assistant
// receives, in the shape these assertions were written against.
//
// They keep asserting on the flat body because that is what goes on the wire:
// a typed field that stops reaching it — a wrong json tag, a zero swallowed by
// omitempty — still fails a test. Asserting on the struct fields would pass in
// exactly that case.
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
