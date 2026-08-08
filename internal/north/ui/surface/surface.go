// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package surface holds the registry of addressable Config-UI surfaces
// and resolves which of them a given configuration serves.
//
// A surface is an entry point the operator can switch off: a navigation
// item, a settings tab, a device-detail tab. The registry is the single
// source for three consumers that must never disagree — the SPA's
// navigation, the profile editor, and the write enforcement that scopes
// the Home Assistant passthrough identity.
package surface

import "github.com/SukramJ/openccu-loom/internal/config"

// ID identifies one surface. The prefix names the surface kind:
// "nav." for a navigation item, "settings." for a settings tab,
// "device." for a device-detail tab.
type ID string

// Group buckets surfaces for the editor and mirrors the navigation
// clusters the SPA already renders.
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
type Floor string

const (
	// FloorNone marks a surface the operator may hide anywhere.
	FloorNone Floor = ""
	// FloorAlways marks a surface no profile may hide.
	FloorAlways Floor = "always"
	// FloorStandalone marks a surface only the embedded profile may
	// hide — in standalone it is the operator's only way to reach
	// something they cannot afford to lose.
	FloorStandalone Floor = "standalone"
)

// Gate names a runtime capability a surface additionally depends on.
// A gated surface stays absent while its feature is off, however the
// profile is configured — making it visible can never conjure a view
// whose feature is switched off.
type Gate string

const (
	// GateNone marks a surface with no capability dependency.
	GateNone Gate = ""
	// GateMatter marks surfaces that need the Matter bridge.
	GateMatter Gate = "matter"
	// GateHistory marks surfaces that need measurement-history recording.
	GateHistory Gate = "history"
)

// Warn names a runtime condition that makes hiding a surface
// consequential enough to confirm. The condition itself is evaluated by
// the client, which holds the live state; the registry only declares
// that the confirmation exists and which condition it hangs on.
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
	// WriteGated marks a surface whose entry additionally decides
	// whether the Ingress passthrough identity may write there — in the
	// embedded profile only. See enforce.go for the route mapping.
	WriteGated bool
	// HAOwns marks surfaces Home Assistant provides itself. Purely
	// informational: the editor renders it as the reason a surface is
	// hidden by default in the embedded profile.
	HAOwns bool
	// Parent, when set, names the surface this one lives inside. A child
	// is only ever as visible as its parent — hiding the Configure tab
	// has to take its sub-tabs with it, or the write enforcement would
	// keep honouring a sub-tab nobody can reach.
	Parent ID
}

// visibility is a small constructor for the shipped default pair.
func visibility(standalone, embedded bool) map[string]bool {
	return map[string]bool{
		config.ProfileStandalone: standalone,
		config.ProfileEmbedded:   embedded,
	}
}

// both marks a surface visible in either profile.
func both() map[string]bool { return visibility(true, true) }

// haOwned marks a surface visible standalone and hidden when Home
// Assistant owns the config surface.
func haOwned() map[string]bool { return visibility(true, false) }

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
	{ID: "nav.overview", Group: GroupOverview, Defaults: haOwned(), HAOwns: true},
	{ID: "nav.devices", Group: GroupOverview, Defaults: both(), Floor: FloorAlways},
	{ID: "nav.favorites", Group: GroupOverview, Defaults: haOwned(), HAOwns: true},
	{ID: "nav.alarm", Group: GroupOverview, Defaults: both(), Warn: WarnAlarmArmed},
	{ID: "nav.security", Group: GroupOverview, Defaults: both(), Warn: WarnSecurityFaults},
	{ID: "nav.inbox", Group: GroupOverview, Defaults: both()},
	{ID: "nav.fleet", Group: GroupOverview, Defaults: both()},

	// --- navigation: automation -----------------------------------
	{ID: "nav.programs", Group: GroupAutomation, Defaults: both()},
	{ID: "nav.sysvars", Group: GroupAutomation, Defaults: both()},
	{ID: "nav.groups", Group: GroupAutomation, Defaults: both()},
	{ID: "nav.links", Group: GroupAutomation, Defaults: both()},

	// --- navigation: diagnostics ----------------------------------
	{ID: "nav.messages", Group: GroupDiagnose, Defaults: both()},
	{ID: "nav.diagnostics", Group: GroupDiagnose, Defaults: both()},
	{ID: "nav.energy", Group: GroupDiagnose, Defaults: haOwned(), Gate: GateHistory, HAOwns: true},
	{ID: "nav.diagrams", Group: GroupDiagnose, Defaults: haOwned(), Gate: GateHistory, HAOwns: true},
	{ID: "nav.signal", Group: GroupDiagnose, Defaults: both()},
	{ID: "nav.audit", Group: GroupDiagnose, Defaults: both()},
	{ID: "nav.logs", Group: GroupDiagnose, Defaults: both(), RoleAdmin: true},

	// --- navigation: bridges --------------------------------------
	{ID: "nav.matter", Group: GroupBridges, Defaults: haOwned(), Gate: GateMatter, WriteGated: true, HAOwns: true},

	// --- navigation: system ---------------------------------------
	{ID: "nav.firmware", Group: GroupSystem, Defaults: both()},
	{ID: "nav.backups", Group: GroupSystem, Defaults: both(), RoleAdmin: true},
	{ID: "nav.settings", Group: GroupSystem, Defaults: both(), Floor: FloorAlways},
	{ID: "nav.about", Group: GroupSystem, Defaults: both(), Floor: FloorAlways},

	// --- settings tabs --------------------------------------------
	{ID: "settings.general", Group: GroupSettings, Defaults: both()},
	{ID: "settings.system", Group: GroupSettings, Defaults: both()},
	{ID: "settings.navviews", Group: GroupSettings, Defaults: both(), Floor: FloorAlways},
	{ID: "settings.changes", Group: GroupSettings, Defaults: both()},
	{ID: "settings.mqtt", Group: GroupSettings, Defaults: both()},
	{ID: "settings.matter", Group: GroupSettings, Defaults: haOwned(), WriteGated: true, HAOwns: true},
	{ID: "settings.mcp", Group: GroupSettings, Defaults: both()},
	{ID: "settings.rest", Group: GroupSettings, Defaults: both()},
	{ID: "settings.discovery", Group: GroupSettings, Defaults: both()},
	{
		ID: "settings.ccus", Group: GroupSettings, Defaults: haOwned(),
		Warn: WarnLastCCUEditor, WarnProfile: config.ProfileStandalone,
		WriteGated: true, HAOwns: true,
	},
	{ID: "settings.callback", Group: GroupSettings, Defaults: both()},
	{ID: "settings.oidc", Group: GroupSettings, Defaults: haOwned(), WriteGated: true, HAOwns: true},
	{ID: "settings.ccu_auth", Group: GroupSettings, Defaults: haOwned(), WriteGated: true, HAOwns: true},
	{
		ID: "settings.users", Group: GroupSettings, Defaults: haOwned(),
		Floor: FloorStandalone, WriteGated: true, HAOwns: true, RoleAdmin: true,
	},
	{ID: "settings.groups", Group: GroupSettings, Defaults: haOwned(), WriteGated: true, HAOwns: true},
	{
		ID: "settings.tokens", Group: GroupSettings, Defaults: haOwned(),
		Floor: FloorStandalone, WriteGated: true, HAOwns: true, RoleAdmin: true,
	},
	{ID: "settings.visibility", Group: GroupSettings, Defaults: both()},
	{ID: "settings.reliability", Group: GroupSettings, Defaults: both()},
	{ID: "settings.persistence", Group: GroupSettings, Defaults: both()},

	// --- device-detail tabs ---------------------------------------
	{ID: "device.overview", Group: GroupDevice, Defaults: both()},
	{ID: "device.configure", Group: GroupDevice, Defaults: haOwned(), WriteGated: true, HAOwns: true},
	{
		ID: "device.configure.device-config", Group: GroupDevice, Defaults: haOwned(),
		Parent: "device.configure", WriteGated: true, HAOwns: true,
	},
	// The channel strip is a selector, not an editor: every write it
	// leads to belongs to the device-config sub-tab, so it carries no
	// route set of its own and is not write-gated.
	{
		ID: "device.configure.channels", Group: GroupDevice, Defaults: haOwned(),
		Parent: "device.configure", HAOwns: true,
	},
	{
		ID: "device.configure.links", Group: GroupDevice, Defaults: haOwned(),
		Parent: "device.configure", WriteGated: true, HAOwns: true,
	},
	{
		ID: "device.configure.schedule", Group: GroupDevice, Defaults: haOwned(),
		Parent: "device.configure", WriteGated: true, HAOwns: true,
	},
	{ID: "device.history", Group: GroupDevice, Defaults: both(), Gate: GateHistory},
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

// Lookup returns the surface with the given id.
func Lookup(id ID) (Surface, bool) {
	s, ok := byID[id]
	return s, ok
}

// DefaultFor reports the shipped visibility of s in the named profile.
// An unknown profile reads as visible: the safe direction is to show a
// surface, never to hide one because of a typo.
func (s Surface) DefaultFor(profile string) bool {
	v, ok := s.Defaults[profile]
	if !ok {
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
