// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"net/http"
	"time"

	"github.com/SukramJ/openccu-loom/internal/build"
)

// APIVersion is the contract version of the north-bound surface
// (REST + WebSocket envelope + push payloads). Bumped independently
// of the daemon build version when the contract changes in a way
// external clients must reason about — addition of capabilities is
// a minor bump, removal or rename of an existing capability or
// payload field is a major bump.
const APIVersion = "2.53.0"

// Capability values surfaced through [InfoResponse.Capabilities].
// External clients gate functionality on the presence of these
// tokens rather than on [APIVersion] alone — capabilities can be
// added before a major bump.
const (
	CapabilityREST          = "rest.v1"
	CapabilityWSBroadcasts  = "ws.broadcasts.v1"
	CapabilityMQTTDiscovery = "mqtt.discovery.v1"
	CapabilityMatterBridge  = "matter.bridge.v1"
	CapabilityOIDC          = "auth.oidc.v1"
	// CapabilityCCUAuth is surfaced when login delegation to the CCU's
	// own user database is enabled (ADR 0043). The SPA may show a
	// "sign in with your CCU account" hint; the credential shape is
	// unchanged.
	CapabilityCCUAuth        = "auth.ccu.v1"
	CapabilityProblemDetails = "errors.problem_details.v1"
	// CapabilitySupervisedRestart is surfaced when the daemon
	// detects that something (systemd, Docker, k8s) will bring it
	// back up after a clean shutdown. The SPA reads this capability
	// to decide whether the "Restart daemon" button should be
	// active.
	CapabilitySupervisedRestart = "system.restart.supervised.v1"
	// CapabilityMCP is surfaced when the MCP server (ADR 0025) is
	// enabled. CapabilityMCPWrite is surfaced additionally when its
	// write-capable tools are permitted (AllowWrites); a client reads
	// the finer-grained token to decide whether to attempt a write tool.
	CapabilityMCP = "mcp.v1"
	//nolint:gosec // G101 false positive: a capability token, not a credential; see #20
	CapabilityMCPWrite = "mcp.write.v1"
	// CapabilityAlarm is surfaced when the daemon-level alarm service
	// is mounted — the same condition that mounts the /alarm REST
	// routes and the alarm_panel WS commands. Clients gate their alarm
	// surface on this token instead of probing /alarm/panels for a 404
	// (which cannot distinguish a disabled subsystem from an old daemon
	// or a reverse-proxy misroute).
	CapabilityAlarm = "alarm.v1"
	// CapabilityHistory is surfaced when the opt-in measurement-history
	// feature is enabled (the same flag that mounts /history, /energy and
	// /history/recording). The SPA gates its history-dependent surfaces —
	// the Diagrams view (SV03) — on this token so they stay hidden when
	// recording is off.
	CapabilityHistory = "history.v1"
)

// InfoResponse is the body of `GET /api/v1/info`.
type InfoResponse struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"build_date"`
	// AddonBuild reports whether this binary was built as the
	// CCU/RaspberryMatic add-on. Support surfaces (the SPA About
	// page) show it so "where does the daemon run?" is answerable
	// from a screenshot.
	AddonBuild   bool     `json:"addon_build"`
	Uptime       string   `json:"uptime"`
	StartedAt    string   `json:"started_at"`
	APIVersion   string   `json:"api_version"`
	SchemaDigest string   `json:"schema_digest"`
	Capabilities []string `json:"capabilities"`
}

// CapabilityDetector lets callers report the runtime presence of
// features the daemon was started with — MQTT-discovery requires a
// configured broker, Matter requires the bridge to be enabled, OIDC
// requires an issuer URL. The base REST + WS + problem-details
// capabilities are always emitted.
type CapabilityDetector interface {
	HasMQTTDiscovery() bool
	HasMatterBridge() bool
	HasOIDC() bool
	HasCCUAuth() bool
	HasSupervisedRestart() bool
	// HasMCP reports whether the MCP server is enabled; HasMCPWrite
	// whether its write tools are permitted. HasMCPWrite implies HasMCP.
	HasMCP() bool
	HasMCPWrite() bool
	// HasAlarm reports whether the daemon-level alarm service is
	// mounted (the /alarm routes exist).
	HasAlarm() bool
	// HasHistory reports whether the opt-in measurement-history feature
	// is enabled.
	HasHistory() bool
}

// Info serves build metadata plus the daemon's wall-clock uptime.
// The startedAt argument is normally the process start time; it is
// captured once at router construction. The optional detector
// surfaces feature capabilities the daemon was started with;
// passing nil emits only the always-on capabilities.
func Info(startedAt time.Time, detector CapabilityDetector) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		JSON(w, http.StatusOK, InfoResponse{
			Version:      build.Version,
			Commit:       build.Commit,
			BuildDate:    build.BuildDate,
			AddonBuild:   build.IsAddon(),
			Uptime:       time.Since(startedAt).Truncate(time.Second).String(),
			StartedAt:    startedAt.UTC().Format(time.RFC3339),
			APIVersion:   APIVersion,
			SchemaDigest: SchemaDigest,
			Capabilities: capabilities(detector),
		})
	}
}

// capabilities composes the capability list from the always-on set
// plus any conditional capabilities the detector reports as active.
func capabilities(d CapabilityDetector) []string {
	out := []string{
		CapabilityREST,
		CapabilityWSBroadcasts,
		CapabilityProblemDetails,
	}
	if d == nil {
		return out
	}
	if d.HasMQTTDiscovery() {
		out = append(out, CapabilityMQTTDiscovery)
	}
	if d.HasMatterBridge() {
		out = append(out, CapabilityMatterBridge)
	}
	if d.HasOIDC() {
		out = append(out, CapabilityOIDC)
	}
	if d.HasCCUAuth() {
		out = append(out, CapabilityCCUAuth)
	}
	if d.HasSupervisedRestart() {
		out = append(out, CapabilitySupervisedRestart)
	}
	if d.HasMCP() {
		out = append(out, CapabilityMCP)
	}
	if d.HasMCPWrite() {
		out = append(out, CapabilityMCPWrite)
	}
	if d.HasAlarm() {
		out = append(out, CapabilityAlarm)
	}
	if d.HasHistory() {
		out = append(out, CapabilityHistory)
	}
	return out
}
