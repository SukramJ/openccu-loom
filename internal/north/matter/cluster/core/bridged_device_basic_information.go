// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package core

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// BridgedDeviceBasicInformation implements the Matter cluster
// (0x0039) per Matter Core Specification 1.5.1 §9.13. Mandatory on
// every bridged endpoint. It mirrors the relevant subset of
// BasicInformation (vendor/product/version metadata) plus the
// bridge-specific Reachable + ReachableChanged event surface.
//
// Compared to BasicInformation:
//
//   - No DataModelRevision / SpecificationVersion / CapabilityMinima
//     (they live on the root endpoint only).
//   - Reachable is mutable based on bridged-device availability.
//   - UniqueID is mandatory (vs. optional on BasicInformation).
type BridgedDeviceBasicInformation struct {
	mu sync.RWMutex

	// dataVersion tracks the per-cluster monotonic counter per Matter
	// §10.6.5. Bumped after every successful state mutation (Reachable
	// flip, NodeLabel write) so DataVersionFilter evaluation works.
	// Satisfies [interfaces.MatterClusterDataVersion].
	dataVersion cluster.DataVersionTracker

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
	uniqueID          string

	// Mutable.
	nodeLabel string
	reachable bool

	// Event surface — wired by the bridge during topology assembly via
	// [SetMatterEventEmitter] + [SetEndpoint] so [SetReachable] can fire
	// the spec-mandated ReachableChanged event (Matter §9.13.6, event id
	// 0x3, priority Critical) without holding a reference to the bridge.
	// Mirrors matter.js packages/node/src/behaviors/bridged-device-basic-
	// information/BridgedDeviceBasicInformationServer.ts (state.reachable
	// setter → events.reachableChanged.emit).
	endpoint uint16
	emitter  interfaces.MatterEventEmitter
}

// Cluster ID + revision per Matter §9.13.
const (
	bridgedBasicInfoClusterID       uint32 = 0x0039
	bridgedBasicInfoClusterRevision uint16 = 6 // matter.js HEAD bridged-device-basic-information.element.ts:20 default=6

	bridgedBasicInfoAttrVendorName         uint32 = 0x0001
	bridgedBasicInfoAttrVendorID           uint32 = 0x0002
	bridgedBasicInfoAttrProductName        uint32 = 0x0003
	bridgedBasicInfoAttrProductID          uint32 = 0x0004
	bridgedBasicInfoAttrNodeLabel          uint32 = 0x0005
	bridgedBasicInfoAttrHardwareVersion    uint32 = 0x0007
	bridgedBasicInfoAttrHardwareVersionStr uint32 = 0x0008
	bridgedBasicInfoAttrSoftwareVersion    uint32 = 0x0009
	bridgedBasicInfoAttrSoftwareVersionStr uint32 = 0x000A
	bridgedBasicInfoAttrManufacturingDate  uint32 = 0x000B
	bridgedBasicInfoAttrPartNumber         uint32 = 0x000C
	bridgedBasicInfoAttrProductURL         uint32 = 0x000D
	bridgedBasicInfoAttrProductLabel       uint32 = 0x000E
	bridgedBasicInfoAttrSerialNumber       uint32 = 0x000F
	bridgedBasicInfoAttrReachable          uint32 = 0x0011
	bridgedBasicInfoAttrUniqueID           uint32 = 0x0012
	bridgedBasicInfoAttrProductAppearance  uint32 = 0x0014
	// ConfigurationVersion (Attr 0x0018) per Matter Core Spec §9.13 and
	// matter.js HEAD packages/model/src/standard/elements/
	// bridged-device-basic-information.element.ts:48, conformance "P,
	// [Rev >= v5]". Apple iOS 18.4+ probes for it via HMHome's bridge-
	// validator and surfaces an "Outdated configuration" warning when
	// absent. Static value `1` is sufficient — we have no runtime
	// configuration revisions to track.
	bridgedBasicInfoAttrConfigurationVersion uint32 = 0x0018

	// bridgedBasicInfoEventReachableChanged is the spec-mandated Matter
	// §9.13.6 / matter.js HEAD bridged-device-basic-information.element.ts
	// line 55 event id (0x0003, priority Critical, conformance "M") that
	// fires when [SetReachable] flips the reachable flag.
	bridgedBasicInfoEventReachableChanged uint32 = 0x0003
)

// Exported cluster / event identifiers so the bridge can address the
// §9.13.6 ReachableChanged event when it fires the flip from the
// device-availability subscription (the cluster server itself is
// reconstructed per dispatch and cannot retain the emitter wiring).
const (
	// BridgedDeviceBasicInformationClusterID is the cluster ID (0x0039).
	BridgedDeviceBasicInformationClusterID = bridgedBasicInfoClusterID
	// EventReachableChanged is the §9.13.6 ReachableChanged event ID
	// (0x0003).
	EventReachableChanged = bridgedBasicInfoEventReachableChanged
)

// ReachableChangedEvent is the cluster-native payload for event
// 0x0003. Mirrors matter.js basic-information.element.ts:117 +
// bridged-device-basic-information.element.ts:55. The ReachableNewValue
// field is encoded as a single TLV bool at ContextTag(0).
type ReachableChangedEvent struct {
	// ReachableNewValue is the post-flip reachable state. ContextTag(0)
	// per Matter §9.13.6.4.
	ReachableNewValue bool
}

// errBridgedBasicInfoUnknown is returned for unsupported writes /
// unknown attributes.
var errBridgedBasicInfoUnknown = errors.New("matter: BridgedDeviceBasicInformation unknown / read-only attribute")

// BridgedConfig carries the bridged device's static metadata. NodeLabel
// + Reachable + UniqueID are mandatory; the rest are optional.
type BridgedConfig struct {
	VendorName         string
	VendorID           uint16
	ProductName        string
	ProductID          uint16
	ProductLabel       string
	ProductURL         string
	ProductAppearance  ProductAppearanceStruct
	HardwareVersion    uint16
	HardwareVersionStr string
	SoftwareVersion    uint32
	SoftwareVersionStr string
	ManufacturingDate  string
	PartNumber         string
	SerialNumber       string
	UniqueID           string
	NodeLabel          string
	Reachable          bool
}

// NewBridgedDeviceBasicInformation constructs the cluster from cfg.
// UniqueID and NodeLabel are mandatory.
//
// When SerialNumber comes in empty we backfill it from UniqueID so the
// attribute does not surface as `(nil, false)` (= UnsupportedAttribute)
// on every read — Apple's HAP-service mapper logs
// `could not find cached attribute values` for the empty field and
// silently downgrades the bridged endpoint. UniqueID is mandatory and
// non-empty, so this is a safe per-endpoint distinct fallback that
// satisfies chip's MTRBaseClusters non-null expectation while staying
// matter.js-compatible (matter.js's bridge sample sets SerialNumber
// to the same address it sets uniqueId to). VendorName / ProductName
// stay empty if the caller passed them empty — matter.js does the
// same and Apple tolerates their absence; only SerialNumber is the
// observed Apple HAP-mapper requirement.
func NewBridgedDeviceBasicInformation(cfg BridgedConfig) (*BridgedDeviceBasicInformation, error) {
	if cfg.UniqueID == "" {
		return nil, errors.New("matter: BridgedDeviceBasicInformation Config.UniqueID must be non-empty")
	}
	if cfg.NodeLabel == "" {
		return nil, errors.New("matter: BridgedDeviceBasicInformation Config.NodeLabel must be non-empty")
	}
	serialNumber := cfg.SerialNumber
	if serialNumber == "" {
		serialNumber = cfg.UniqueID
	}
	// Reachable is taken from cfg so the bridge can advertise the
	// underlying CCU device's live availability at construction time
	// (materialize.go reads dev.Available() per dispatch). A dead device
	// must surface Reachable=false immediately, not after a deferred
	// SetReachable(false) call that — because cluster servers are
	// reconstructed per dispatch — would never persist. Mirrors matter.js
	// BridgedDeviceBasicInformationServer where `reachable` is a plain
	// state field initialised from the caller-supplied value.
	reachable := cfg.Reachable
	b := &BridgedDeviceBasicInformation{
		vendorName:        cfg.VendorName,
		vendorID:          cfg.VendorID,
		productName:       cfg.ProductName,
		productID:         cfg.ProductID,
		productLabel:      cfg.ProductLabel,
		productURL:        cfg.ProductURL,
		productAppearance: cfg.ProductAppearance,
		hardwareVersion:   cfg.HardwareVersion,
		hardwareString:    cfg.HardwareVersionStr,
		softwareVersion:   cfg.SoftwareVersion,
		softwareString:    cfg.SoftwareVersionStr,
		manufacturingDate: cfg.ManufacturingDate,
		partNumber:        cfg.PartNumber,
		serialNumber:      serialNumber,
		uniqueID:          cfg.UniqueID,
		nodeLabel:         cfg.NodeLabel,
		reachable:         reachable,
	}
	validateBridgedBasicInfoAttributes(cfg, serialNumber)
	return b, nil
}

// validateBridgedBasicInfoAttributes emits slog warnings for suspicious
// BridgedConfig values caught at construction time. All checks are
// defensive diagnostics; none block construction.
func validateBridgedBasicInfoAttributes(cfg BridgedConfig, resolvedSerial string) {
	// When SerialNumber was not set by the caller it is backfilled from
	// UniqueID (see constructor). Warn when the two fields collide so
	// operators can identify over-reused identifiers in multi-device setups.
	// The warning fires only when the caller explicitly supplied SerialNumber
	// (cfg.SerialNumber != "") and it happens to equal UniqueID — the
	// intentional backfill path (cfg.SerialNumber == "") is silent.
	if cfg.SerialNumber != "" && resolvedSerial == cfg.UniqueID {
		slog.Default().Warn(
			"matter.bridged_device_basic_information.validate",
			slog.String("field", "SerialNumber"),
			slog.String("reason", "SerialNumber equals UniqueID — they should differ for controller caches"),
			slog.String("value", cfg.UniqueID),
		)
	}
	// VendorID is optional on bridged devices, but when supplied it must be a
	// valid device identity. Mirrors matter.js basic-information-validators.ts:32
	// (`vendorId > 0xfff4`); 0xFFF5-0xFFFF are reserved test/anchor IDs. Warn
	// rather than block — bridged metadata is advisory, not a commissioned
	// identity.
	if cfg.VendorID != 0 && cfg.VendorID > 0xFFF4 {
		slog.Default().Warn(
			"matter.bridged_device_basic_information.validate",
			slog.String("field", "VendorID"),
			slog.String("reason", "VendorID outside 0x0001-0xFFF4 is not a valid device identity"),
			slog.String("value", fmt.Sprintf("0x%04X", cfg.VendorID)),
		)
	}
}

// Compile-time assertions.
var (
	_ interfaces.MatterClusterServer      = (*BridgedDeviceBasicInformation)(nil)
	_ interfaces.MatterClusterDataVersion = (*BridgedDeviceBasicInformation)(nil)
	_ interfaces.MatterClusterEventLister = (*BridgedDeviceBasicInformation)(nil)
)

// MatterClusterID implements [interfaces.MatterClusterServer].
func (b *BridgedDeviceBasicInformation) MatterClusterID() uint32 { return bridgedBasicInfoClusterID }

// MatterDataVersion implements [interfaces.MatterClusterDataVersion].
// Returns the current per-cluster monotonic counter bumped after every
// successful Reachable flip or NodeLabel write. Mirrors matter.js
// BridgedDeviceBasicInformationServer.ts DataVersion tracking.
func (b *BridgedDeviceBasicInformation) MatterDataVersion() uint32 {
	return b.dataVersion.Current()
}

// MatterRead implements [interfaces.MatterClusterServer].
func (b *BridgedDeviceBasicInformation) MatterRead(attrID uint32) (any, bool) { //nolint:gocyclo,funlen // wire/dispatch table over many attribute/opcode cases
	b.mu.RLock()
	defer b.mu.RUnlock()
	switch attrID {
	// VendorName / VendorID / ProductName / ProductID and the
	// hardware/software version attributes are all OPTIONAL on
	// BridgedDeviceBasicInformation (Matter Core §9.13.6 — mandatory is
	// only Reachable + UniqueID + the global metadata attributes;
	// NodeLabel is mandatory-writable). matter.js's bridge sample
	// (`examples/device-bridge-onoff/src/BridgedDevicesNode.ts:91-99`)
	// only sets nodeLabel + productName + productLabel + serialNumber +
	// reachable on each bridged endpoint and leaves vendorName / vendorId
	// / productId undefined → matter.js does not put them on the wire.
	// Apple Home's HAP-service mapper treats VendorID=0xFFF1 (CSA Test
	// Vendor) on a bridged endpoint as "test hardware" and quietly
	// excludes the endpoint from its persistent MTRDevice cache — surfaces
	// as `Storing cluster information count: 3` (vs. the 30+ records a
	// fully accepted bridge produces). Mirror matter.js: when the field
	// is zero/empty, treat the attribute as unimplemented.
	case bridgedBasicInfoAttrVendorName:
		if b.vendorName == "" {
			return nil, false
		}
		return b.vendorName, true
	case bridgedBasicInfoAttrVendorID:
		if b.vendorID == 0 {
			return nil, false
		}
		return b.vendorID, true
	case bridgedBasicInfoAttrProductName:
		if b.productName == "" {
			return nil, false
		}
		return b.productName, true
	case bridgedBasicInfoAttrProductID:
		if b.productID == 0 {
			return nil, false
		}
		return b.productID, true
	case bridgedBasicInfoAttrNodeLabel:
		return b.nodeLabel, true
	case bridgedBasicInfoAttrHardwareVersion:
		if b.hardwareVersion == 0 {
			return nil, false
		}
		return b.hardwareVersion, true
	case bridgedBasicInfoAttrHardwareVersionStr:
		if b.hardwareString == "" {
			return nil, false
		}
		return b.hardwareString, true
	case bridgedBasicInfoAttrSoftwareVersion:
		if b.softwareVersion == 0 {
			return nil, false
		}
		return b.softwareVersion, true
	case bridgedBasicInfoAttrSoftwareVersionStr:
		if b.softwareString == "" {
			return nil, false
		}
		return b.softwareString, true
	// Optional attributes are advertised conditionally — exposing them
	// with an empty value violates matter.js's per-attribute constraint
	// (e.g. ManufacturingDate min=8) and Apple Home's HAP service mapper
	// rejects the cluster, sending RemoveFabric. matter.js's
	// home-assistant-matter-bridge only emits these when the underlying
	// device registry has a real value; we mirror that by returning
	// (nil, false) for empty values, which surfaces as
	// `UnsupportedAttribute` to the controller — exactly the spec's
	// signal for "this optional attribute is not implemented".
	case bridgedBasicInfoAttrManufacturingDate:
		if b.manufacturingDate == "" {
			return nil, false
		}
		return b.manufacturingDate, true
	case bridgedBasicInfoAttrPartNumber:
		if b.partNumber == "" {
			return nil, false
		}
		return b.partNumber, true
	case bridgedBasicInfoAttrProductURL:
		if b.productURL == "" {
			return nil, false
		}
		return b.productURL, true
	case bridgedBasicInfoAttrProductLabel:
		if b.productLabel == "" {
			return nil, false
		}
		return b.productLabel, true
	case bridgedBasicInfoAttrSerialNumber:
		if b.serialNumber == "" {
			return nil, false
		}
		return b.serialNumber, true
	case bridgedBasicInfoAttrReachable:
		return b.reachable, true
	case bridgedBasicInfoAttrUniqueID:
		return b.uniqueID, true
	case bridgedBasicInfoAttrProductAppearance:
		if b.productAppearance == (ProductAppearanceStruct{}) {
			return nil, false
		}
		return b.productAppearance, true
	case bridgedBasicInfoAttrConfigurationVersion:
		// Static `1` matches chip's default; bumped on any future
		// runtime-configuration change to the bridged-device surface.
		return uint32(1), true
	case cluster.AttrGlobalFeatureMap:
		return uint32(0), true
	case cluster.AttrGlobalClusterRevision:
		return bridgedBasicInfoClusterRevision, true
	}
	return nil, false
}

// MatterWrite accepts NodeLabel only; Reachable updates flow through
// [BridgedDeviceBasicInformation.SetReachable] from the bridge core.
func (b *BridgedDeviceBasicInformation) MatterWrite(_ context.Context, attrID uint32, value any, _ hmenum.CommandPriority) error {
	if attrID != bridgedBasicInfoAttrNodeLabel {
		return fmt.Errorf("%w: 0x%04X", errBridgedBasicInfoUnknown, attrID)
	}
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
	// Bump DataVersion after a successful NodeLabel mutation so
	// DataVersionFilter evaluation correctly detects the cluster changed.
	b.dataVersion.Bump()
	return nil
}

// MatterInvoke always rejects — BridgedDeviceBasicInformation has no
// commands.
func (b *BridgedDeviceBasicInformation) MatterInvoke(_ context.Context, cmdID uint32, _ any, _ hmenum.CommandPriority) (any, error) {
	return nil, im.UnsupportedCommandf("matter: BridgedDeviceBasicInformation has no commands (got 0x%02X)", cmdID)
}

// MatterReportable returns the list of subscribe-able attributes.
// Reachable is the primary one — controllers watch it to gate UX.
func (b *BridgedDeviceBasicInformation) MatterReportable() []uint32 {
	return []uint32{bridgedBasicInfoAttrReachable, bridgedBasicInfoAttrNodeLabel}
}

// MatterEvents implements [interfaces.MatterClusterEventLister] so the
// dispatcher synthesises the global EventList (0xFFFA) attribute correctly
// for this cluster. ReachableChanged (0x0003) is the only event in this cluster.
func (b *BridgedDeviceBasicInformation) MatterEvents() []uint32 {
	return []uint32{bridgedBasicInfoEventReachableChanged}
}

// MatterAttributes implements [interfaces.MatterClusterAttributeLister]
// so wildcard subscribe / read enumerates every attribute the cluster
// exposes. Apple Home reads this set on every bridged endpoint to
// build its HAP service map; missing attributes leave Apple's
// MTRDevice with VID/PID Unknown and abort pairing.
func (b *BridgedDeviceBasicInformation) MatterAttributes() []uint32 {
	// Mandatory attributes always reported. Optional ones gate on the
	// underlying value being non-empty — Apple Home's HAP service mapper
	// validates per-attribute constraints (e.g. ManufacturingDate min=8)
	// against every attribute the cluster advertises, even if the
	// attribute would surface as the empty string. Skipping un-set
	// optional attributes mirrors matter.js's behaviour-layer pattern of
	// "the cluster only owns the attribute when the application set it".
	// AttributeList enumerates only the attributes the cluster *actually*
	// implements — see MatterRead. Optional attributes that are
	// zero/empty are omitted so wildcard subscribe doesn't carry them on
	// the wire (matter.js bridged-sample pattern).
	out := []uint32{
		bridgedBasicInfoAttrNodeLabel,
		bridgedBasicInfoAttrReachable,
		bridgedBasicInfoAttrUniqueID,
		bridgedBasicInfoAttrConfigurationVersion,
	}
	if b.vendorName != "" {
		out = append(out, bridgedBasicInfoAttrVendorName)
	}
	if b.vendorID != 0 {
		out = append(out, bridgedBasicInfoAttrVendorID)
	}
	if b.productName != "" {
		out = append(out, bridgedBasicInfoAttrProductName)
	}
	if b.productID != 0 {
		out = append(out, bridgedBasicInfoAttrProductID)
	}
	if b.hardwareVersion != 0 {
		out = append(out, bridgedBasicInfoAttrHardwareVersion)
	}
	if b.hardwareString != "" {
		out = append(out, bridgedBasicInfoAttrHardwareVersionStr)
	}
	if b.softwareVersion != 0 {
		out = append(out, bridgedBasicInfoAttrSoftwareVersion)
	}
	if b.softwareString != "" {
		out = append(out, bridgedBasicInfoAttrSoftwareVersionStr)
	}
	if b.manufacturingDate != "" {
		out = append(out, bridgedBasicInfoAttrManufacturingDate)
	}
	if b.partNumber != "" {
		out = append(out, bridgedBasicInfoAttrPartNumber)
	}
	if b.productURL != "" {
		out = append(out, bridgedBasicInfoAttrProductURL)
	}
	if b.productLabel != "" {
		out = append(out, bridgedBasicInfoAttrProductLabel)
	}
	if b.serialNumber != "" {
		out = append(out, bridgedBasicInfoAttrSerialNumber)
	}
	if b.productAppearance != (ProductAppearanceStruct{}) {
		out = append(out, bridgedBasicInfoAttrProductAppearance)
	}
	return out
}

// SetReachable updates the reachable flag. Returns true when the
// value changed. Emits the Matter §9.13.6 ReachableChanged event (id
// 0x0003, priority Critical) on the bridge's [interfaces.MatterEventEmitter]
// when it has been wired via [SetMatterEventEmitter] — mirrors
// matter.js HEAD bridged-device-basic-information/Behavior.ts where
// the reachable setter triggers events.reachableChanged.emit. Without
// the event, Apple Home's HAP-service mapper never learns about
// availability flips and silently caches the boot-time value, so
// ongoing CCU drop-outs never surface to the user.
func (b *BridgedDeviceBasicInformation) SetReachable(reachable bool) (changed bool) {
	b.mu.Lock()
	changed = b.reachable != reachable
	b.reachable = reachable
	emitter := b.emitter
	endpoint := b.endpoint
	b.mu.Unlock()
	if changed {
		// Bump DataVersion after a successful Reachable flip so
		// DataVersionFilter evaluation correctly detects the cluster
		// changed. Must happen after the state mutation succeeds.
		b.dataVersion.Bump()
		if emitter != nil {
			// Priority INFO per matter.js HEAD
			// packages/model/src/standard/elements/bridged-device-basic-information.element.ts:55
			// (`Event({ name: "ReachableChanged", id: 0x3, priority: "info" })`).
			// Critical here also starved the event log: a CCU interface
			// flap flips every bridged device at once, and the buffer's
			// critical class is where the boot-once StartUp / BootReason
			// events live.
			emitter.MatterEmitEvent(endpoint, bridgedBasicInfoClusterID,
				bridgedBasicInfoEventReachableChanged,
				ReachableChangedEvent{ReachableNewValue: reachable},
				interfaces.MatterEventPriorityInfo)
		}
	}
	return changed
}

// SetMatterEventEmitter implements [interfaces.MatterEventReceiver].
// Called by the bridge during topology assembly so [SetReachable] can
// fire the §9.13.6 ReachableChanged event without the cluster holding
// a direct reference to the bridge. Idempotent — re-wiring during
// topology rebuild replaces the emitter cleanly.
func (b *BridgedDeviceBasicInformation) SetMatterEventEmitter(emitter interfaces.MatterEventEmitter) {
	b.mu.Lock()
	b.emitter = emitter
	b.mu.Unlock()
}

// SetEndpoint stamps the endpoint id this BDBI server is mounted on.
// Matter events carry the (endpoint, cluster, event) triple so the
// commissioner can fan them out to the right subscription path. The
// endpoint is captured here (not at construction) because the bridge
// assembles the topology after the cluster is built and a single BDBI
// instance is mounted on exactly one endpoint per bridged device.
func (b *BridgedDeviceBasicInformation) SetEndpoint(endpoint uint16) {
	b.mu.Lock()
	b.endpoint = endpoint
	b.mu.Unlock()
}

// Compile-time assertion: BDBI participates in the same emitter wiring
// as GenericSwitch, so the bridge's SetMatterEventEmitter loop in
// bridge.go (the topology-assembly walk) auto-injects the emitter.
var _ interfaces.MatterEventReceiver = (*BridgedDeviceBasicInformation)(nil)
