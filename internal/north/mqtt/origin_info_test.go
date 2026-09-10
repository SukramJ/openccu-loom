// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestBuildOriginInfo_Keys verifies that BuildOriginInfo marshals to the three
// HA Discovery origin keys.
//
// Checked on the encoded form rather than the struct fields: Home Assistant's
// abbreviation table accepts `sw`/`url` as well as `sw_version`/`support_url`,
// so the field names alone would not say which spelling reaches the broker.
func TestBuildOriginInfo_Keys(t *testing.T) {
	t.Parallel()
	got := originMap(t)
	for _, key := range []string{"name", "sw_version", "support_url"} {
		if _, ok := got[key]; !ok {
			t.Errorf("BuildOriginInfo() missing key %q", key)
		}
	}
	for _, abbreviated := range []string{"sw", "url"} {
		if _, present := got[abbreviated]; present {
			t.Errorf("BuildOriginInfo() emitted the abbreviation %q", abbreviated)
		}
	}
}

// TestBuildOriginInfo_NameIsConstant verifies the origin name is stable.
func TestBuildOriginInfo_NameIsConstant(t *testing.T) {
	t.Parallel()
	if got := BuildOriginInfo().Name; got != originName {
		t.Errorf("name = %v, want %q", got, originName)
	}
}

// TestBuildOriginInfo_SupportURL verifies the support_url is well-formed.
func TestBuildOriginInfo_SupportURL(t *testing.T) {
	t.Parallel()
	if url := BuildOriginInfo().URL; !strings.HasPrefix(url, "https://") {
		t.Errorf("support_url = %q, want https:// prefix", url)
	}
}

// TestBuildOriginInfo_VersionFollowsSetOriginVersion verifies that calling
// SetOriginVersion is reflected by the next BuildOriginInfo call.
func TestBuildOriginInfo_VersionFollowsSetOriginVersion(t *testing.T) {
	// Not marked Parallel — modifies the shared originVersionStore.
	orig := originVersion()
	t.Cleanup(func() { SetOriginVersion(orig) })

	SetOriginVersion("9.9.9-test")
	if got := BuildOriginInfo().SW; got != "9.9.9-test" {
		t.Errorf("sw_version = %v, want %q", got, "9.9.9-test")
	}
}

func originMap(t *testing.T) map[string]any {
	t.Helper()
	raw, err := json.Marshal(BuildOriginInfo())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out := map[string]any{}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}
