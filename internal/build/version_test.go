// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package build

import "testing"

func TestIsAddon_DefaultFalse(t *testing.T) {
	t.Parallel()
	orig := AddonBuild
	t.Cleanup(func() { AddonBuild = orig })
	AddonBuild = "false"
	if IsAddon() {
		t.Error("IsAddon() = true with AddonBuild=\"false\", want false")
	}
}

func TestIsAddon_TrueWhenStamped(t *testing.T) {
	orig := AddonBuild
	t.Cleanup(func() { AddonBuild = orig })
	AddonBuild = "true"
	if !IsAddon() {
		t.Error("IsAddon() = false with AddonBuild=\"true\", want true")
	}
}
