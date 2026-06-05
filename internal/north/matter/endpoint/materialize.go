// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package endpoint

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/SukramJ/openccu-loom/internal/north/matter/bootid"
	mattercore "github.com/SukramJ/openccu-loom/internal/north/matter/cluster/core"
	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/measurement"
	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/wire"
	"github.com/SukramJ/openccu-loom/internal/north/matter/store"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// matterDeviceTypeBridgedNode is the Matter Device Type ID for the
// BridgedNode auxiliary type per Matter Application Cluster Spec §1.4.
// Every bridged endpoint's DeviceTypeList must include this entry in
// addition to the device's primary type so Apple Home / Google Home
// recognise the endpoint as part of a Matter Bridge — without it,
// Apple's iCloud-Heim sync rejects the topology after Subscribe.
const matterDeviceTypeBridgedNode uint32 = 0x0013

// ClusterServers returns the dispatchable cluster servers for ep —
// the slice the bridge attaches to its TLV dispatcher for this
// endpoint. Three paths feed the result:
//
//  1. Custom-DP-backed endpoints (ep.Source != nil) ask the source
//     directly via [interfaces.MatterEndpointSource.MatterClusterServers].
//  2. MomentarySwitch sources (ep.Measurement is a
//     [wire.GenericSwitchSource]) get a [wire.GenericSwitch] cluster
//     server constructed at materialise time so the cluster knows the
//     endpoint ID for event emission.
//  3. Other standalone-sensor endpoints (ep.Measurement != nil)
//     consult the [measurement.FromMeasurementClass] materializer,
//     which maps the measurement class enum to a concrete read-only
//     cluster server wired against the source DP.
//
// The root endpoint (ep.IsRoot()) returns the slice the daemon
// attached via [Endpoint.RootClusterServers] (BasicInformation,
// GeneralCommissioning, OperationalCredentials, …). Bridged
// endpoints without either field set return nil.
//
// Result is a fresh slice on every call; callers may mutate or
// append without affecting subsequent invocations.
func ClusterServers(ep *Endpoint) []interfaces.MatterClusterServer { //nolint:funlen // wire/dispatch table over many attribute/opcode cases
	if ep == nil {
		return nil
	}
	if ep.IsRoot() {
		return append([]interfaces.MatterClusterServer(nil), ep.RootClusterServers...)
	}
	if ep.IsAggregator() {
		return append([]interfaces.MatterClusterServer(nil), ep.AggregatorClusterServers...)
	}

	// Source / measurement-driven cluster servers come first so the
	// dispatcher's wildcard read enumerates them in the same order
	// across calls.
	var inner []interfaces.MatterClusterServer
	switch {
	case ep.Source != nil:
		inner = append([]interfaces.MatterClusterServer(nil), ep.Source.MatterClusterServers()...)
	case ep.Measurement != nil:
		class := ep.Measurement.MatterMeasurementClass()
		// MomentarySwitch is event-driven and needs the endpoint ID
		// at construction time so [wire.GenericSwitch.FireInitialPress]
		// can address the right Matter address.
		if class == interfaces.MatterMeasurementMomentarySwitch {
			if src, ok := ep.Measurement.(wire.GenericSwitchSource); ok {
				inner = []interfaces.MatterClusterServer{wire.NewGenericSwitch(ep.ID, src)}
			}
		} else {
			inner = measurement.FromMeasurementClass(class, ep.Measurement)
			// PowerSource.EndpointList (§11.7.6.20) must name the endpoint
			// the power source feeds; stamp it post-construction, like the
			// other endpoint-aware servers (BasicInformation, …).
			for _, s := range inner {
				if ps, ok := s.(*measurement.PowerSourceServer); ok {
					ps.SetEndpoint(ep.ID)
				}
			}
		}
	}
	if len(inner) == 0 {
		// Defensive: a bridged endpoint with neither source nor
		// measurement contributes no cluster surface; the topology
		// would be malformed but we must not panic mid-dispatch.
		return nil
	}

	// Spec §9.5 + §9.13: every bridged endpoint MUST advertise both
	// the Descriptor cluster (DeviceTypeList containing BridgedNode +
	// the primary type, plus a populated ServerList) and the
	// BridgedDeviceBasicInformation cluster (NodeLabel + Reachable +
	// UniqueID). Apple Home's HMMTRAccessoryServerBrowser builds its
	// "topology dictionary" from these two clusters alone — without
	// them Subscribe still works but Apple aborts pairing with
	// HMMTRErrorDomain Code 9 ("no topology / no link layer") ~9 s
	// after CommissioningComplete.
	// Apple Home's HMMTRAccessoryPairingStep_BuildingHAPServicesAndCharacteristicsFromCHIP
	// requires the full BridgedDeviceBasicInformation surface — empty
	// fields surface as HAPErrorDomain Code 24 ("Failed to rebuild HAP
	// services of CHIP Accessory") and Apple aborts the pair after the
	// topology rebuild step.
	//
	// matter.js's home-assistant-matter-bridge fills the cluster from
	// the HA device registry (BasicInformationServer.update at
	// packages/backend/src/matter/behaviors/basic-information-server.ts):
	//
	//   vendorId, vendorName, productName, productLabel,
	//   hardwareVersion, softwareVersion, hardwareVersionString,
	//   softwareVersionString, nodeLabel, reachable, serialNumber
	//
	// SerialNumber surfaces the human-readable hardware address (HmIP/
	// BidCos device serial, e.g. "0001D8A991F2DC"). UniqueID stays the
	// opaque 32-char hash. Keeping the two distinct mirrors matter.js's
	// `basic-information-validators.ts:26` invariant — that validator
	// warns when `uniqueId === serialNumber`, and Apple Home's HAP-
	// service mapper appears to treat the equality as a duplicate-
	// accessory signal: MTRDevice tears the CASE session down ~10 s
	// after Subscribe-Initial, and the bridge surfaces as "added but
	// not supported". With the address as SerialNumber, the two fields
	// carry orthogonal information (physical hardware ID vs. stable
	// fabric-level UID) the way matter.js's own bridged-device pattern
	// uses them.
	productName := ""
	address := ""
	if ep.BridgedDevice != nil {
		address = ep.BridgedDevice.Address
		productName = address
	}
	if productName == "" {
		productName = "openccu-loom Bridged Device"
	}
	// Minimal bridged-endpoint surface matching matter.js's
	// `examples/device-bridge-onoff/src/BridgedDevicesNode.ts:91-99`:
	// only `nodeLabel`, `productName`, `productLabel`, `serialNumber`,
	// `reachable`, `uniqueId` are set. VendorName, VendorID, ProductID,
	// HardwareVersion, HardwareVersionStr, SoftwareVersion,
	// SoftwareVersionStr are optional and matter.js Sample's
	// Apple-successful pair byte-dump confirms they are
	// NOT on the wire. A previous P5-attempt (Run 7) dropped these and
	// regressed `HMAccessory.Reachable` to NO — that run was missing
	// the P10 StartUp/BootReason event emit; with both in place the
	// minimal surface matches matter.js's behaviour byte-for-byte.
	// VendorID / ProductID default to the CSA Test pair (0xFFF1 /
	// 0x8001) so the dev workflow keeps producing Apple-pair-viable
	// bridged endpoints out-of-the-box. An operator-supplied
	// production pair propagates via [Endpoint.BridgeVendorID] /
	// [Endpoint.BridgeProductID], which the assembler copies from
	// [Topology.VendorID] / [Topology.ProductID] onto every bridged
	// endpoint at build time.
	vendorID := ep.BridgeVendorID
	if vendorID == 0 {
		vendorID = 0xFFF1
	}
	productID := ep.BridgeProductID
	if productID == 0 {
		productID = 0x8001
	}
	// Reachable is read LIVE from the underlying CCU device on every
	// materialise call (cluster servers are reconstructed per dispatch,
	// so each Read self-heals to the current availability). When the CCU
	// device drops — interface circuit-breaker open, STICKY_UNREACH —
	// `dev.Available()` returns false and Apple/Google see the bridged
	// device as unreachable. The bridge separately fires the §9.13.6
	// ReachableChanged event on the flip (see Bridge.NotifyDeviceReachable)
	// so subscribed commissioners learn about it without re-reading.
	//
	// At pair time a transient unreachability can still abort Apple's HAP
	// rebuild (HAPErrorDomain Code=14); the assembler captures the
	// availability snapshot at topology-build time into ep.Reachable, and
	// a healthy boot reports true. We prefer the live value here because a
	// permanently-dead device must NOT advertise Reachable=true forever —
	// that was the dormant bug this wiring closes.
	reachable := true
	if ep.BridgedDevice != nil {
		reachable = ep.BridgedDevice.Available()
	}
	bridged, err := mattercore.NewBridgedDeviceBasicInformation(mattercore.BridgedConfig{
		// Apple's HMHome layer successfully projects HMCharacteristic values
		// from the MTRDevice cache only when the bridged endpoint carries
		// non-empty VendorName + ProductName + VendorID + ProductID.
		// matter.js's Sample-Bridge sets these to Test-Vendor values and
		// Apple's iPad shows sensors as Reachable+Controllable=YES. We set
		// them analogously plus the HmIP manufacturer "eQ-3" + the
		// device-specific ProductName (= Address for unique per-endpoint
		// identification). Without these fields Apple shows the sensors
		// in the Home app as "not available".
		VendorName:   "eQ-3",
		VendorID:     vendorID,
		ProductName:  productName,
		ProductID:    productID,
		ProductLabel: productName,
		SerialNumber: address,
		NodeLabel:    ep.FriendlyName,
		UniqueID:     uniqueIDFor(ep.SourceKey),
		// Reachable mirrors the underlying CCU device's live availability
		// (see the `reachable` derivation above). When the device is dead
		// the bridged endpoint now correctly advertises Reachable=false;
		// the bridge fires the §9.13.6 ReachableChanged event on each flip
		// via Bridge.NotifyDeviceReachable so commissioners are notified.
		Reachable: reachable,
	})
	if err != nil {
		// NodeLabel / UniqueID validation fail safely — return inner
		// only; Apple will still see the cluster surface but reject
		// the topology, which is the same outcome as before this fix.
		return append([]interfaces.MatterClusterServer(nil), inner...)
	}
	// Stamp the endpoint id so SetReachable can address the §9.13.6
	// ReachableChanged event to the right (endpoint, cluster, event)
	// triple. Without this the event would fire at endpoint 0 and the
	// commissioner's subscription path mismatch would silently swallow
	// it (subscription paths are scoped per endpoint per matter.js
	// SubscriptionHandler.ts).
	bridged.SetEndpoint(ep.ID)

	// Per-DeviceType revisions extracted from matter.js's
	// `packages/model/src/standard/elements/*.element.ts`. Apple Home's
	// HAP service mapper validates the revision against its internal
	// schema-version table; a mismatched revision (we previously
	// hard-coded 1 everywhere) makes Apple log "Attribute report
	// <private> is not parsed into a known struct" + "No Endpoints In
	// Use at endpoint 0" and abort with HAPErrorDomain Code=14.
	// BridgedNode revision is sourced from the codegen'd schema table
	// (matter.js HEAD `bridged-node.element.ts`, currently 3). Using the
	// lookup keeps the revision in lock-step with the next
	// `make generate-matter-schema` run; a hardcoded constant would
	// silently drift when matter.js bumps. Mirrors matter.js's
	// `BridgedNodeDt.revision` indirection via `@matter/model`.
	// DeviceTypeList order: PRIMARY device type FIRST, BridgedNode
	// SECONDARY. Apple Home's HAP-service mapper iterates the list and
	// keys its HMService lookup on the *first* entry — BridgedNode (19)
	// has no HAP-service mapping, so a list starting with BridgedNode
	// makes Apple drop the endpoint into the fallback "Other" category
	// and render the bridge as "not supported". matter.js's bridge
	// sample (`examples/device-bridge-onoff/src/BridgedDevicesNode.ts`
	// + decoder dump of a successful Apple pair) emits
	// `DeviceTypeList=[{OnOffLight(0x0100) rev3}, {BridgedNode(19) rev3}]`
	// — primary first, BridgedNode last. Mirror that order.
	bridgedRev := deviceTypeRevision(uint16(matterDeviceTypeBridgedNode))
	deviceTypes := make([]mattercore.DeviceTypeStruct, 0, 2)
	if ep.DeviceType != 0 {
		deviceTypes = append(deviceTypes, mattercore.DeviceTypeStruct{
			DeviceType: uint32(ep.DeviceType),
			Revision:   deviceTypeRevision(ep.DeviceType),
		})
	}
	deviceTypes = append(deviceTypes, mattercore.DeviceTypeStruct{
		DeviceType: matterDeviceTypeBridgedNode, Revision: bridgedRev,
	})

	// Spec §1.4: Identify (0x0003) is mandatory on every endpoint other
	// than Root and NetworkCommissioning. Apple Home's HAP-service
	// rebuild step uses the cluster's presence as a structural gate —
	// without Identify in ServerList the pair fails with HAPErrorDomain
	// Code=24 ("Failed to rebuild HAP services") regardless of how
	// complete the rest of the cluster surface is. matter.js mirrors
	// this: every device-type definition under
	// `packages/node/src/devices/*.ts` lists Identify in the mandatory
	// cluster set (verified for contact-sensor, temperature-sensor,
	// occupancy-sensor, light-sensor, pressure-sensor, humidity-sensor,
	// air-quality-sensor, water-leak-detector, generic-switch,
	// window-covering, on-off-plug-in-unit). Mounting Identify here, in
	// front of the source / measurement clusters, keeps it discoverable
	// at the lowest cluster ID and ensures the ServerList builder picks
	// it up automatically.
	identify := mattercore.NewIdentify()

	clusters := make([]interfaces.MatterClusterServer, 0, len(inner)+1)
	clusters = append(clusters, identify)
	clusters = append(clusters, inner...)

	// Pass nil serverList — the provider closure below derives it from
	// the final mounted set so the advertised ServerList cannot drift
	// from the clusters actually returned (mirrors the Root / Aggregator
	// pattern in daemon.go). A static buildServerList call here would
	// silently drift whenever clusters are added before or after the
	// Descriptor construction.
	descriptor, derr := mattercore.NewDescriptor(deviceTypes, nil, nil, nil)
	if derr != nil {
		return append(clusters, bridged)
	}

	out := make([]interfaces.MatterClusterServer, 0, len(clusters)+2)
	out = append(out, clusters...)
	out = append(out, descriptor, bridged)

	// Install the server-list provider after `out` is fully assembled so
	// the closure captures the final, immutable slice. Every ServerList
	// read returns the IDs of the clusters actually mounted on this
	// endpoint — no duplicates, deterministic order.
	mounted := out
	descriptor.SetServerListProvider(func() []uint32 {
		ids := make([]uint32, 0, len(mounted))
		seen := make(map[uint32]struct{}, len(mounted))
		for _, srv := range mounted {
			if srv == nil {
				continue
			}
			id := srv.MatterClusterID()
			if _, dup := seen[id]; dup {
				continue
			}
			seen[id] = struct{}{}
			ids = append(ids, id)
		}
		return ids
	})
	return out
}

// uniqueIDFor derives a stable 32-character hex string from the
// endpoint's source key. Matter §9.13.5.20 requires a persistent,
// per-device identifier; Apple Home's HAP service mapper additionally
// requires distinct UniqueID values across bridged endpoints — duplicate
// fingerprints make the mapper collapse the topology into a single
// HMAccessory entry and abort pair after Subscribe-Initial.
//
// Source order:
//
//  1. Concrete [store.EndpointKey] — render the full 5-tuple
//     (CentralName + DeviceAddress + ChannelNo + DPKind + DPKey).
//  2. A type that implements `Stringer` — use its String() form.
//  3. A reflective fallback that walks any exported fields via
//     fmt.Sprintf("%+v", key) so non-EndpointKey shapes still produce
//     a deterministic, key-distinguishing string.
//  4. Last-ditch literal "openccu-loom-bridged-<addr>" — only reached
//     when key is nil; never fall back to a single literal because
//     duplicates fail Apple Home pair (verified against the
//     in-tree decoder; all five bridged endpoints had collapsed onto
//     SHA256("openccu-loom-bridged") = 8fb64448e9...).
func uniqueIDFor(key any) string {
	s := renderSourceKey(key)
	if s == "" {
		// Nil / un-renderable key — guarantee uniqueness via the key
		// pointer address so Apple does not collapse the endpoint into
		// a duplicate HMAccessory.
		s = fmt.Sprintf("openccu-loom-bridged-%p", key)
	}
	// Mix the per-boot salt in so Apple's HAP cache sees a fresh
	// fingerprint after every daemon restart; see package bootid.
	salt := bootid.Salt()
	salted := append([]byte(nil), salt[:]...)
	salted = append(salted, '|')
	salted = append(salted, []byte(s)...)
	sum := sha256.Sum256(salted)
	return hex.EncodeToString(sum[:16])
}

// renderSourceKey produces a deterministic, key-distinguishing string
// representation. The concrete [store.EndpointKey] is checked first so
// every field is included — the previous implementation relied on a
// `Central()/Address()` interface that EndpointKey does not satisfy,
// so every endpoint fell through to a literal default and Apple Home
// rejected the duplicate-fingerprint topology.
func renderSourceKey(key any) string {
	switch k := key.(type) {
	case nil:
		return ""
	case store.EndpointKey:
		return fmt.Sprintf("%s|%s|%d|%s|%s",
			k.CentralName, k.DeviceAddress, k.ChannelNo, k.DPKind, k.DPKey)
	case *store.EndpointKey:
		if k == nil {
			return ""
		}
		return fmt.Sprintf("%s|%s|%d|%s|%s",
			k.CentralName, k.DeviceAddress, k.ChannelNo, k.DPKind, k.DPKey)
	case interface {
		Central() string
		Address() string
	}:
		return k.Central() + ":" + k.Address()
	case fmt.Stringer:
		return k.String()
	}
	// Reflective fallback for shapes the type switch does not cover —
	// fmt's %+v walks every exported field, which is exactly what the
	// hash needs.
	s := strings.TrimSpace(fmt.Sprintf("%+v", key))
	if s == "" || s == "{}" || s == "<nil>" {
		return ""
	}
	return s
}
