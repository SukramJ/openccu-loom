// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

// Package surface holds the registry of addressable Config-UI surfaces
// and resolves which of them a given configuration serves.
//
// A surface is an entry point the operator can switch off: a navigation
// item, a settings tab, a device-detail tab. The registry is the single
// source for the two consumers that must never disagree — the SPA's
// navigation and the profile editor that explains it.
package surface

import "github.com/SukramJ/openccu-loom/internal/config"

// ID identifies one surface. The prefix names the surface kind:
// "nav." for a navigation item, "settings." for a settings tab,
// "device." for a device-detail tab.
//
// loom:reachable:reason="the identifier type the registry is keyed by and the REST /ui/surfaces handler ranges over; a method-less string alias, which the analyzer's type heuristic (reachable only via its methods) cannot see used"
type ID string

// Group buckets surfaces for the editor and mirrors the navigation
// clusters the SPA already renders.
//
// loom:reachable:reason="carried by Surface.Group into the REST payload the editor buckets by; a method-less string alias the analyzer's type heuristic cannot see used"
type Group string

const (
	// GroupOverview mirrors the SPA's top navigation cluster.
	GroupOverview Group = "overview"
	// GroupAutomation mirrors the automation cluster.
	GroupAutomation Group = "automation"
	// GroupDiagnose mirrors the diagnostics cluster.
	GroupDiagnose Group = "diagnose"
	// GroupBridges mirrors the (Matter-only) bridges cluster.
	GroupBridges Group = "bridges"
	// GroupSystem mirrors the system cluster.
	GroupSystem Group = "system"
	// GroupSettings collects the settings tabs.
	GroupSettings Group = "settings"
	// GroupDevice collects the device-detail tabs.
	GroupDevice Group = "device"
)

// Floor says in which profiles a surface can never be hidden.
//
// loom:reachable:reason="carried by Surface.Floor and read by Surface.IsFloor on every resolve; a method-less string alias the analyzer's type heuristic cannot see used"
type Floor string

const (
	// FloorNone marks a surface the operator may hide anywhere.
	FloorNone Floor = ""
	// FloorAlways marks a surface no profile may hide.
	FloorAlways Floor = "always"
	// FloorStandalone marks a surface only a host-owned profile may
	// hide — in standalone it is the operator's only way to reach
	// something they cannot afford to lose.
	FloorStandalone Floor = "standalone"
)

// Gate names a runtime capability a surface additionally depends on.
// A gated surface stays absent while its feature is off, however the
// profile is configured — making it visible can never conjure a view
// whose feature is switched off.
//
// loom:reachable:reason="carried by Surface.Gate into the REST payload so the editor can name the capability a row waits on; a method-less string alias the analyzer's type heuristic cannot see used"
type Gate string

const (
	// GateNone marks a surface with no capability dependency.
	GateNone Gate = ""
	// GateMatter marks surfaces that need the Matter bridge.
	GateMatter Gate = "matter"
	// GateFeaturePrograms and the other feature gates mark a surface that
	// needs a per-central feature; it is offered while at least one
	// central offers it. On a fleet of openccu-lite systems there are no
	// programs or system variables, and heating groups and backups depend
	// on the token's scopes.
	//
	// The new-devices view (nav.inbox) carries no feature gate: it lists
	// the daemon's own onboarding hold, which exists on every system type,
	// and a CCU's inbox only feeds it (ADR 0082). The published gate
	// vocabulary keeps `feature:hub.inbox` for clients of older daemons.
	GateFeaturePrograms Gate = "feature:hub.programs"
	// GateFeatureSysvars marks the system-variables view.
	GateFeatureSysvars Gate = "feature:hub.sysvars"
	// GateFeatureGroups marks the heating-groups view.
	GateFeatureGroups Gate = "feature:heating_groups.read"
	// GateFeatureBackupCreate marks the backups view.
	GateFeatureBackupCreate Gate = "feature:system.backup.create"
)

// Warn names a runtime condition that makes hiding a surface
// consequential enough to confirm. The condition itself is evaluated by
// the client, which holds the live state; the registry only declares
// that the confirmation exists and which condition it hangs on.
//
// loom:reachable:reason="carried by Surface.Warn into the REST payload so the editor knows which confirmation a hide needs; a method-less string alias the analyzer's type heuristic cannot see used"
type Warn string

const (
	// WarnNone marks a surface that hides without a confirmation.
	WarnNone Warn = ""
	// WarnAlarmArmed fires while the alarm system is armed or arming:
	// with the panel hidden there is no way to disarm from this UI.
	WarnAlarmArmed Warn = "alarm_armed"
	// WarnSecurityFaults fires while faults are unacknowledged, which
	// can only be done on that view.
	WarnSecurityFaults Warn = "security_faults"
	// WarnLastCCUEditor fires in the standalone profile, where hiding
	// CCU administration leaves the config file and the REST API as the
	// only ways to add a CCU.
	WarnLastCCUEditor Warn = "last_ccu_editor"
)

// Surface describes one addressable entry point.
//
// loom:reachable:reason="returned by Registry() and ranged over by the REST /ui/surfaces handler on every request; a data struct whose fields production reads directly, which the analyzer's type heuristic cannot see used"
type Surface struct {
	// ID is the stable identifier stored in a profile.
	ID ID
	// Group buckets the surface in the editor.
	Group Group
	// Defaults holds the shipped visibility per profile name. A profile
	// stores an entry only when it deviates from this.
	Defaults map[string]bool
	// Floor, when set, refuses any override that would hide it.
	Floor Floor
	// Gate names an additional runtime capability dependency.
	Gate Gate
	// Warn names the condition under which hiding asks first.
	Warn Warn
	// WarnProfile, when set, limits Warn to that profile.
	WarnProfile string
	// RoleAdmin marks surfaces the SPA already restricts to admins.
	// Independent of the profile: role gating and surface visibility
	// answer different questions and are ANDed, never merged.
	RoleAdmin bool
	// HAOwns marks surfaces Home Assistant provides itself. Purely
	// informational: the editor renders it as the reason a surface is
	// hidden by default in the embedded profile.
	HAOwns bool
	// BoxOwns marks surfaces the openccu-lite box provides in its own
	// shell. Informational like HAOwns: the reason a surface is hidden by
	// default in the lite profile.
	BoxOwns bool
	// Parent, when set, names the surface this one lives inside. A child
	// is only ever as visible as its parent — hiding the Configure tab
	// has to take its sub-tabs with it, or the resolved map would promise
	// a sub-tab nobody can reach.
	Parent ID
	// Opens names the editor a read-only overview hands off to. The two
	// fleet-wide lists are catalogues: they answer "which devices have a
	// program at all", a question the device detail can only answer one
	// device at a time, and a row then jumps into the device's own editor.
	//
	// Hiding that editor does not hide the catalogue — the listing keeps
	// its diagnostic value without it. It costs the jump: the rows stop
	// linking, because following one would land on a device whose tab is
	// not there.
	//
	// Declared here rather than only in the view, so the coupling is
	// visible where it is configured. An operator who hides the schedule
	// editor should not have to discover in the fleet list that something
	// changed there too.
	Opens ID
	// MultiCentralVisible flips a hidden default back to visible on a
	// daemon serving more than one CCU, in the embedded and in the lite
	// profile.
	//
	// The embedded profile assumes Home Assistant owns CCU administration
	// — but a Home Assistant config entry addresses ONE CCU (the loom
	// backend passes a serial), while this switch is daemon-wide. Bind
	// one of three CCUs into HA and the shipped default would hide the
	// CCU settings for the other two in the only UI that can edit them:
	// HA has no entry for them.
	//
	// The lite profile has the same shape: the daemon onboards the box it
	// runs on by itself, and a second CCU is one the box knows nothing
	// about.
	//
	// So the default follows the fleet. An operator who genuinely wants
	// it hidden still can — this moves the default, not the ceiling.
	MultiCentralVisible bool
}

// visibility is a small constructor for the shipped default row. It
// takes one argument per hosted profile on purpose: a surface cannot be
// registered without a value for every column. The standalone profile
// is the full console, so its column is always visible.
func visibility(embedded, lite bool) map[string]bool {
	return map[string]bool{
		config.ProfileStandalone: true,
		config.ProfileEmbedded:   embedded,
		config.ProfileLite:       lite,
	}
}

// everywhere marks a surface visible in every profile.
func everywhere() map[string]bool { return visibility(true, true) }

// haOwned marks a surface Home Assistant provides and the openccu-lite
// box does not: hidden in the embedded profile only.
func haOwned() map[string]bool { return visibility(false, true) }

// boxOwned marks a surface the openccu-lite box provides and Home
// Assistant does not: hidden in the lite profile only.
func boxOwned() map[string]bool { return visibility(true, false) }

// hostOwned marks a surface either host provides itself: visible only
// standalone.
func hostOwned() map[string]bool { return visibility(false, false) }

// registry is the authoritative table. Order is the order the editor
// renders, which mirrors the SPA navigation.
//
// Every navigation item, settings tab and device-detail tab must appear
// here — TestEverySurfaceIsRegistered fails the build on a view that
// lands without a classification, because an unclassified view is
// either a leaked duplicate or a lost capability the moment the
// embedded profile goes live.
var registry = []Surface{
	// --- navigation: overview -------------------------------------
	{ID: "nav.devices", Group: GroupOverview, Defaults: everywhere(), Floor: FloorAlways},
	{ID: "nav.alarm", Group: GroupOverview, Defaults: everywhere(), Warn: WarnAlarmArmed},
	{ID: "nav.security", Group: GroupOverview, Defaults: everywhere(), Warn: WarnSecurityFaults},
	{ID: "nav.inbox", Group: GroupOverview, Defaults: everywhere()},
	{ID: "nav.fleet", Group: GroupOverview, Defaults: everywhere()},

	// --- navigation: automation -----------------------------------
	{ID: "nav.programs", Group: GroupAutomation, Defaults: everywhere(), Gate: GateFeaturePrograms},
	{ID: "nav.sysvars", Group: GroupAutomation, Defaults: everywhere(), Gate: GateFeatureSysvars},
	{ID: "nav.groups", Group: GroupAutomation, Defaults: everywhere(), Gate: GateFeatureGroups},
	{ID: "nav.links", Group: GroupAutomation, Defaults: everywhere(), Opens: "device.configure.links"},
	{ID: "nav.schedules", Group: GroupAutomation, Defaults: everywhere(), Opens: "device.configure.schedule"},

	// --- navigation: diagnostics ----------------------------------
	{ID: "nav.messages", Group: GroupDiagnose, Defaults: everywhere()},
	{ID: "nav.diagnostics", Group: GroupDiagnose, Defaults: everywhere()},
	{ID: "nav.signal", Group: GroupDiagnose, Defaults: everywhere()},
	{ID: "nav.audit", Group: GroupDiagnose, Defaults: everywhere()},
	{ID: "nav.logs", Group: GroupDiagnose, Defaults: everywhere(), RoleAdmin: true},

	// --- navigation: bridges --------------------------------------
	{ID: "nav.matter", Group: GroupBridges, Defaults: haOwned(), Gate: GateMatter, HAOwns: true},

	// --- navigation: system ---------------------------------------
	// The box delivers and installs device firmware on its Updates page.
	{ID: "nav.firmware", Group: GroupSystem, Defaults: boxOwned(), BoxOwns: true},
	{ID: "nav.backups", Group: GroupSystem, Defaults: everywhere(), RoleAdmin: true, Gate: GateFeatureBackupCreate},
	{ID: "nav.settings", Group: GroupSystem, Defaults: everywhere(), Floor: FloorAlways},
	{ID: "nav.about", Group: GroupSystem, Defaults: everywhere(), Floor: FloorAlways},
	{ID: "nav.licenses", Group: GroupSystem, Defaults: everywhere()},

	// --- settings tabs --------------------------------------------
	{ID: "settings.general", Group: GroupSettings, Defaults: everywhere()},
	{ID: "settings.system", Group: GroupSettings, Defaults: everywhere()},
	{ID: "settings.navviews", Group: GroupSettings, Defaults: everywhere(), Floor: FloorAlways},
	{ID: "settings.changes", Group: GroupSettings, Defaults: everywhere()},
	{ID: "settings.mqtt", Group: GroupSettings, Defaults: everywhere()},
	{ID: "settings.matter", Group: GroupSettings, Defaults: haOwned(), HAOwns: true},
	{ID: "settings.mcp", Group: GroupSettings, Defaults: everywhere()},
	{ID: "settings.measurements", Group: GroupSettings, Defaults: everywhere()},
	{ID: "settings.rest", Group: GroupSettings, Defaults: everywhere()},
	{ID: "settings.discovery", Group: GroupSettings, Defaults: everywhere()},
	{
		// On the box the daemon creates the central for it by itself.
		ID: "settings.ccus", Group: GroupSettings, Defaults: hostOwned(),
		Warn: WarnLastCCUEditor, WarnProfile: config.ProfileStandalone,
		HAOwns: true, BoxOwns: true, MultiCentralVisible: true,
	},
	{ID: "settings.callback", Group: GroupSettings, Defaults: everywhere()},
	// Accounts and sign-in are the box's in the lite profile: the request
	// that selects it was authenticated by a box session, and the box
	// keeps the users and the identity provider behind that session.
	{ID: "settings.oidc", Group: GroupSettings, Defaults: hostOwned(), HAOwns: true, BoxOwns: true},
	{ID: "settings.ccu_auth", Group: GroupSettings, Defaults: hostOwned(), HAOwns: true, BoxOwns: true},
	{
		ID: "settings.users", Group: GroupSettings, Defaults: hostOwned(),
		Floor: FloorStandalone, HAOwns: true, BoxOwns: true, RoleAdmin: true,
	},
	{ID: "settings.groups", Group: GroupSettings, Defaults: haOwned(), HAOwns: true},
	{
		ID: "settings.tokens", Group: GroupSettings, Defaults: haOwned(),
		Floor: FloorStandalone, HAOwns: true, RoleAdmin: true,
	},
	{ID: "settings.visibility", Group: GroupSettings, Defaults: everywhere()},
	{ID: "settings.reliability", Group: GroupSettings, Defaults: everywhere()},
	{ID: "settings.persistence", Group: GroupSettings, Defaults: everywhere()},

	// --- device-detail tabs ---------------------------------------
	{ID: "device.overview", Group: GroupDevice, Defaults: everywhere()},
	// The device editor is visible in every profile. Home Assistant does
	// not provide it for a CCU this daemon serves: the integration
	// registers no configuration panel of its own for such an entry, so
	// this is the only place its users can edit a paramset, a link or a
	// schedule.
	{ID: "device.configure", Group: GroupDevice, Defaults: everywhere()},
	// The device and channel parameters on one page, as the CCU WebUI's
	// "Geräte-/Kanalparameter". A stored override for the former
	// device.configure.device-config sub-tab is reported as Ignored.
	{ID: "device.configure.channels", Group: GroupDevice, Defaults: everywhere(), Parent: "device.configure"},
	{ID: "device.configure.links", Group: GroupDevice, Defaults: everywhere(), Parent: "device.configure"},
	{ID: "device.configure.schedule", Group: GroupDevice, Defaults: everywhere(), Parent: "device.configure"},
	// The change log of the device and the choice of which data points are
	// captured. The log needs no capture, so the tab carries no gate.
	{ID: "device.history", Group: GroupDevice, Defaults: everywhere()},
}

// Registry returns every registered surface in editor order.
func Registry() []Surface {
	out := make([]Surface, len(registry))
	copy(out, registry)
	return out
}

// byID indexes the registry once; the table is immutable after init.
var byID = func() map[ID]Surface {
	m := make(map[ID]Surface, len(registry))
	for _, s := range registry {
		m[s.ID] = s
	}
	return m
}()

// DefaultFor reports the shipped visibility of s in the named profile,
// for a single-CCU daemon.
func (s Surface) DefaultFor(profile string) bool {
	return s.DefaultForFleet(profile, Fleet{})
}

// DefaultForFleet reports the shipped visibility of s in the named
// profile for the given fleet.
//
// An unknown profile reads as visible: the safe direction is to show a
// surface, never to hide one because of a typo.
func (s Surface) DefaultForFleet(profile string, fleet Fleet) bool {
	v, ok := s.Defaults[profile]
	if !ok {
		return true
	}
	if !v && s.MultiCentralVisible && profile != config.ProfileStandalone && fleet.MultiCentral() {
		return true
	}
	return v
}

// IsFloor reports whether s may never be hidden in the named profile.
func (s Surface) IsFloor(profile string) bool {
	switch s.Floor {
	case FloorAlways:
		return true
	case FloorStandalone:
		return profile == config.ProfileStandalone
	case FloorNone:
		return false
	default:
		return false
	}
}
