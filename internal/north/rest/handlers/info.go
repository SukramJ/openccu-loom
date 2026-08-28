// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

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
const APIVersion = "7.21.0"

// Capability values surfaced through [InfoResponse.Capabilities].
// External clients gate functionality on the presence of these
// tokens rather than on [APIVersion] alone — capabilities can be
// added before a major bump.
//
// A token means the daemon is CONFIGURED for that capability, not that
// the subsystem is working right now. `matter.bridge.v1` is emitted from
// `north.matter.enabled`; it stays emitted while the bridge is starting,
// and it would stay emitted if the bridge crashed. That is deliberate and
// is the question a client is actually asking: may I use this path at all.
// A broker that is briefly unreachable is not a missing capability, and a
// token that came and went with connectivity would make every client
// re-derive its own feature set on every poll.
//
// Liveness is a different question with a different answer: /health, whose
// components report what is running. Do not add a runtime probe to a
// detector here — it would give one field two meanings and break the
// clients that already read this one.
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
	// CapabilityAddonSelfUpdate is surfaced when the CCU add-on
	// self-update platform capability check passed (ADR 0057: an
	// add-on build AND an executable firmware installer). The SPA's
	// add-on-update settings card gates its whole visibility on this
	// token rather than on the shape of a GET /system/addon-update
	// response, since that endpoint always answers 200 regardless of
	// platform support.
	CapabilityAddonSelfUpdate = "addon_self_update"
	// CapabilityMQTTRaw is surfaced when the raw topic plane is enabled.
	// It is independent of mqtt.discovery.v1: the two planes are
	// configured separately and a deployment may run either, both or
	// neither, so a client that wants raw state topics cannot infer their
	// presence from the discovery token.
	CapabilityMQTTRaw = "mqtt.raw.v1"
	// CapabilityWebhookInbound is surfaced when the inbound webhook
	// endpoints (POST /webhook/value, /webhook/program) are mounted.
	// Without the token a caller cannot distinguish "not enabled" from
	// "wrong path" — both answer 404.
	CapabilityWebhookInbound = "webhook.inbound.v1"
	// CapabilityDiagrams is surfaced when the diagram CRUD surface is
	// mounted. The SPA gated its diagram panel on history.v1 as a stand-in
	// because no token existed; that proxy breaks the moment an operator
	// turns recording off while keeping their saved diagrams.
	CapabilityDiagrams = "diagrams.v1"
	// CapabilityAdminPersistence is surfaced when the persistence-backed
	// admin surface is mounted — stored users, tokens, centrals, config
	// sections, preferences, areas. Without a database these routes exist
	// but every write is refused, which a client cannot tell apart from a
	// permission problem.
	//
	// It and CapabilityDiagrams are driven by the same opened database
	// today, so a client will see both or neither. They are separate tokens
	// because they gate separate mounts, and each detector reads the
	// condition of its own: deriving one from the other would make them
	// agree by construction and hide the release where they stop being the
	// same question.
	CapabilityAdminPersistence = "admin.persistence.v1"
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
	AddonBuild bool   `json:"addon_build"`
	Uptime     string `json:"uptime"`
	StartedAt  string `json:"started_at"`
	// ConfigUIURL is the externally-reachable address of this daemon's
	// Config UI, derived from `north.rest.public_url`. Empty when no
	// public URL is configured.
	//
	// It exists because a client's own connection address is not
	// necessarily one a browser can follow: an integration reaching the
	// daemon over a container network, or on a LAN address behind a
	// reverse proxy, knows how to TALK to it but not where to SEND a
	// person. Only the operator knows that, which is what public_url
	// records. A client that wants to link a human at the Config UI
	// reads this and falls back to guessing from its own address.
	//
	// Captured at construction rather than read live: the field is
	// restart-required (restart.go), so the boot value is the one this
	// process actually serves under. A live read would report an address
	// the running daemon is not reachable at yet.
	ConfigUIURL  string   `json:"config_ui_url"`
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
	// HasAddonSelfUpdate reports whether the CCU add-on self-update
	// platform capability check passed (ADR 0057).
	HasAddonSelfUpdate() bool
	// HasMQTTRaw reports whether the raw MQTT topic plane is enabled,
	// independently of HA discovery.
	HasMQTTRaw() bool
	// HasWebhookInbound reports whether the inbound webhook endpoints are
	// mounted.
	HasWebhookInbound() bool
	// HasDiagrams reports whether the diagram CRUD surface is mounted.
	HasDiagrams() bool
	// HasAdminPersistence reports whether the persistence-backed admin
	// surface has a database behind it.
	HasAdminPersistence() bool
}

// Info serves build metadata plus the daemon's wall-clock uptime.
// The startedAt argument is normally the process start time; it is
// captured once at router construction. The optional detector
// surfaces feature capabilities the daemon was started with;
// passing nil emits only the always-on capabilities.
func Info(startedAt time.Time, detector CapabilityDetector, configUIURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		JSON(w, http.StatusOK, InfoResponse{
			Version:      build.Version,
			Commit:       build.Commit,
			BuildDate:    build.BuildDate,
			AddonBuild:   build.IsAddon(),
			Uptime:       time.Since(startedAt).Truncate(time.Second).String(),
			StartedAt:    startedAt.UTC().Format(time.RFC3339),
			ConfigUIURL:  configUIURL,
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
	if d.HasAddonSelfUpdate() {
		out = append(out, CapabilityAddonSelfUpdate)
	}
	if d.HasMQTTRaw() {
		out = append(out, CapabilityMQTTRaw)
	}
	if d.HasWebhookInbound() {
		out = append(out, CapabilityWebhookInbound)
	}
	if d.HasDiagrams() {
		out = append(out, CapabilityDiagrams)
	}
	if d.HasAdminPersistence() {
		out = append(out, CapabilityAdminPersistence)
	}
	return out
}
