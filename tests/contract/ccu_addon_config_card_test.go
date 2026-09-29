// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"strings"
	"testing"
)

// TestCCUAddonConfigCardOpensUIInNewTab pins that the settings landing
// card's "Open Config UI" link leaves the embedding frame. occulited's
// shell shows the card inside a frame and treats any cross-origin
// navigation of that frame as the addon refusing to be embedded (the
// embedding contract in occulited's system-api documentation) — and the
// Config UI on its own port is always another origin. The link must
// therefore open a new tab; without target="_blank" the shell replaces
// the card with its "refuses to be embedded" notice.
func TestCCUAddonConfigCardOpensUIInNewTab(t *testing.T) {
	t.Parallel()

	card := readAddonFile(t, "packaging/ccu-addon/ccu/www/config.cgi")
	for _, needle := range []string{"_blank", "noopener"} {
		if !strings.Contains(card, needle) {
			t.Errorf("config.cgi: the Config-UI link must carry %q so it opens outside the shell's frame", needle)
		}
	}
}
