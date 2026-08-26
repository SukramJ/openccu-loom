// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hub

import (
	"testing"
	"time"
)

// TestInstallModeConsumeChangeSincePublish covers the change-detection the
// periodic install-mode refresh depends on to avoid re-publishing an
// identical steady-state tuple every poll: the first call always reports
// changed, a repeated identical call does not, and a genuine transition
// (or an active countdown ticking down) does.
func TestInstallModeConsumeChangeSincePublish(t *testing.T) {
	t.Parallel()
	m := NewInstallMode("ccu-HmIP-RF", nil)

	enabled, remainingS, changed := m.ConsumeChangeSincePublish()
	if enabled || remainingS != 0 || !changed {
		t.Fatalf("first call: enabled=%v remainingS=%d changed=%v, want false/0/true", enabled, remainingS, changed)
	}

	_, _, changed = m.ConsumeChangeSincePublish()
	if changed {
		t.Fatal("second call with unchanged (off, 0) state reported changed=true")
	}

	m.OnState(true, 60*time.Second)
	enabled, remainingS, changed = m.ConsumeChangeSincePublish()
	if !enabled || remainingS <= 0 || !changed {
		t.Fatalf("after enabling: enabled=%v remainingS=%d changed=%v, want true/>0/true", enabled, remainingS, changed)
	}

	m.OnState(false, 0)
	_, _, changed = m.ConsumeChangeSincePublish()
	if !changed {
		t.Fatal("disabling after an active window reported changed=false")
	}
	_, _, changed = m.ConsumeChangeSincePublish()
	if changed {
		t.Fatal("repeated (off, 0) after the disable reported changed=true")
	}
}
