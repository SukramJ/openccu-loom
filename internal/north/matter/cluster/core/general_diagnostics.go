// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package core

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/pkg/mattercontract"
)

// NetworkInterfaceStruct mirrors Matter §11.12.4.1 (NetworkInterface
// struct) — the per-interface entry the GeneralDiagnostics
// `NetworkInterfaces` (0x0000) attribute exposes. Apple Home's
// `HMMTRAccessoryServerBrowser` reads this list to build its internal
// "topology dictionary"; without at least one entry it logs
// "No enumeration/topology dictionary found" + "Nil supported link
// layer types" and aborts the pair via RemoveFabric ~5 s after
// Subscribe-Initial. matter.js HEAD shape:
// packages/types/src/clusters/general-diagnostics.ts:NetworkInterface.
type NetworkInterfaceStruct struct {
	// Name is a non-empty UTF-8 label (max 32 chars). Matches the OS
	// interface name ("en0", "eth0", …).
	Name string
	// IsOperational is true when the interface is up and carrying
	// traffic.
	IsOperational bool
	// OffPremiseServicesReachableIPv4 / IPv6 are nullable booleans
	// (nil = "unknown" / "no DNS/HTTP probe done"). openccu-loom does
	// not probe off-premise reachability — both stay nil.
	OffPremiseServicesReachableIPv4 *bool
	OffPremiseServicesReachableIPv6 *bool
	// HardwareAddress is the EUI-48 (6-byte) or EUI-64 (8-byte) MAC.
	// Empty for loopback / synthetic interfaces.
	HardwareAddress []byte
	// IPv4Addresses + IPv6Addresses carry the raw 4-byte / 16-byte
	// address bytes for every active address on the interface.
	IPv4Addresses [][]byte
	IPv6Addresses [][]byte
	// InterfaceType is the matter.js InterfaceTypeEnum:
	// 0 Unspecified, 1 WiFi, 2 Ethernet, 3 Cellular, 4 Thread.
	InterfaceType uint8
}

// InterfaceType enum values per Matter §11.12.5.1.
const (
	InterfaceTypeUnspecified uint8 = 0
	InterfaceTypeWiFi        uint8 = 1
	InterfaceTypeEthernet    uint8 = 2
	InterfaceTypeCellular    uint8 = 3
	InterfaceTypeThread      uint8 = 4
)

// GeneralDiagnostics implements the Matter GeneralDiagnostics cluster
// (0x0033) per Matter Core Specification 1.5.1 §11.12. Mandatory on
// the Root endpoint. Reports basic runtime diagnostics: uptime,
// reboot reason, hardware faults, network faults.
//
// openccu-loom emits stub values for the fault lists (the bridge
// itself does not surface hardware-fault events to Matter); UpTime
// and TotalOperationalHours come from a monotonic clock.
type GeneralDiagnostics struct {
	mu sync.RWMutex

	startTime  time.Time
	bootReason uint8

	// Persistence-seeded counters; populated by [SetPersistedCounters].
	// rebootCount survives across daemon restarts (incremented on each
	// fresh boot before being seeded into the cluster).
	// baseOperationalHours is the accumulated TotalOperationalHours
	// from prior process lifetimes; the live attribute adds the
	// current process's uptime to this base.
	rebootCount          uint16
	baseOperationalHours uint32
	persistedSeeded      bool

	// dataVersion tracks the per-cluster monotonic counter per Matter
	// §10.6.5. Bumped at construction and when persisted counters are seeded
	// via [SetPersistedCounters]. Runtime attributes (UpTime,
	// TotalOperationalHours) change continuously but DataVersion is not bumped
	// per-second — controllers that cache UpTime do not need sub-second
	// invalidation. Satisfies [mattercontract.ClusterDataVersion].
	// Mirrors chip's ember dirty-marking in
	// src/app/clusters/general-diagnostics-server/.
	dataVersion cluster.DataVersionTracker

	// Event emitter + endpoint; wired by the bridge topology assembler
	// via [SetMatterEventEmitter] + [SetEndpoint] so [EmitBootReason]
	// can fire the §11.12.8.1 BootReason event.
	endpoint uint16
	emitter  mattercontract.EventEmitter
}

// BootReason values per Matter §11.12.5.4.
const (
	BootReasonUnspecified      uint8 = 0
	BootReasonPowerOnReboot    uint8 = 1
	BootReasonBrownOutReset    uint8 = 2
	BootReasonSoftwareWatchdog uint8 = 3
	BootReasonHardwareWatchdog uint8 = 4
	BootReasonSoftwareUpdate   uint8 = 5
	BootReasonSoftwareReset    uint8 = 6
)

// Cluster ID + revision per Matter §11.12.
const (
	gendiagClusterID       uint32 = 0x0033
	gendiagClusterRevision uint16 = 3 // matter.js HEAD general-diagnostics.element.ts:21 default=3

	gendiagAttrNetworkInterfaces        uint32 = 0x0000
	gendiagAttrRebootCount              uint32 = 0x0001
	gendiagAttrUpTime                   uint32 = 0x0002
	gendiagAttrTotalOperationalHours    uint32 = 0x0003
	gendiagAttrBootReason               uint32 = 0x0004
	gendiagAttrActiveHardwareFaults     uint32 = 0x0005
	gendiagAttrActiveRadioFaults        uint32 = 0x0006
	gendiagAttrActiveNetworkFaults      uint32 = 0x0007
	gendiagAttrTestEventTriggersEnabled uint32 = 0x0008

	// Commands per Matter §11.12.7.
	gendiagCmdTestEventTrigger uint32 = 0x0000
	gendiagCmdTimeSnapshot     uint32 = 0x0001
	gendiagCmdTimeSnapshotResp uint32 = 0x0002

	// Events per Matter §11.12.8 / matter.js general-diagnostics.element.ts:74-79.
	// BootReason is event 0x03; the lower three (HardwareFaultChange,
	// RadioFaultChange, NetworkFaultChange) are optional and not emitted
	// by the bridge.
	gendiagEventBootReason uint32 = 0x0003
)

// NewGeneralDiagnostics returns the cluster with startTime captured
// at construction. bootReason is supplied by the daemon's bootstrap
// (typically [BootReasonPowerOnReboot] for cold start, or
// [BootReasonSoftwareUpdate] after an OTA).
func NewGeneralDiagnostics(bootReason uint8) *GeneralDiagnostics {
	g := &GeneralDiagnostics{
		startTime:  time.Now(),
		bootReason: bootReason,
	}
	// Seed DataVersion at a non-zero sentinel so DataVersionFilter=0 does not
	// produce false-positive cache hits.
	g.dataVersion.Bump()
	return g
}

// UpTimeSeconds returns the seconds elapsed since the cluster was
// constructed (= daemon start). Daemon shutdown hooks read it to
// compute the operational-hours delta to persist back to the store.
func (g *GeneralDiagnostics) UpTimeSeconds() uint64 {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return uint64(time.Since(g.startTime).Seconds())
}

// SetPersistedCounters seeds RebootCount and the
// pre-current-process TotalOperationalHours from external storage —
// the daemon's bootstrap loads the values from a SQLite row before
// the cluster is mounted, then bumps RebootCount and persists the
// updated state. Without seeding, the cluster reports a hardcoded
// RebootCount=1 placeholder and TotalOperationalHours == this
// process's uptime.
//
// Mirrors matter.js packages/node/src/behaviors/general-diagnostics/
// GeneralDiagnosticsServer.ts where bootReason / rebootCount are
// stamped from persistent state at construction. The wiring side is
// the daemon's responsibility; this method makes the cluster
// persistence-aware without forcing the wiring to land in lockstep.
func (g *GeneralDiagnostics) SetPersistedCounters(rebootCount uint16, baseOperationalHours uint32) {
	g.mu.Lock()
	g.rebootCount = rebootCount
	g.baseOperationalHours = baseOperationalHours
	g.persistedSeeded = true
	g.mu.Unlock()
	// Bump DataVersion after counter seed so subscribers that cached the
	// pre-seed values get a version change notification.
	g.dataVersion.Bump()
}

// BootReasonEvent is the payload for the Matter §11.12.8.1 BootReason
// event (id 0x0000, priority Critical). Mirrors matter.js
// packages/model/src/standard/elements/general-diagnostics.element.ts:74-79.
type BootReasonEvent struct {
	// BootReason carries the BootReasonEnum value that caused the
	// current boot (conformance M, field id 0x0).
	BootReason uint8
}

// Compile-time assertions: GeneralDiagnostics satisfies MatterClusterServer,
// the attribute-lister capability, the event-lister capability, the
// event-receiver (emitter wiring) capability, the command-lister capability,
// and MatterClusterDataVersion.
var (
	_ mattercontract.ClusterServer                 = (*GeneralDiagnostics)(nil)
	_ mattercontract.ClusterAttributeLister        = (*GeneralDiagnostics)(nil)
	_ mattercontract.ClusterEventLister            = (*GeneralDiagnostics)(nil)
	_ mattercontract.EventReceiver                 = (*GeneralDiagnostics)(nil)
	_ mattercontract.ClusterCommandLister          = (*GeneralDiagnostics)(nil)
	_ mattercontract.ClusterDataVersion            = (*GeneralDiagnostics)(nil)
	_ mattercontract.ClusterCommandInvokePrivilege = (*GeneralDiagnostics)(nil)
)

// MatterDataVersion implements [mattercontract.ClusterDataVersion].
// Returns the per-cluster monotonic counter seeded at construction.
// Mirrors chip's ember dirty-marking in
// src/app/clusters/general-diagnostics-server/ and matter.js behavior
// layer auto-tracking.
func (g *GeneralDiagnostics) MatterDataVersion() uint32 {
	return g.dataVersion.Current()
}

// MatterClusterID implements [mattercontract.ClusterServer].
func (g *GeneralDiagnostics) MatterClusterID() uint32 { return gendiagClusterID }

// MinInvokePrivilege implements [mattercontract.ClusterCommandInvokePrivilege].
// TestEventTrigger requires Manage (4) per Matter §11.12 (access "M").
// Mirrors matter.js packages/model/src/standard/elements/
// general-diagnostics.element.ts:90.
func (g *GeneralDiagnostics) MinInvokePrivilege(cmdID uint32) uint8 {
	switch cmdID {
	case gendiagCmdTestEventTrigger:
		return 4 // Manage
	default:
		return 3 // Operate — standard default
	}
}

// MatterRead implements [mattercontract.ClusterServer].
func (g *GeneralDiagnostics) MatterRead(attrID uint32) (any, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	switch attrID {
	case gendiagAttrNetworkInterfaces:
		// Apple Home's HMMTRAccessoryServerBrowser reads this list to
		// build its "topology dictionary". An empty list (the prior
		// stub value) makes Apple log "No enumeration/topology dictionary
		// found" + "Nil supported link layer types" and tear the
		// fabric down via RemoveFabric ~5 s after Subscribe-Initial.
		// Enumerate every operational non-loopback interface and emit
		// at least one entry so HAP-ServiceMapper can resolve the link
		// layer type. Mirrors matter.js
		// packages/node/src/behaviors/general-diagnostics/
		// GeneralDiagnosticsServer.ts:networkInterfaces.
		return enumerateNetworkInterfaces(), true
	case gendiagAttrRebootCount:
		// Seeded by the daemon via [SetPersistedCounters]; falls back
		// to 1 (= "first boot") when persistence is not wired.
		if g.persistedSeeded {
			return g.rebootCount, true
		}
		return uint16(1), true
	case gendiagAttrUpTime:
		return uint64(time.Since(g.startTime).Seconds()), true
	case gendiagAttrTotalOperationalHours:
		// Live = persisted base hours + current process uptime hours.
		// Daemon shutdown hooks should snapshot the value back to the
		// store; without that, the base portion stays at zero.
		live := uint32(time.Since(g.startTime).Hours()) //nolint:gosec // hours since startTime fits uint32 for any realistic uptime; see #20
		return g.baseOperationalHours + live, true
	// BootReason / ActiveHardwareFaults / ActiveRadioFaults /
	// ActiveNetworkFaults are OPTIONAL on GeneralDiagnostics. matter.js's
	// `examples/device-bridge-onoff` Sample (Apple-pair-success byte-
	// dump) does NOT emit these four — Apple's MTRDevice-
	// Cache appears to treat the presence of empty fault-list arrays
	// as "unexpected schema" and refuses to persist the cluster.
	// BootReason still surfaces via the §11.12.8.1 BootReason event
	// (id 0x0000) on Subscribe-Initial, which Apple parses into
	// `estimated start time forward to ...`. The attribute itself is
	// not needed when the event flows.
	case gendiagAttrBootReason:
		return nil, false
	case gendiagAttrActiveHardwareFaults:
		return nil, false
	case gendiagAttrActiveRadioFaults:
		return nil, false
	case gendiagAttrActiveNetworkFaults:
		return nil, false
	case gendiagAttrTestEventTriggersEnabled:
		return false, true
	case cluster.AttrGlobalFeatureMap:
		return uint32(0), true
	case cluster.AttrGlobalClusterRevision:
		return gendiagClusterRevision, true
	}
	return nil, false
}

// MatterWrite always rejects — GeneralDiagnostics is read-only.
func (g *GeneralDiagnostics) MatterWrite(_ context.Context, attrID uint32, _ any) error {
	return fmt.Errorf("matter: GeneralDiagnostics is read-only (got attr 0x%04X)", attrID)
}

// MatterInvoke handles GeneralDiagnostics commands per Matter §11.12.7.
//
// Implemented:
//   - 0x01 TimeSnapshot — returns SystemTimeMs (monotonic since boot)
//     and PosixTimeMs (wall clock; null when the bridge is pre-time-sync).
//
// TestEventTrigger (0x00, conformance M) is enumerated but always fails
// enable-key validation with ConstraintError (see the handler); the bridge
// configures no test-event enable key. PayloadTestRequest (0x03, DMTEST) is
// not implemented.
func (g *GeneralDiagnostics) MatterInvoke(_ context.Context, cmdID uint32, _ any) (any, error) {
	switch cmdID {
	case gendiagCmdTimeSnapshot:
		systemMs := uint64(time.Since(g.startTime).Milliseconds()) //nolint:gosec // G115: wall-clock millis are non-negative for any valid host time; see #20
		// Mirrors matter.js packages/node/src/behaviors/general-diagnostics/
		// GeneralDiagnosticsServer.ts::timeSnapshot — PosixTimeMs is
		// nullable per spec; we always have a wall-clock so emit a real
		// value, but the encoder must respect the nullable shape on the
		// wire side.
		posixMs := uint64(time.Now().UnixMilli()) //nolint:gosec // UnixMilli >= 0 for valid wall-clock; see #20
		return TimeSnapshotResponse{
			SystemTimeMs: systemMs,
			PosixTimeMs:  &posixMs,
		}, nil
	case gendiagCmdTestEventTrigger:
		// TestEventTrigger is mandatory (conformance M) but the bridge
		// configures no test-event enable key, so every invocation fails
		// enable-key validation with ConstraintError. Mirrors matter.js
		// GeneralDiagnosticsServer.ts #validateTestEnabledKey (an all-zero
		// or non-matching enable key → Status.ConstraintError,
		// GeneralDiagnosticsServer.ts:99,104). Enumerating the command in
		// AcceptedCommandList satisfies the mandatory-command conformance;
		// it simply never enables a trigger on the bridge.
		return nil, gendiagConstraintErr{}
	}
	return nil, im.UnsupportedCommandf("matter: GeneralDiagnostics command 0x%02X not supported", cmdID)
}

// gendiagConstraintErr is the typed [im.StatusCodeError] returned by
// TestEventTrigger. Maps to IM ConstraintError (0x87), matching matter.js's
// enable-key rejection.
type gendiagConstraintErr struct{}

func (gendiagConstraintErr) Error() string {
	return "matter: GeneralDiagnostics TestEventTrigger: no test-event enable key configured"
}

func (gendiagConstraintErr) MatterStatusCode() im.StatusCode { return im.StatusConstraintError }

var _ im.StatusCodeError = gendiagConstraintErr{}

// TimeSnapshotResponse mirrors Matter §11.12.7.3.
// Mirrors matter.js packages/model/src/standard/elements/
// general-diagnostics.element.ts:99-102.
type TimeSnapshotResponse struct {
	// SystemTimeMs is the bridge's monotonic time since boot,
	// expressed in milliseconds.
	SystemTimeMs uint64
	// PosixTimeMs is wall-clock time (Unix epoch ms), nullable. Set
	// to nil when the bridge has no synchronised wall clock yet.
	PosixTimeMs *uint64
}

// MatterAcceptedCommands implements [mattercontract.ClusterCommandLister].
// Lists the command IDs the server handles via MatterInvoke.
// Mirrors matter.js packages/model/src/standard/elements/
// general-diagnostics.element.ts accepted commands.
//
// TestEventTrigger (0x00) is mandatory (conformance M) and is enumerated;
// the handler always rejects it with ConstraintError (no enable key). Only
// PayloadTestRequest (0x03, DMTEST) stays unlisted.
func (g *GeneralDiagnostics) MatterAcceptedCommands() []uint32 {
	return []uint32{
		gendiagCmdTestEventTrigger, // 0x00
		gendiagCmdTimeSnapshot,     // 0x01
	}
}

// MatterGeneratedCommands implements [mattercontract.ClusterCommandLister].
// Lists the response command IDs this server may emit.
// Mirrors matter.js packages/model/src/standard/elements/
// general-diagnostics.element.ts generated commands.
func (g *GeneralDiagnostics) MatterGeneratedCommands() []uint32 {
	return []uint32{
		gendiagCmdTimeSnapshotResp, // 0x02
	}
}

// MatterReportable returns the subscribe-able attributes.
func (g *GeneralDiagnostics) MatterReportable() []uint32 {
	return []uint32{gendiagAttrUpTime, gendiagAttrBootReason}
}

// MatterAttributes lists every GeneralDiagnostics (0x0033) attribute
// the server implements via MatterRead. Apple Home's HAP service
// rebuild reads the full attribute set; without this the dispatcher
// falls back to MatterReportable's two-attribute surface.
func (g *GeneralDiagnostics) MatterAttributes() []uint32 {
	// BootReason + 3× ActiveFaults are OPTIONAL per Matter Core
	// §11.12.6 and matter.js's bridge sample does not advertise them
	// (verified via Apple-pair-success byte-dump). BootReason
	// surfaces via the §11.12.8.1 BootReason *event* which we emit on
	// Subscribe-Initial via EmitBootReason() — Apple parses that event
	// into the `estimated start time` log line and the BootReason
	// attribute on the wire is redundant.
	return []uint32{
		gendiagAttrNetworkInterfaces,
		gendiagAttrRebootCount,
		gendiagAttrUpTime,
		gendiagAttrTotalOperationalHours,
		gendiagAttrTestEventTriggersEnabled,
	}
}

// MatterEvents implements [mattercontract.ClusterEventLister] so the
// dispatcher synthesises the global EventList (0xFFFA) attribute
// correctly for this cluster.
func (g *GeneralDiagnostics) MatterEvents() []uint32 {
	return []uint32{gendiagEventBootReason}
}

// SetMatterEventEmitter implements [mattercontract.EventReceiver].
// Called by the bridge during topology assembly so [EmitBootReason]
// can fire the §11.12.8.1 BootReason event without the cluster holding
// a direct reference to the bridge. Idempotent — re-wiring during
// topology rebuild replaces the emitter cleanly.
func (g *GeneralDiagnostics) SetMatterEventEmitter(emitter mattercontract.EventEmitter) {
	g.mu.Lock()
	g.emitter = emitter
	g.mu.Unlock()
}

// SetEndpoint stamps the endpoint id this GeneralDiagnostics server is
// mounted on. Matter events carry the (endpoint, cluster, event) triple
// so the commissioner can fan them out to the right subscription path.
// The root endpoint is always 0 in standard topologies, but the bridge
// injects the real value here so the cluster does not hard-code it.
func (g *GeneralDiagnostics) SetEndpoint(endpoint uint16) {
	g.mu.Lock()
	g.endpoint = endpoint
	g.mu.Unlock()
}

// EmitBootReason fires the Matter §11.12.8.1 BootReason event (id
// 0x0000, priority Critical) via the wired [mattercontract.EventEmitter].
// No-op when the emitter has not been wired yet — the daemon calls this
// once at startup after topology assembly. Mirrors matter.js
// packages/node/src/behaviors/general-diagnostics/
// GeneralDiagnosticsServer.ts where the BootReason event is emitted
// on startup with the persisted boot-reason value.
func (g *GeneralDiagnostics) EmitBootReason() {
	g.mu.RLock()
	emitter := g.emitter
	endpoint := g.endpoint
	bootReason := g.bootReason
	g.mu.RUnlock()
	if emitter == nil {
		slog.Default().Warn("matter.general_diagnostics.emit_bootreason_skipped",
			slog.String("reason", "emitter nil — wiring race"))
		return
	}
	slog.Default().Info("matter.general_diagnostics.emit_bootreason",
		slog.Any("endpoint", endpoint), slog.Any("boot_reason", bootReason))
	emitter.MatterEmitEvent(endpoint, gendiagClusterID, gendiagEventBootReason,
		BootReasonEvent{BootReason: bootReason},
		mattercontract.EventPriorityCritical)
}

// enumerateNetworkInterfaces walks net.Interfaces() and projects every
// non-loopback interface that has at least one assigned address into a
// [NetworkInterfaceStruct]. The returned slice is what
// `NetworkInterfaces` reports on the wire — guaranteed non-empty in
// practice (any host that can talk Matter has at least one routable
// interface). Loopback interfaces are excluded so they cannot
// accidentally satisfy the HAP-ServiceMapper's "at least one
// non-loopback layer" check.
//
// Heuristics:
//
//   - InterfaceType is Ethernet for any name starting with "en" or
//     "eth"; WiFi for "wl" prefixes; Thread for "tr"; Unspecified
//     otherwise. The bridge does not probe link technology so a
//     macOS "en0" (which can be either WiFi or Ethernet) defaults to
//     Ethernet — Apple's mapper treats both equivalently for a
//     non-Thread / non-Cellular accessory.
//   - HardwareAddress is canonicalised to 6 or 8 bytes. Synthetic
//     interfaces with empty MACs return an empty slice, which Matter
//     allows.
//   - IPv4 addresses are emitted as the raw 4-byte form. IPv6
//     addresses are emitted as the raw 16-byte form. Link-local
//     (fe80::/10) addresses are kept — Apple's commissioner uses
//     them to route inside the home subnet.
//
// Apple HAP-mapper hard limits — picked empirically from observed
// Apple Home iOS 17+ behaviour. Exceeding either makes Apple log
// "No known schema for decoding attribute value" + HAPErrorDomain
// Code=14 and tear the pair down.
const (
	maxIfacesForApple = 4 // matter.js spec has no overall cap; Apple's mapper does
	maxIPv4PerIface   = 4 // matter.js: list constraint "max 4"
	maxIPv6PerIface   = 8 // matter.js: list constraint "max 8"
)

func enumerateNetworkInterfaces() []NetworkInterfaceStruct {
	ifaces, err := net.Interfaces()
	if err != nil || len(ifaces) == 0 {
		return []NetworkInterfaceStruct{syntheticEthernetIface()}
	}
	out := make([]NetworkInterfaceStruct, 0, len(ifaces))
	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		// matter.js NetworkInterface struct field 4 (HardwareAddress)
		// is `type: "hwadr"` with exact constraint 6 or 8 bytes.
		// Apple's IM-decoder rejects the whole struct (and on cascade
		// the entire Subscribe-Initial) with "No known schema for
		// decoding attribute value" when HardwareAddress is empty or
		// odd length. Synthetic / virtual interfaces (utun, awdl, ...)
		// often have either no MAC or a 0-byte MAC — skip them.
		if len(iface.HardwareAddr) != 6 && len(iface.HardwareAddr) != 8 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil || len(addrs) == 0 {
			continue
		}
		entry := NetworkInterfaceStruct{
			Name:            iface.Name,
			IsOperational:   iface.Flags&net.FlagUp != 0,
			InterfaceType:   classifyInterface(iface.Name),
			HardwareAddress: append([]byte(nil), iface.HardwareAddr...),
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			if v4 := ipnet.IP.To4(); v4 != nil {
				if len(entry.IPv4Addresses) < maxIPv4PerIface {
					entry.IPv4Addresses = append(entry.IPv4Addresses, append([]byte(nil), v4...))
				}
			} else if v6 := ipnet.IP.To16(); v6 != nil {
				if len(entry.IPv6Addresses) < maxIPv6PerIface {
					entry.IPv6Addresses = append(entry.IPv6Addresses, append([]byte(nil), v6...))
				}
			}
		}
		out = append(out, entry)
		if len(out) >= maxIfacesForApple {
			break
		}
	}
	if len(out) == 0 {
		// No real interface qualifies (loopback-only host, no MACs,
		// no addresses). Fall back to one synthetic Ethernet entry —
		// matter.js spec accepts hardware-address fixed at zeros for
		// synthetic accessories, and Apple's HAP mapper just needs ONE
		// entry to build the topology dictionary.
		return []NetworkInterfaceStruct{syntheticEthernetIface()}
	}
	return out
}

// syntheticEthernetIface returns a fallback NetworkInterface struct
// when host enumeration produces nothing valid. All-zero MAC keeps
// the matter.js `hwadr` constraint (exact 6 bytes); zero address
// lists keep the spec's max-4 / max-8 constraints trivially.
func syntheticEthernetIface() NetworkInterfaceStruct {
	return NetworkInterfaceStruct{
		Name:            "eth0",
		IsOperational:   true,
		HardwareAddress: make([]byte, 6),
		InterfaceType:   InterfaceTypeEthernet,
	}
}

// classifyInterface maps an OS interface name to a Matter
// InterfaceType enum. Defaults to Ethernet for the common Linux/macOS
// prefixes (en0, eth0) — Apple's HAP mapper treats Ethernet + WiFi
// identically for non-Thread accessories, so the heuristic miss is
// harmless. A loopback name should never reach this function (the
// caller filters it out), but guard with Unspecified just in case.
func classifyInterface(name string) uint8 {
	n := strings.ToLower(name)
	switch {
	case strings.HasPrefix(n, "lo"):
		return InterfaceTypeUnspecified
	case strings.HasPrefix(n, "wl") || strings.HasPrefix(n, "wlan") || strings.HasPrefix(n, "wifi"):
		return InterfaceTypeWiFi
	case strings.HasPrefix(n, "tr") || strings.HasPrefix(n, "thread"):
		return InterfaceTypeThread
	case strings.HasPrefix(n, "en") || strings.HasPrefix(n, "eth"):
		return InterfaceTypeEthernet
	}
	return InterfaceTypeUnspecified
}
