// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"strings"
	"testing"
)

// TestBuildOriginInfo_Keys verifies that BuildOriginInfo returns a map with
// the three required HA Discovery origin keys.
func TestBuildOriginInfo_Keys(t *testing.T) {
	t.Parallel()
	got := BuildOriginInfo()
	for _, key := range []string{"name", "sw_version", "support_url"} {
		if _, ok := got[key]; !ok {
			t.Errorf("BuildOriginInfo() missing key %q", key)
		}
	}
}

// TestBuildOriginInfo_NameIsConstant verifies the origin name is stable.
func TestBuildOriginInfo_NameIsConstant(t *testing.T) {
	t.Parallel()
	got := BuildOriginInfo()
	if got["name"] != originName {
		t.Errorf("name = %v, want %q", got["name"], originName)
	}
}

// TestBuildOriginInfo_SupportURL verifies the support_url is well-formed.
func TestBuildOriginInfo_SupportURL(t *testing.T) {
	t.Parallel()
	got := BuildOriginInfo()
	url, ok := got["support_url"].(string)
	if !ok {
		t.Fatalf("support_url is not a string: %T", got["support_url"])
	}
	if !strings.HasPrefix(url, "https://") {
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
	got := BuildOriginInfo()
	if got["sw_version"] != "9.9.9-test" {
		t.Errorf("sw_version = %v, want %q", got["sw_version"], "9.9.9-test")
	}
}
