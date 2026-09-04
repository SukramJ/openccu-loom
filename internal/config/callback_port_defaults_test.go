// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package config

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestCallbackPortDefaultsComeFromHmenum pins the daemon's bound callback
// ports to the exported constants rather than to a literal repeated in
// applyDefaults. Before this, pkg/hmenum's two callback-port constants had no
// consumer at all: they read 8120/8129 and so did the config literals, so the
// two could drift without anything noticing which one the listener actually
// binds.
func TestCallbackPortDefaultsComeFromHmenum(t *testing.T) {
	t.Parallel()
	c := Default()
	if c.Callback.Port != hmenum.DefaultXMLRPCCallbackPort {
		t.Errorf("Default().Callback.Port = %d, want hmenum.DefaultXMLRPCCallbackPort (%d)",
			c.Callback.Port, hmenum.DefaultXMLRPCCallbackPort)
	}
	if c.Callback.BinPort != hmenum.DefaultBINRPCCallbackPort {
		t.Errorf("Default().Callback.BinPort = %d, want hmenum.DefaultBINRPCCallbackPort (%d)",
			c.Callback.BinPort, hmenum.DefaultBINRPCCallbackPort)
	}
}
