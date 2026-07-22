// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package backends

import "github.com/SukramJ/openccu-loom/pkg/hmenum"

// Kind identifies one of the backend strategies the daemon knows about.
type Kind int

// Kind values. Iota ordering is not wire-significant; strings are.
const (
	KindCCU Kind = iota
	KindCUxD
	KindHomegear
)

// String renders the kind for logs and metrics.
func (k Kind) String() string {
	switch k {
	case KindCCU:
		return "ccu"
	case KindCUxD:
		return "cuxd"
	case KindHomegear:
		return "homegear"
	default:
		return "unknown"
	}
}

// Capabilities describes what a backend can do. Mirrors the
// SPECIFICATION §9.2 capability matrix and
// BackendCapabilities (client/backends/capabilities.py).
type Capabilities struct {
	// RPCCallback is true when the CCU pushes events to us and we host
	// a callback server for this backend.
	RPCCallback bool

	// PingPong is true when the backend implements RPC "ping" / "pong"
	// so the PingPongTracker can observe keepalive health.
	PingPong bool

	// ListDevices is true when the backend can enumerate devices in
	// one RPC call (as opposed to the JSON-RPC / ReGa hybrid path).
	ListDevices bool

	// GetAllPrograms is true when the backend exposes the CCU program catalogue
	// via native RPC.
	GetAllPrograms bool

	// GetAllSysvars is true when the backend exposes the CCU sysvar catalogue
	// via native RPC.
	GetAllSysvars bool

	// FirmwareUpdate is true when the backend accepts firmware-update
	// triggers.
	FirmwareUpdate bool

	// ConfigRestore is true when the backend exposes
	// `restoreConfigToDevice` (re-transmit stored config after a
	// factory reset). CCU only; the caller additionally gates on the
	// interface, since hs485d / VirtualDevices under KindCCU do not
	// expose the method.
	ConfigRestore bool

	// ReplaceDevice is true when the backend exposes
	// `listReplaceableDevices` / `replaceDevice` (guided swap of a
	// paired device for a new one). CCU only; the caller additionally
	// gates on the interface, since HMIPServer (HmIP-*) throws
	// NotImplementedException — only rfd (BidCos-RF) and hs485d
	// (BidCos-Wired) implement it.
	ReplaceDevice bool

	// SearchDevices is true when the backend exposes the wired-bus scan
	// `searchDevices`. CCU only; the caller additionally gates on the
	// interface, since only hs485d (BidCos-Wired) implements it.
	SearchDevices bool

	// CommunicationTest is true when the backend can run the CCU's
	// per-device communication/function test (ReGa DevStartComTest +
	// poll). Requires the ReGa runner, so CCU only; the caller
	// additionally gates on the radio interface.
	CommunicationTest bool

	// RequiresPeriodicRefresh is true for backends that cannot push
	// events and need the periodic-refresh coordinator.
	RequiresPeriodicRefresh bool

	// AlarmMessages is true when the backend can fetch and acknowledge
	// alarm/service messages.
	AlarmMessages bool

	// Backup is true when the backend supports CCU config backup
	// download.
	Backup bool

	// CreateSystemVariable is true when the backend can create new system
	// variables.
	CreateSystemVariable bool

	// DeleteDevice is true when the backend can unpair a device.
	DeleteDevice bool

	// DeleteSystemVariable is true when the backend supports deleting system
	// variables.
	DeleteSystemVariable bool

	// ExecuteProgram is true when the backend can trigger CCU programs.
	ExecuteProgram bool

	// HasSystemUpdate is true when the backend can report and trigger CCU
	// firmware updates. Dynamically set to false when the CCU firmware version
	// is too old.
	HasSystemUpdate bool

	// InboxDevices is true when the backend exposes the pairing inbox.
	InboxDevices bool

	// InstallMode is true when the backend supports pairing mode
	// on/off.
	InstallMode bool

	// InstallModeLocal is true when the backend supports the
	// keyserver-less HmIP LOCAL teach-in (SGTIN + device-key
	// whitelist). Requires the HmIP JSON-RPC surface; the backend
	// additionally gates on the HmIP-RF interface at call time.
	InstallModeLocal bool

	// LinkOperations is true when the backend supports direct-link
	// CRUD.
	LinkOperations bool

	// ServiceMessages is true when the backend can fetch service messages.
	ServiceMessages bool

	// SetProgramState is true when the backend can enable/disable programs.
	SetProgramState bool

	// SetSystemVariable is true when the backend can write system variable
	// values.
	SetSystemVariable bool

	// SuppressServiceMessage is true when the backend can suppress individual
	// service messages.
	SuppressServiceMessage bool

	// ValueListRead is true when the backend supports bulk VALUE-list
	// reads.
	ValueListRead bool

	// VirtualKey is true when the backend supports virtual-key devices.
	VirtualKey bool

	// Functions is true when the backend can enumerate CCU functions
	// (Gewerke).
	Functions bool

	// Rooms is true when the backend can enumerate CCU rooms.
	Rooms bool

	// Metadata is true when the backend supports object metadata
	// (getMetadata / setMetadata) — used by Homegear for device naming.
	Metadata bool

	// Rename is true when the backend supports renaming devices and channels via
	// JSON-RPC.
	Rename bool

	// IseIDLookup is true when the backend can resolve a CCU ISE-ID from a
	// device/channel address.
	IseIDLookup bool
}

// CapabilityFor returns the static capability profile for kind.
// Dynamic capabilities (HasSystemUpdate) are set to the most optimistic
// value here and may be overridden after probing the CCU version via
// [UpdateCapabilitiesForVersion].
func CapabilityFor(kind Kind) Capabilities {
	switch kind {
	case KindCCU:
		// CCU (HmIP-RF, BidCos-RF, BidCos-Wired, …) exposes both XML-RPC and
		// JSON-RPC surfaces.
		return Capabilities{
			RPCCallback:            true,
			PingPong:               true,
			ListDevices:            true,
			GetAllPrograms:         true,
			GetAllSysvars:          true,
			FirmwareUpdate:         true,
			ConfigRestore:          true,
			ReplaceDevice:          true,
			SearchDevices:          true,
			CommunicationTest:      true,
			AlarmMessages:          true,
			Backup:                 true,
			CreateSystemVariable:   true,
			DeleteDevice:           true,
			DeleteSystemVariable:   true,
			ExecuteProgram:         true,
			HasSystemUpdate:        true,
			InboxDevices:           true,
			InstallMode:            true,
			InstallModeLocal:       true,
			LinkOperations:         true,
			ServiceMessages:        true,
			SetProgramState:        true,
			SetSystemVariable:      true,
			SuppressServiceMessage: true,
			ValueListRead:          true,
			VirtualKey:             true,
			Functions:              true,
			Rooms:                  true,
			Rename:                 true,
			IseIDLookup:            true,
			Metadata:               true,
		}
	case KindCUxD:
		// CUxD uses BIN-RPC only; no JSON-RPC surface.
		return Capabilities{
			RPCCallback:    true,
			PingPong:       true,
			ListDevices:    true,
			LinkOperations: true,
		}
	case KindHomegear:
		// Homegear is XML-RPC-only. RPC-Callback is supported; PingPong is
		// not implemented on Homegear's XML-RPC surface. ListDevices works
		// via XML-RPC. There is no JSON-RPC layer — Programs, Rooms,
		// Functions, CCU-Backup, Install-Mode and Firmware-Updates have no
		// counterpart and are reported as ErrUnsupported.
		// System variables and link operations run over Homegear-specific
		// XML-RPC methods (see HomegearBackend).
		// GetAllSysvars is false: Homegear uses XML-RPC getAllSystemVariables,
		// not the CCU JSON-RPC SysVar.getAll endpoint.
		// Metadata is false: Homegear does not support getMetadata /
		// setMetadata / deleteMetadata.
		return Capabilities{
			RPCCallback:          true,
			PingPong:             false,
			ListDevices:          true,
			GetAllSysvars:        false, // Homegear uses XML-RPC, no JSON-RPC SysVar.getAll
			SetSystemVariable:    true,
			DeleteSystemVariable: true,
			DeleteDevice:         true, // deleteDevice(address, flags) via XML-RPC
			LinkOperations:       true, // getLinks/addLink/removeLink/getLinkPeers + LINK paramsets via XML-RPC
			Metadata:             false,
			ValueListRead:        true,
		}
	default:
		return Capabilities{}
	}
}

// UpdateCapabilitiesForVersion adjusts dynamic capability flags based on the
// probed CCU software version string. Some CCU features (system update info)
// were added in specific firmware versions; older firmware lacks these
// methods and the JSON-RPC client would receive an error.
//
// softwareVersion is the version string from
// SystemInformation.SoftwareVersion (e.g. "3.55.10.20210601"). An empty
// string is treated as unknown and no adjustment is applied.
//
// Currently known adjustments: - HasSystemUpdate requires CCU firmware ≥
// 3.49.
func UpdateCapabilitiesForVersion(caps Capabilities, softwareVersion string) Capabilities {
	if softwareVersion == "" {
		return caps
	}
	// Parse the major.minor version prefix. We only need the first two
	// numeric components: "3.49.x.y" → major=3, minor=49.
	major, minor := parseMajorMinor(softwareVersion)
	if major == 0 && minor == 0 {
		return caps
	}
	// System update info requires CCU firmware ≥ 3.49 for the ReGa
	// get_system_update_info script to be available.
	if major < 3 || (major == 3 && minor < 49) {
		caps.HasSystemUpdate = false
	}
	return caps
}

// parseMajorMinor extracts the first two numeric components from a
// dot-separated version string. Returns (0, 0) when parsing fails.
func parseMajorMinor(version string) (major, minor int) {
	i := 0
	for i < len(version) && (version[i] >= '0' && version[i] <= '9') {
		major = major*10 + int(version[i]-'0')
		i++
	}
	if i >= len(version) || version[i] != '.' {
		return 0, 0
	}
	i++
	for i < len(version) && (version[i] >= '0' && version[i] <= '9') {
		minor = minor*10 + int(version[i]-'0')
		i++
	}
	return major, minor
}

// KindFor returns the backend kind the daemon should use for the given
// interface. See SPECIFICATION §8.5 for the strategy — CUxD always
// BIN-RPC, everything else CCU (XML-RPC). The Homegear-Kind ist
// versionsabhängig und wird vom Detector ([DetermineBackendKind])
// gewählt — KindFor wird nur aus der reinen Interface-ID heraus
// aufgerufen.
func KindFor(iface hmenum.Interface) Kind {
	switch iface {
	case hmenum.InterfaceCUxD:
		return KindCUxD
	default:
		return KindCCU
	}
}
