// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"

	"github.com/SukramJ/openccu-loom/internal/north/matter/bootid"
	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// BasicInformation implements the Matter BasicInformation cluster
// (0x0028) per Matter Core Specification 1.5.1 §11.1. Mandatory on
// the Root endpoint (0). Identifies the bridge to commissioners:
// vendor / product / hardware / firmware metadata, plus user-writable
// NodeLabel and Location.
//
// Two new mandatory attributes since the Matter 1.3 baseline:
//
//   - SpecificationVersion (1.0+ but value bumped per spec release) —
//     advertised as 0x01050100 (Matter 1.5.1.0).
//   - ConfigurationVersion (1.5 mandatory) — opaque uint32 the
//     manufacturer increments when the meta-state of the device
//     changes meaningfully (e.g. firmware update changes endpoint
//     topology).
type BasicInformation struct {
	mu sync.RWMutex

	dataModelRevision uint16
	vendorName        string
	vendorID          uint16
	productName       string
	productID         uint16
	productLabel      string
	productURL        string
	productAppearance ProductAppearanceStruct
	hardwareVersion   uint16
	hardwareString    string
	softwareVersion   uint32
	softwareString    string
	manufacturingDate string
	partNumber        string
	serialNumber      string

	// Mutable.
	nodeLabel string
	location  string
	// identityLabel is the NodeLabel the cluster was constructed with.
	// It feeds [BasicInformation.uniqueID] instead of the live
	// nodeLabel: UniqueID carries quality F (fixed for the lifetime of
	// the device, Matter §11.1.5.13), and SerialNumber falls back to a
	// UniqueID slice — deriving either from a value a controller can
	// write means renaming the bridge in Apple or Google Home silently
	// changes the node's identity under the controller's cache.
	identityLabel string
	// onPersistentWrite, when non-nil, fires after every successful
	// Matter write to a non-volatile attribute (NodeLabel / Location)
	// so the daemon can persist the value across restarts. See
	// [BasicInformation.SetOnPersistentWrite].
	onPersistentWrite func(nodeLabel, location string)

	// Capability metadata.
	capabilityMinima     CapabilityMinimaStruct
	configurationVersion uint32
	maxPathsPerInvoke    uint16

	// Event emitter + endpoint; wired by the bridge topology assembler
	// via [SetMatterEventEmitter] + [SetEndpoint] so [EmitStartUp],
	// [EmitShutDown], and [EmitLeave] can fire their respective events.
	endpoint uint16
	emitter  interfaces.MatterEventEmitter

	// dataVersion tracks the per-cluster monotonic counter (Matter
	// §10.6.5). Bumped on every successful NodeLabel / Location /
	// LocalConfigDisabled write so subscribers see the change. Without
	// the bump, controllers that cache via DataVersionFilter never
	// re-read post-write.
	dataVersion cluster.DataVersionTracker
}

// ProductAppearanceStruct mirrors Matter §11.1.5.20 (added in 1.4 as
// mandatory). Finish + PrimaryColor describe the physical look of
// the bridge for commissioner UIs.
//
// PrimaryColor uses Quality "X" (nullable). The zero value (0) encodes
// the "Black" enum variant. PrimaryColorAbsent (0xFF) is the sentinel
// that the wire encoder translates to a TLV-Null element; all other
// values encode as uint8. Valid enum range per spec is 0–19.
type ProductAppearanceStruct struct {
	Finish       uint8 // ProductFinishEnum: 0=Other, 1=Matte, 2=Satin, 3=Polished, 4=Rugged, 5=Fabric
	PrimaryColor uint8 // ColorEnum 0–19 or PrimaryColorAbsent (0xFF) for null
}

// PrimaryColorAbsent is the sentinel that signals the PrimaryColor field
// should be encoded as TLV-Null (Quality "X" per Matter §11.1.5.20.2).
// Values 0–19 are valid color enum entries.
const PrimaryColorAbsent uint8 = 0xFF

// CapabilityMinimaStruct mirrors Matter §11.1.5.18.
type CapabilityMinimaStruct struct {
	CaseSessionsPerFabric  uint16
	SubscriptionsPerFabric uint16
}

// Cluster ID + revision per Matter §11.1.
const (
	basicInfoClusterID       uint32 = 0x0028
	basicInfoClusterRevision uint16 = 6 // matter.js HEAD basic-information.element.ts:20 default=6

	basicInfoAttrDataModelRevision    uint32 = 0x0000
	basicInfoAttrVendorName           uint32 = 0x0001
	basicInfoAttrVendorID             uint32 = 0x0002
	basicInfoAttrProductName          uint32 = 0x0003
	basicInfoAttrProductID            uint32 = 0x0004
	basicInfoAttrNodeLabel            uint32 = 0x0005
	basicInfoAttrLocation             uint32 = 0x0006
	basicInfoAttrHardwareVersion      uint32 = 0x0007
	basicInfoAttrHardwareVersionStr   uint32 = 0x0008
	basicInfoAttrSoftwareVersion      uint32 = 0x0009
	basicInfoAttrSoftwareVersionStr   uint32 = 0x000A
	basicInfoAttrManufacturingDate    uint32 = 0x000B
	basicInfoAttrPartNumber           uint32 = 0x000C
	basicInfoAttrProductURL           uint32 = 0x000D
	basicInfoAttrProductLabel         uint32 = 0x000E
	basicInfoAttrSerialNumber         uint32 = 0x000F
	basicInfoAttrLocalConfigDisabled  uint32 = 0x0010
	basicInfoAttrReachable            uint32 = 0x0011
	basicInfoAttrUniqueID             uint32 = 0x0012
	basicInfoAttrCapabilityMinima     uint32 = 0x0013
	basicInfoAttrProductAppearance    uint32 = 0x0014
	basicInfoAttrSpecificationVersion uint32 = 0x0015
	basicInfoAttrMaxPathsPerInvoke    uint32 = 0x0016
	basicInfoAttrConfigurationVersion uint32 = 0x0018 // Matter 1.5
)

// Events per Matter §11.1.8.
const (
	basicInfoEventStartUp          uint32 = 0x0000
	basicInfoEventShutDown         uint32 = 0x0001
	basicInfoEventLeave            uint32 = 0x0002
	basicInfoEventReachableChanged uint32 = 0x0003
)

// errBasicInfoUnknown is returned when a write hits an unwritable or
// unknown attribute.
var errBasicInfoUnknown = errors.New("matter: BasicInformation unknown / read-only attribute")

// Config carries the bridge's static identity. The fields map 1:1 to
// the Matter BasicInformation attributes; non-empty values pass
// through to the cluster as-is.
type Config struct {
	DataModelRevision    uint16 // Matter §11.1.5.1 — 1.5.1 = 19 (matter.js HEAD Specification.ts:67)
	VendorName           string
	VendorID             uint16
	ProductName          string
	ProductID            uint16
	ProductLabel         string
	ProductURL           string
	ProductAppearance    ProductAppearanceStruct
	HardwareVersion      uint16
	HardwareVersionStr   string
	SoftwareVersion      uint32
	SoftwareVersionStr   string
	ManufacturingDate    string // ISO 8601 YYYYMMDD
	PartNumber           string
	SerialNumber         string
	NodeLabel            string // user-writable; supplied default
	Location             string // user-writable; "XX" for unset per spec
	CapabilityMinima     CapabilityMinimaStruct
	ConfigurationVersion uint32
	MaxPathsPerInvoke    uint16
}

// NewBasicInformation constructs the cluster from cfg. Returns an
// error when mandatory fields (VendorID, ProductID, NodeLabel) are
// missing or zero-valued.
func NewBasicInformation(cfg Config) (*BasicInformation, error) {
	// VendorID must be a valid device identity: non-zero and within the
	// CSA-assignable range. Mirrors matter.js
	// packages/node/src/behaviors/basic-information/basic-information-validators.ts:32
	// (`vendorId === 0 || vendorId > 0xfff4` → ImplementationError); 0xFFF5-0xFFFF
	// are reserved test/anchor IDs that are not valid product identities.
	if cfg.VendorID == 0 || cfg.VendorID > 0xFFF4 {
		return nil, fmt.Errorf("matter: BasicInformation Config.VendorID 0x%04X is not a valid device identity; it must be in 0x0001-0xFFF4", cfg.VendorID)
	}
	if cfg.ProductID == 0 {
		return nil, errors.New("matter: BasicInformation Config.ProductID must be non-zero")
	}
	if cfg.NodeLabel == "" {
		return nil, errors.New("matter: BasicInformation Config.NodeLabel must be non-empty")
	}
	loc := cfg.Location
	if loc == "" {
		loc = "XX"
	}
	dmr := cfg.DataModelRevision
	if dmr == 0 {
		// matter.js HEAD `packages/model/src/common/Specification.ts`:
		// `DATA_MODEL_REVISION = 19`. The previous baseline 18 stamped
		// the bridge with Matter 1.5.0 data-model revision while
		// `SpecificationVersion` already advertised 1.5.1 — a
		// pre-publication Apple Home build flagged the mismatch as
		// `BasicInformation.DataModelRevision != SpecificationVersion's
		// implied DataModelRevision` in some HAP-service mapper paths.
		// Keep the constant in lock-step with matter.js HEAD.
		dmr = 19
	}
	maxPaths := cfg.MaxPathsPerInvoke
	if maxPaths == 0 {
		// matter.js HEAD `packages/types/src/protocol/definitions/
		// interaction.ts:13` `DEFAULT_MAX_PATHS_PER_INVOKE = 10`.
		// Apple Home and chip-tool never batch invokes in practice, so
		// the previous default of 1 had no live symptom; matter.js
		// parity makes the bridge robust against a future commissioner
		// that DOES batch (Matter Spec §11.1.5.20 allows 1..65535).
		maxPaths = 10
	}
	// ConfigurationVersion is optional on Root BasicInformation and
	// matter.js Sample omits it. Carry zero (= "not configured") all
	// the way through — MatterRead skips the attribute on the wire
	// when the value stays zero, matching matter.js parity.
	cfgVer := cfg.ConfigurationVersion
	// matter.js basic-information.element.ts: HardwareVersionString +
	// SoftwareVersionString carry `constraint: "1 to 64"` — empty
	// strings violate the spec's lower bound. Apple Home's HAP service
	// mapper enforces the constraint silently and aborts pair after
	// Subscribe-Initial. Fall back to printable defaults derived from
	// the numeric versions so the wire output always honours `min 1`.
	hwStr := cfg.HardwareVersionStr
	if hwStr == "" {
		hwStr = fmt.Sprintf("%d.0", cfg.HardwareVersion)
		if cfg.HardwareVersion == 0 {
			hwStr = "1.0"
		}
	}
	// SoftwareVersion (0x0009) and SoftwareVersionString (0x000A) must
	// describe the same release. matter.js holds that invariant by
	// deriving the string from the numeric value —
	// BasicInformationServer.ts:71
	// `setDefault("softwareVersionString", state.softwareVersion.toString())`
	// — so the pair can never diverge. This bridge's authoritative
	// version is the human-readable build string, so when the caller
	// supplies only the string the derivation runs in the opposite
	// direction via [SoftwareVersionFromString]. A divergent pair (a
	// hard-coded numeric 1 next to string "0.32.1") crashes at least one
	// ecosystem hub (Aqara) during bridge synchronisation.
	swVersion := cfg.SoftwareVersion
	swStr := cfg.SoftwareVersionStr
	if swVersion == 0 && swStr != "" {
		swVersion = SoftwareVersionFromString(swStr)
	}
	if swStr == "" {
		// Mirrors matter.js BasicInformationServer.ts:71 — the string
		// default is the decimal rendering of the numeric value. Even 0
		// renders as one byte, so the SoftwareVersionString constraint
		// "1 to 64" (basic-information.element.ts) always holds.
		swStr = strconv.FormatUint(uint64(swVersion), 10)
	}
	bi := &BasicInformation{
		dataModelRevision:    dmr,
		vendorName:           cfg.VendorName,
		vendorID:             cfg.VendorID,
		productName:          cfg.ProductName,
		productID:            cfg.ProductID,
		productLabel:         cfg.ProductLabel,
		productURL:           cfg.ProductURL,
		productAppearance:    cfg.ProductAppearance,
		hardwareVersion:      cfg.HardwareVersion,
		hardwareString:       hwStr,
		softwareVersion:      swVersion,
		softwareString:       swStr,
		manufacturingDate:    cfg.ManufacturingDate,
		partNumber:           cfg.PartNumber,
		serialNumber:         cfg.SerialNumber,
		nodeLabel:            cfg.NodeLabel,
		identityLabel:        cfg.NodeLabel,
		location:             loc,
		capabilityMinima:     defaultCapabilityMinima(cfg.CapabilityMinima),
		configurationVersion: cfgVer,
		maxPathsPerInvoke:    maxPaths,
	}
	validateBasicInfoAttributes(cfg)
	return bi, nil
}

// SoftwareVersionFromString derives the numeric SoftwareVersion
// attribute (Matter §11.1.5.10, uint32) from a human-readable
// semver-style version string so both version attributes advertise the
// same release.
//
// matter.js has no string-to-numeric path — its default flow derives
// the string FROM the numeric value (matter.js packages/node/src/
// behaviors/basic-information/BasicInformationServer.ts:71
// `setDefault("softwareVersionString", state.softwareVersion.toString())`)
// so the pair can never diverge. This bridge's authoritative version is
// the build string, so the same "one release, two renderings" invariant
// is held by deriving in the opposite direction with a stable,
// monotonic encoding:
//
//	numeric = major*1_000_000 + minor*1_000 + patch
//
// with each component clamped to 999 ("0.32.1" → 32_001, "1.2.3" →
// 1_002_003). Semver pre-release / build-metadata suffixes are dropped
// before parsing ("0.32.0-rc.1" → 32_000): a uint32 cannot express
// pre-release ordering and controllers only compare the numeric for
// update detection. A leading "v"/"V" is tolerated. Strings without a
// leading numeric major component ("dev", "") map deterministically to
// 1, and the result is floored at 1: SoftwareVersion is mandatory on
// the Root endpoint, and 0 is matter.js's development default
// (BasicInformationServer.ts:59) that its initializer warns about — a
// production bridge never advertises it.
func SoftwareVersionFromString(version string) uint32 {
	v := strings.TrimSpace(version)
	v = strings.TrimPrefix(v, "v")
	v = strings.TrimPrefix(v, "V")
	// Drop semver pre-release ("-rc.1") and build metadata ("+g4ad313").
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	var parts [3]uint32
	majorParsed := false
	for i, p := range strings.SplitN(v, ".", 4) {
		if i > 2 {
			break
		}
		n, err := strconv.ParseUint(p, 10, 32)
		if err != nil {
			// Stop at the first non-numeric component; what parsed so
			// far keeps the derivation deterministic ("0.32.x" → 32_000).
			break
		}
		if n > 999 {
			n = 999
		}
		parts[i] = uint32(n)
		majorParsed = true
	}
	if !majorParsed {
		return 1
	}
	n := parts[0]*1_000_000 + parts[1]*1_000 + parts[2]
	if n == 0 {
		return 1
	}
	return n
}

// validateBasicInfoAttributes emits slog debug diagnostics for suspicious
// configurations caught at construction time. All checks are defensive and
// advisory — none block construction, and each has a printable fallback, so
// they log at debug rather than warn to avoid operational noise.
func validateBasicInfoAttributes(cfg Config) {
	if cfg.SerialNumber != "" && cfg.SerialNumber == fmt.Sprintf("%04X-%04X", cfg.VendorID, cfg.ProductID) {
		slog.Default().Debug("matter.basic_information.validate",
			slog.String("field", "UniqueID/SerialNumber"),
			slog.String("reason", "UniqueID and SerialNumber are identical — they should differ for controller caches"))
	}
	if strings.TrimSpace(cfg.VendorName) == "" {
		slog.Default().Debug("matter.basic_information.validate",
			slog.String("field", "VendorName"),
			slog.String("reason", "VendorName is empty — controllers may display an unknown vendor"))
	}
	if strings.TrimSpace(cfg.ProductName) == "" {
		slog.Default().Debug("matter.basic_information.validate",
			slog.String("field", "ProductName"),
			slog.String("reason", "ProductName is empty — controllers may display an unknown product"))
	}
	if cfg.HardwareVersionStr == "" {
		slog.Default().Debug("matter.basic_information.validate",
			slog.String("field", "HardwareVersionStr"),
			slog.String("reason", "HardwareVersionStr is empty — a fallback will be generated but callers should supply a real version string"))
	}
}

// defaultCapabilityMinima floors both fields at the Matter §11.1.5.18
// minimum of 3 (constraint "3 to 10000"). Mirrors matter.js
// packages/model/src/standard/elements/basic-information.element.ts:165-169
// (`CapabilityMinima.CaseSessionsPerFabric default 3`,
//
//	`CapabilityMinima.SubscriptionsPerFabric default 3`) and the
//
// `setDefault` floor used by BasicInformationServer.ts. Without this
// floor, the zero value `{0, 0}` reaches the wire and controllers
// that interpret 0 strictly (e.g. Apple Home) refuse to establish any
// subscription against the bridge even after a clean CASE handshake.
func defaultCapabilityMinima(in CapabilityMinimaStruct) CapabilityMinimaStruct {
	out := in
	if out.CaseSessionsPerFabric < 3 {
		out.CaseSessionsPerFabric = 3
	}
	if out.SubscriptionsPerFabric < 3 {
		out.SubscriptionsPerFabric = 3
	}
	return out
}

// StartUpEvent is the payload for the Matter §11.1.8.1 StartUp event
// (id 0x0000, priority Critical). Mirrors matter.js
// packages/model/src/standard/elements/basic-information.element.ts:84-90.
type StartUpEvent struct {
	// SoftwareVersion carries the current SoftwareVersion attribute
	// value (conformance M, field id 0x0).
	SoftwareVersion uint32
}

// ShutDownEvent is the payload for the Matter §11.1.8.2 ShutDown event
// (id 0x0001, priority Critical). No fields (conformance O). Mirrors
// matter.js basic-information.element.ts:91-95.
type ShutDownEvent struct{}

// LeaveEvent is the payload for the Matter §11.1.8.3 Leave event
// (id 0x0002, priority Info).
type LeaveEvent struct {
	// FabricIndex identifies the fabric that was removed (conformance M,
	// field id 0x0).
	FabricIndex uint8
}

// Compile-time assertions.
var (
	_ interfaces.MatterClusterServer                  = (*BasicInformation)(nil)
	_ interfaces.MatterClusterEventLister             = (*BasicInformation)(nil)
	_ interfaces.MatterEventReceiver                  = (*BasicInformation)(nil)
	_ interfaces.MatterClusterDataVersion             = (*BasicInformation)(nil)
	_ interfaces.MatterClusterAttributeWritePrivilege = (*BasicInformation)(nil)
)

// MatterClusterID implements [interfaces.MatterClusterServer].
func (b *BasicInformation) MatterClusterID() uint32 { return basicInfoClusterID }

// MinWritePrivilege implements [interfaces.MatterClusterAttributeWritePrivilege].
// NodeLabel and LocalConfigDisabled require Manage (4); Location
// requires Administer (5). Mirrors matter.js
// packages/model/src/standard/elements/basic-information.element.ts:36
// (NodeLabel "RW VM"), :40 (Location "RW VA"), :79
// (LocalConfigDisabled "RW VM").
func (b *BasicInformation) MinWritePrivilege(attrID uint32) uint8 {
	switch attrID {
	case basicInfoAttrNodeLabel, basicInfoAttrLocalConfigDisabled:
		return 4 // Manage
	case basicInfoAttrLocation:
		return 5 // Administer
	default:
		return 3 // Operate — standard default
	}
}

// MatterRead implements [interfaces.MatterClusterServer].
func (b *BasicInformation) MatterRead(attrID uint32) (any, bool) { //nolint:gocyclo,funlen // wire/dispatch table over many attribute/opcode cases
	b.mu.RLock()
	defer b.mu.RUnlock()
	switch attrID {
	case basicInfoAttrDataModelRevision:
		return b.dataModelRevision, true
	case basicInfoAttrVendorName:
		// matter.js basic-information.element.ts: VendorName constraint "max 32".
		return tlv.BoundedString{Value: b.vendorName, MaxBytes: 32}, true
	case basicInfoAttrVendorID:
		return b.vendorID, true
	case basicInfoAttrProductName:
		// matter.js basic-information.element.ts: ProductName constraint "max 32".
		return tlv.BoundedString{Value: b.productName, MaxBytes: 32}, true
	case basicInfoAttrProductID:
		return b.productID, true
	case basicInfoAttrNodeLabel:
		// matter.js basic-information.element.ts: NodeLabel constraint "max 32".
		return tlv.BoundedString{Value: b.nodeLabel, MaxBytes: 32}, true
	case basicInfoAttrLocation:
		// matter.js basic-information.element.ts: Location constraint "2".
		return tlv.BoundedString{Value: b.location, MaxBytes: 2}, true
	case basicInfoAttrHardwareVersion:
		return b.hardwareVersion, true
	case basicInfoAttrHardwareVersionStr:
		// matter.js basic-information.element.ts: HardwareVersionString constraint "1 to 64".
		return tlv.BoundedString{Value: b.hardwareString, MaxBytes: 64}, true
	case basicInfoAttrSoftwareVersion:
		return b.softwareVersion, true
	case basicInfoAttrSoftwareVersionStr:
		// matter.js basic-information.element.ts: SoftwareVersionString constraint "1 to 64".
		return tlv.BoundedString{Value: b.softwareString, MaxBytes: 64}, true
	// Optional attributes — return UnsupportedAttribute when unset so
	// matter.js / Apple Home don't see a constraint-violating empty
	// string (ManufacturingDate has min=8, PartNumber/ProductURL/
	// ProductLabel/SerialNumber are conformance="O" but their max-N
	// constraint is enforced by the HAP service mapper even when the
	// value is "").
	case basicInfoAttrManufacturingDate:
		if b.manufacturingDate == "" {
			return nil, false
		}
		return b.manufacturingDate, true
	case basicInfoAttrPartNumber:
		if b.partNumber == "" {
			return nil, false
		}
		return b.partNumber, true
	case basicInfoAttrProductURL:
		if b.productURL == "" {
			return nil, false
		}
		return b.productURL, true
	case basicInfoAttrProductLabel:
		if b.productLabel == "" {
			return nil, false
		}
		return b.productLabel, true
	case basicInfoAttrSerialNumber:
		// When no Config.SerialNumber was provided, derive a deterministic
		// fallback from UniqueID so Apple Home's HAP-mapper cache for
		// EP0:0x28:0x000F (BasicInformation.SerialNumber) is pre-populated
		// by the initial Subscribe. Without the fallback MTRDevice logs
		// `could not find cached attribute values` and the post-Subscribe
		// HAP service-build retries pointlessly. Mirrors
		// BridgedDeviceBasicInformation's same fallback pattern at
		// NewBridgedDeviceBasicInformation:170-173 and chip bridge-app's
		// `DeviceLayerBasicInformationPolicy.h:75-77` SerialNumber
		// non-empty contract.
		if b.serialNumber == "" {
			return basicInfoSerialFromUniqueID(b.uniqueID()), true
		}
		return b.serialNumber, true
	// LocalConfigDisabled (0x10) and ConfigurationVersion (0x18) are
	// OPTIONAL on the *root* BasicInformation cluster and matter.js's
	// bridge sample does NOT emit them on Root. Reachable (0x11) IS
	// emitted as `true` — verified empirically (Run 15 vs Run 16):
	// omitting Reachable on Root flips Apple's HMAccessory
	// back to Reachable=NO + Controllable=NO. Apple consults the Root
	// Reachable attribute even when bridged endpoints also carry the
	// attribute on BridgedDeviceBasicInformation.
	case basicInfoAttrLocalConfigDisabled:
		return nil, false
	case basicInfoAttrReachable:
		return true, true
	case basicInfoAttrUniqueID:
		return b.uniqueID(), true
	case basicInfoAttrCapabilityMinima:
		return b.capabilityMinima, true
	case basicInfoAttrProductAppearance:
		if b.productAppearance == (ProductAppearanceStruct{}) {
			return nil, false
		}
		return b.productAppearance, true
	case basicInfoAttrSpecificationVersion:
		return cluster.SpecificationVersion, true
	case basicInfoAttrMaxPathsPerInvoke:
		return b.maxPathsPerInvoke, true
	case basicInfoAttrConfigurationVersion:
		// Matter 1.5 optional. matter.js Sample omits this on Root.
		// Emit only when the config explicitly set a non-zero value.
		if b.configurationVersion == 0 {
			return nil, false
		}
		return b.configurationVersion, true
	case cluster.AttrGlobalFeatureMap:
		return uint32(0), true
	case cluster.AttrGlobalClusterRevision:
		return basicInfoClusterRevision, true
	}
	return nil, false
}

// basicInfoSerialFromUniqueID slices the UniqueID-derived hex down to
// the spec's 32-byte SerialNumber ceiling (Matter §11.1.5.20
// "max 32") so the wire value satisfies both the constraint and
// Apple's HAP-mapper precondition. 16 hex chars (= 16 bytes) is the
// natural width of UniqueID's SHA-256 prefix; longer would not
// improve uniqueness given the same input set.
func basicInfoSerialFromUniqueID(uid string) string {
	if len(uid) <= 16 {
		return uid
	}
	return uid[:16]
}

// uniqueID derives a stable 32-character hex identifier from
// VendorID + ProductID + the configured NodeLabel + SerialNumber.
// Every input is fixed at construction — in particular the label is the
// configured one, never the live [BasicInformation.nodeLabel] a
// controller can write, because UniqueID carries quality F (fixed for
// the lifetime of the device, Matter §11.1.5.13). Mirrors matter.js
// `BasicInformationServer.createUniqueId()` (32-char random a–zA–Z0–9
// persisted with Quality "FN") in length and shape, but uses a
// deterministic SHA-256 prefix so the value survives bridge restarts
// without an additional persistence layer. The previous
// `"%04X-%04X" % (vendorID, productID)` form (≤ 9 chars, identical
// across every openccu-loom instance running the same test
// VendorID+ProductID) looked like a class identifier to Apple's
// HAP-Service mapper — MTRDevice rejects the topology silently and
// the bridge surfaces as "added but not supported". The hex output
// also satisfies the Matter Core Spec §11.1.5.13 32-byte UTF-8
// ceiling.
func (b *BasicInformation) uniqueID() string {
	// Mix in the per-boot salt so Apple's HAP cache sees a fresh
	// fingerprint after every daemon restart; see package bootid.
	salt := bootid.Salt()
	h := sha256.Sum256(fmt.Appendf(nil, "%s|%04X|%04X|%s|%s",
		hex.EncodeToString(salt[:]), b.vendorID, b.productID, b.identityLabel, b.serialNumber))
	return hex.EncodeToString(h[:16])
}

// MatterWrite accepts NodeLabel, Location, and LocalConfigDisabled
// per Matter §11.1.7. Other attributes are read-only.
func (b *BasicInformation) MatterWrite(_ context.Context, attrID uint32, value any, _ hmenum.CommandPriority) error {
	switch attrID {
	case basicInfoAttrNodeLabel:
		s, ok := value.(string)
		if !ok {
			return fmt.Errorf("matter: NodeLabel write expected string, got %T", value)
		}
		if len(s) > 32 {
			return errors.New("matter: NodeLabel exceeds 32 utf-8 bytes")
		}
		b.mu.Lock()
		b.nodeLabel = s
		b.mu.Unlock()
		b.dataVersion.Bump()
		b.firePersistentWrite()
		return nil
	case basicInfoAttrLocation:
		s, ok := value.(string)
		if !ok {
			return fmt.Errorf("matter: Location write expected string, got %T", value)
		}
		if len(s) != 2 {
			return fmt.Errorf("matter: Location must be ISO-3166 2-letter (got len=%d)", len(s))
		}
		b.mu.Lock()
		b.location = s
		b.mu.Unlock()
		b.dataVersion.Bump()
		b.firePersistentWrite()
		return nil
	case basicInfoAttrLocalConfigDisabled:
		// Persistent across reboots per Matter §11.1.6.17. openccu-loom
		// has no physical config UI on the bridge, so the attribute is
		// effectively a no-op; we accept the write but discard the
		// value. Mirrors how matter.js + chip-tool treat headless
		// bridges.
		b.dataVersion.Bump()
		return nil
	}
	return fmt.Errorf("%w: 0x%04X", errBasicInfoUnknown, attrID)
}

// MatterInvoke always rejects — BasicInformation has no commands.
func (b *BasicInformation) MatterInvoke(_ context.Context, cmdID uint32, _ any, _ hmenum.CommandPriority) (any, error) {
	return nil, im.UnsupportedCommandf("matter: BasicInformation has no commands (got 0x%02X)", cmdID)
}

// MatterReportable lists the subscribe-able attributes.
func (b *BasicInformation) MatterReportable() []uint32 {
	return []uint32{
		basicInfoAttrNodeLabel,
		basicInfoAttrLocation,
		basicInfoAttrReachable,
	}
}

// MatterAttributes implements [interfaces.MatterClusterAttributeLister]
// so wildcard subscribe / read enumerates the full attribute surface.
// Apple Home builds its HAP service map from the post-CASE wildcard
// subscribe — without VendorID / ProductID / NodeLabel / UniqueID
// arriving in the initial-report stream Apple aborts at
// HMMTRAccessoryPairingStep_BuildingHAPServicesAndCharacteristicsFromCHIP
// with HAPErrorDomain Code 24. Globals (FeatureMap + ClusterRevision)
// are merged in by the dispatcher.
func (b *BasicInformation) MatterAttributes() []uint32 {
	// LocalConfigDisabled (0x10) and ConfigurationVersion (0x18) are
	// intentionally OMITTED — optional on Root BasicInformation, and
	// matter.js's bridge sample does not emit them. Reachable (0x11)
	// IS emitted because Apple's HMAccessory.Reachable signal depends
	// on it (Run 15 vs Run 16 verification, empirically confirmed).
	out := []uint32{
		basicInfoAttrDataModelRevision,
		basicInfoAttrVendorName,
		basicInfoAttrVendorID,
		basicInfoAttrProductName,
		basicInfoAttrProductID,
		basicInfoAttrNodeLabel,
		basicInfoAttrLocation,
		basicInfoAttrHardwareVersion,
		basicInfoAttrHardwareVersionStr,
		basicInfoAttrSoftwareVersion,
		basicInfoAttrSoftwareVersionStr,
		basicInfoAttrReachable,
		basicInfoAttrUniqueID,
		basicInfoAttrCapabilityMinima,
		basicInfoAttrSpecificationVersion,
		basicInfoAttrMaxPathsPerInvoke,
	}
	if b.manufacturingDate != "" {
		out = append(out, basicInfoAttrManufacturingDate)
	}
	if b.partNumber != "" {
		out = append(out, basicInfoAttrPartNumber)
	}
	if b.productURL != "" {
		out = append(out, basicInfoAttrProductURL)
	}
	if b.productLabel != "" {
		out = append(out, basicInfoAttrProductLabel)
	}
	// SerialNumber is always enumerated — when no value is configured,
	// MatterRead serves the UniqueID-derived fallback so Apple's HAP-mapper
	// cache for EP0:0x28:0x000F is pre-populated by the initial Subscribe.
	out = append(out, basicInfoAttrSerialNumber)
	if b.productAppearance != (ProductAppearanceStruct{}) {
		out = append(out, basicInfoAttrProductAppearance)
	}
	return out
}

// SetNodeLabel updates NodeLabel out-of-band (e.g. from the config
// UI or the boot-time restore of a persisted commissioner write, not
// from a Matter write). Goes through the same length check as the
// Matter write path. Does NOT fire the persistence hook — restores
// must not echo back into the store.
func (b *BasicInformation) SetNodeLabel(s string) error {
	if len(s) > 32 {
		return errors.New("matter: NodeLabel exceeds 32 utf-8 bytes")
	}
	b.mu.Lock()
	b.nodeLabel = s
	b.mu.Unlock()
	return nil
}

// SetLocation updates Location out-of-band (boot-time restore of a
// persisted commissioner write). Same validation as the Matter write
// path; does NOT fire the persistence hook.
func (b *BasicInformation) SetLocation(s string) error {
	if len(s) != 2 {
		return fmt.Errorf("matter: Location must be ISO-3166 2-letter (got len=%d)", len(s))
	}
	b.mu.Lock()
	b.location = s
	b.mu.Unlock()
	return nil
}

// SetOnPersistentWrite wires a hook fired after every successful
// Matter write to a non-volatile ("N" quality, Matter §11.1.6)
// attribute — NodeLabel and Location — with the cluster's current
// values. The daemon persists them so a commissioner-set label
// survives a restart; matter.js gets the same via its persistent
// behavior state. Pass nil to detach.
func (b *BasicInformation) SetOnPersistentWrite(fn func(nodeLabel, location string)) {
	b.mu.Lock()
	b.onPersistentWrite = fn
	b.mu.Unlock()
}

// firePersistentWrite snapshots the current NodeLabel/Location and
// invokes the persistence hook outside the cluster lock.
func (b *BasicInformation) firePersistentWrite() {
	b.mu.RLock()
	fn := b.onPersistentWrite
	label := b.nodeLabel
	loc := b.location
	b.mu.RUnlock()
	if fn != nil {
		fn(label, loc)
	}
}

// MatterEvents implements [interfaces.MatterClusterEventLister] so the
// dispatcher synthesises the global EventList (0xFFFA) attribute
// correctly for this cluster.
// ReachableChanged (0x0003) is always included because the Reachable
// attribute is always exposed on Root BasicInformation and BDBI, and the
// event's conformance is "Reachable" — present exactly when the attribute
// is present.
func (b *BasicInformation) MatterEvents() []uint32 {
	return []uint32{
		basicInfoEventStartUp,
		basicInfoEventShutDown,
		basicInfoEventLeave,
		basicInfoEventReachableChanged,
	}
}

// MatterDataVersion implements [interfaces.MatterClusterDataVersion].
// Bumped on every successful NodeLabel / Location / LocalConfigDisabled
// write so DataVersionFilter evaluation correctly detects the cluster
// changed; controllers cache via this counter and skip the cluster on
// re-reads when it stays unchanged.
func (b *BasicInformation) MatterDataVersion() uint32 {
	return b.dataVersion.Current()
}

// SetMatterEventEmitter implements [interfaces.MatterEventReceiver].
// Called by the bridge during topology assembly so the emit methods can
// fire their events without the cluster holding a direct reference to
// the bridge. Idempotent — re-wiring during topology rebuild replaces
// the emitter cleanly.
func (b *BasicInformation) SetMatterEventEmitter(emitter interfaces.MatterEventEmitter) {
	b.mu.Lock()
	b.emitter = emitter
	b.mu.Unlock()
}

// SetEndpoint stamps the endpoint id this BasicInformation server is
// mounted on. Matter events carry the (endpoint, cluster, event) triple
// so the commissioner can fan them out to the right subscription path.
// The root endpoint is always 0 in standard topologies, but the bridge
// injects the real value here so the cluster does not hard-code it.
func (b *BasicInformation) SetEndpoint(endpoint uint16) {
	b.mu.Lock()
	b.endpoint = endpoint
	b.mu.Unlock()
}

// EmitStartUp fires the Matter §11.1.8.1 StartUp event (id 0x0000,
// priority Critical) with the current SoftwareVersion. No-op when the
// emitter has not been wired yet — the daemon calls this once at startup
// after topology assembly. Mirrors matter.js
// packages/node/src/behaviors/basic-information/BasicInformationServer.ts
// where the startup event is emitted with the softwareVersion value.
func (b *BasicInformation) EmitStartUp() {
	b.mu.RLock()
	emitter := b.emitter
	endpoint := b.endpoint
	swVersion := b.softwareVersion
	b.mu.RUnlock()
	if emitter == nil {
		slog.Default().Warn("matter.basic_information.emit_startup_skipped",
			slog.String("reason", "emitter nil — wiring race"))
		return
	}
	slog.Default().Info("matter.basic_information.emit_startup",
		slog.Any("endpoint", endpoint), slog.Any("sw_version", swVersion))
	emitter.MatterEmitEvent(endpoint, basicInfoClusterID, basicInfoEventStartUp,
		StartUpEvent{SoftwareVersion: swVersion},
		interfaces.MatterEventPriorityCritical)
}

// EmitShutDown fires the Matter §11.1.8.2 ShutDown event (id 0x0001,
// priority Critical, no fields). No-op when the emitter has not been
// wired yet — the daemon calls this in its shutdown hook. Mirrors
// matter.js basic-information.element.ts:91-95.
func (b *BasicInformation) EmitShutDown() {
	b.mu.RLock()
	emitter := b.emitter
	endpoint := b.endpoint
	b.mu.RUnlock()
	if emitter == nil {
		return
	}
	emitter.MatterEmitEvent(endpoint, basicInfoClusterID, basicInfoEventShutDown,
		ShutDownEvent{},
		interfaces.MatterEventPriorityCritical)
}

// EmitLeave fires the Matter §11.1.8.3 Leave event (id 0x0002, priority
// Info) with the given fabricIndex. Called by the OperationalCredentials
// cluster's RemoveFabric handler so subscribers learn which fabric
// departed. No-op when the emitter has not been wired yet.
func (b *BasicInformation) EmitLeave(fabricIndex uint8) {
	b.mu.RLock()
	emitter := b.emitter
	endpoint := b.endpoint
	b.mu.RUnlock()
	if emitter == nil {
		return
	}
	emitter.MatterEmitEvent(endpoint, basicInfoClusterID, basicInfoEventLeave,
		LeaveEvent{FabricIndex: fabricIndex},
		interfaces.MatterEventPriorityInfo)
}

// EmitReachableChanged fires the Matter §11.1.8.4 ReachableChanged event
// (id 0x0003, priority Info) when the Reachable attribute transitions.
// The event conformance is "Reachable", meaning it is defined whenever the
// Reachable attribute is present. For the Root endpoint and BDBI clusters
// Reachable is always exposed, so this method should be called whenever
// the reachability state of the bridge or a bridged device changes.
// No-op when the emitter has not been wired yet.
func (b *BasicInformation) EmitReachableChanged(reachable bool) {
	b.mu.RLock()
	emitter := b.emitter
	endpoint := b.endpoint
	b.mu.RUnlock()
	if emitter == nil {
		return
	}
	emitter.MatterEmitEvent(endpoint, basicInfoClusterID, basicInfoEventReachableChanged,
		ReachableChangedEvent{ReachableNewValue: reachable},
		interfaces.MatterEventPriorityInfo)
}
