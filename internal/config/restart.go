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
	add(boot.Callback.Host != eff.Callback.Host, "callback.host")
	add(boot.Callback.Port != eff.Callback.Port, "callback.port")
	add(boot.Callback.BinPort != eff.Callback.BinPort, "callback.bin_port")
	add(boot.Callback.PortRange != eff.Callback.PortRange, "callback.port_range")
	add(boot.North.Matter.Enabled != eff.North.Matter.Enabled, "north.matter.enabled")
	add(boot.North.Matter.Listen != eff.North.Matter.Listen, "north.matter.listen")
	add(boot.North.MCP.Enabled != eff.North.MCP.Enabled, "north.mcp.enabled")
	add(boot.North.MCP.AllowWrites != eff.North.MCP.AllowWrites, "north.mcp.allow_writes")
	add(boot.North.MCP.Path != eff.North.MCP.Path, "north.mcp.path")
	// The outbound webhook bridge is wired once at boot, so every webhook
	// field is restart-required (mirrors the MCP block above).
	add(boot.North.Webhook.Enabled != eff.North.Webhook.Enabled, "north.webhook.enabled")
	add(boot.North.Webhook.URL != eff.North.Webhook.URL, "north.webhook.url")
	add(boot.North.Webhook.Secret != eff.North.Webhook.Secret, "north.webhook.secret")
	add(!reflect.DeepEqual(boot.North.Webhook.Events, eff.North.Webhook.Events), "north.webhook.events")
	add(!reflect.DeepEqual(boot.North.Webhook.Centrals, eff.North.Webhook.Centrals), "north.webhook.centrals")
	add(boot.North.Webhook.ParameterGlob != eff.North.Webhook.ParameterGlob, "north.webhook.parameter_glob")
	add(boot.North.Webhook.TimeoutMs != eff.North.Webhook.TimeoutMs, "north.webhook.timeout_ms")
	add(boot.North.Webhook.Inbound.Enabled != eff.North.Webhook.Inbound.Enabled, "north.webhook.inbound.enabled")
	add(boot.North.Webhook.Inbound.Token != eff.North.Webhook.Inbound.Token, "north.webhook.inbound.token")
	// The scheduled-backup job is registered once at boot with the interval +
	// keep-count captured then, so a change takes effect only after a restart.
	add(boot.Backup.Schedule != eff.Backup.Schedule, "backup.schedule")
	add(boot.Backup.KeepLast != eff.Backup.KeepLast, "backup.keep_last")
	add(!reflect.DeepEqual(boot.Centrals, eff.Centrals), "centrals")
	// The login chain (incl. the CCU auth provider) is wired once at
	// boot, so any change to the CCU-auth block is restart-required.
	add(!reflect.DeepEqual(boot.North.REST.Auth.CCU, eff.North.REST.Auth.CCU), "north.rest.auth.ccu")
	// The HA Ingress auth-passthrough middleware is also wired once at boot.
	add(!reflect.DeepEqual(boot.North.REST.Auth.HAIngress, eff.North.REST.Auth.HAIngress), "north.rest.auth.ha_ingress")
	return out
}
