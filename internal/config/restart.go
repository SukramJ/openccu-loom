// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package config

import "reflect"

// RestartRequiredDiff reports the restart-required config paths whose
// value differs between two configs — typically the running (boot)
// config and the freshly assembled persisted config. A non-empty result
// means a saved change is staged but cannot take effect until the daemon
// restarts; an empty result means nothing is pending (so reverting a
// change clears it). The path set mirrors restartRequiredPaths in the
// REST config handler and the explicit diff in the file-reload watcher;
// all three track SPECIFICATION.md §7.1 Q12.
func RestartRequiredDiff(boot, eff *Config) []string {
	if boot == nil || eff == nil {
		return nil
	}
	out := make([]string, 0, 4)
	add := func(differ bool, path string) {
		if differ {
			out = append(out, path)
		}
	}
	add(boot.DataDir != eff.DataDir, "data_dir")
	add(boot.North.REST.Listen != eff.North.REST.Listen, "north.rest.listen")
	add(boot.North.REST.PublicURL != eff.North.REST.PublicURL, "north.rest.public_url")
	add(boot.North.UI.Listen != eff.North.UI.Listen, "north.ui.listen")
	add(boot.Callback.Host != eff.Callback.Host, "callback.host")
	add(boot.Callback.Port != eff.Callback.Port, "callback.port")
	add(boot.Callback.BinPort != eff.Callback.BinPort, "callback.bin_port")
	add(boot.Callback.PortRange != eff.Callback.PortRange, "callback.port_range")
	add(boot.North.Matter.Enabled != eff.North.Matter.Enabled, "north.matter.enabled")
	add(boot.North.Matter.Listen != eff.North.Matter.Listen, "north.matter.listen")
	add(boot.North.MCP.Enabled != eff.North.MCP.Enabled, "north.mcp.enabled")
	add(boot.North.MCP.AllowWrites != eff.North.MCP.AllowWrites, "north.mcp.allow_writes")
	add(boot.North.MCP.Path != eff.North.MCP.Path, "north.mcp.path")
	add(!reflect.DeepEqual(boot.Centrals, eff.Centrals), "centrals")
	return out
}
