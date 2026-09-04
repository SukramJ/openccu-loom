// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package outputs

import (
	"encoding/json"
	"testing"
)

// A stored output document survives a parse/re-encode round trip with
// every sysvar-mirror key intact. The sensor half of the REST config
// handler already re-encodes a parsed struct before persisting it; a
// key OutputConfig does not declare would be dropped the first time
// the output half does the same.
func TestOutputConfigRoundTripsTheSysvarMirrorKeys(t *testing.T) {
	const raw = `{"sysvar_name":"AlarmZoneEG","sysvar_allow_disarm":true,"sysvar_existing":true}`

	cfg, err := ParseOutputConfig(raw)
	if err != nil {
		t.Fatalf("ParseOutputConfig: %v", err)
	}
	out, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal round trip: %v", err)
	}
	for _, key := range []string{"sysvar_name", "sysvar_allow_disarm", "sysvar_existing"} {
		if _, ok := got[key]; !ok {
			t.Errorf("round trip dropped %q: %s", key, out)
		}
	}
	if got["sysvar_allow_disarm"] != true {
		t.Errorf("sysvar_allow_disarm = %v, want true", got["sysvar_allow_disarm"])
	}
	if got["sysvar_existing"] != true {
		t.Errorf("sysvar_existing = %v, want true", got["sysvar_existing"])
	}
}
