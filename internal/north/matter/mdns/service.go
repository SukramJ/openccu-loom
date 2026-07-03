// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mdns

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
)

// defaultHostName returns the OS hostname (with any `.local` suffix
// stripped) so a SRV target without an explicit HostName resolves via
// the operating-system mDNS responder's existing A/AAAA records.
// Falls back to "openccu-loom-matter" only when os.Hostname fails.
func defaultHostName() string {
	if h, err := os.Hostname(); err == nil && h != "" {
		return strings.TrimSuffix(h, ".local")
	}
	return "openccu-loom-matter"
}

// Errors.
var (
	// ErrInvalidService is returned when [Service.Validate] rejects
	// a service-record bundle.
	ErrInvalidService = errors.New("mdns: invalid service")
)

// Service is a typed mDNS service record. The Advertiser converts it
// to wire-format DNS messages.
type Service struct {
	// InstanceName is the leftmost label of the SRV / TXT record
	// (e.g. "9C71D38FBE48F2E5-0000000012345678"). 1..63 octets.
	InstanceName string
	// ServiceType is the service-type label
	// ("_matter._tcp" / "_matterc._udp").
	ServiceType string
	// Domain is typically "local". Empty defaults to "local".
	Domain string
	// Port is the operational TCP / commissioning UDP port.
	Port uint16
	// HostName is the bridge's hostname (sans domain) — same value
	// for both service records.
	HostName string
	// Addresses are the local IP addresses the bridge is reachable at.
	// IPv6 first per Matter §4.3.1.5.
	Addresses []net.IP
	// TXT records carry the discovery metadata. Order is not
	// significant — [Service.MarshalTXT] sorts before emit.
	TXT []TXTRecord
	// Subtypes are the additional service-type labels the responder
	// announces (e.g. `_L<long-discriminator>` for commissionable
	// service). Each subtype is the bare label without the
	// `._sub.<service-type>` suffix.
	Subtypes []string
}

// TXTRecord is one (key, value) entry. Matter TXT records use ASCII
// keys with case-insensitive lookup; values are ASCII text.
type TXTRecord struct {
	Key   string
	Value string
}

// Operational + Commissionable service-type labels per Matter §4.3.1.
const (
	ServiceTypeOperational    = "_matter._tcp"
	ServiceTypeCommissionable = "_matterc._udp"
)

// Validate checks the service record for the constraints common to
// every Advertiser implementation.
func (s Service) Validate() error {
	if s.InstanceName == "" {
		return fmt.Errorf("%w: instance name is empty", ErrInvalidService)
	}
	if len(s.InstanceName) > 63 {
		return fmt.Errorf("%w: instance name length=%d (max 63)", ErrInvalidService, len(s.InstanceName))
	}
	if s.ServiceType != ServiceTypeOperational && s.ServiceType != ServiceTypeCommissionable {
		return fmt.Errorf("%w: unknown service type %q", ErrInvalidService, s.ServiceType)
	}
	if s.Port == 0 {
		return fmt.Errorf("%w: port must be non-zero", ErrInvalidService)
	}
	if s.HostName == "" {
		return fmt.Errorf("%w: hostname is empty", ErrInvalidService)
	}
	for i, txt := range s.TXT {
		if txt.Key == "" {
			return fmt.Errorf("%w: TXT[%d] key is empty", ErrInvalidService, i)
		}
		if len(txt.Key)+len(txt.Value)+1 > 255 {
			return fmt.Errorf("%w: TXT[%d] %s=... exceeds 255 bytes", ErrInvalidService, i, txt.Key)
		}
	}
	return nil
}

// MarshalTXT returns the TXT records as `key=value` strings sorted by
// key (case-insensitive). Sorting keeps the wire form deterministic
// across re-emit cycles — convenient for golden-vector tests.
func (s Service) MarshalTXT() []string {
	out := make([]string, len(s.TXT))
	for i, t := range s.TXT {
		out[i] = t.Key + "=" + t.Value
	}
	sort.SliceStable(out, func(i, j int) bool {
		return strings.ToLower(out[i]) < strings.ToLower(out[j])
	})
	return out
}

// FQDN returns the fully-qualified service-instance name in DNS form:
// "<instance>.<service-type>.<domain>.".
func (s Service) FQDN() string {
	domain := s.Domain
	if domain == "" {
		domain = "local"
	}
	return fmt.Sprintf("%s.%s.%s.", s.InstanceName, s.ServiceType, domain)
}

// HostFQDN returns the host's fully-qualified name: "<host>.<domain>.".
func (s Service) HostFQDN() string {
	domain := s.Domain
	if domain == "" {
		domain = "local"
	}
	return fmt.Sprintf("%s.%s.", s.HostName, domain)
}

// OperationalServiceConfig drives [BuildOperationalService].
type OperationalServiceConfig struct {
	// CompressedFabricID is the 8-byte derived fabric identifier
	// (HKDF over the root public key + fabric ID, Matter §4.13.2.4).
	CompressedFabricID [8]byte
	// NodeID is the 64-bit operational node identifier.
	NodeID uint64
	// Port is the TCP listener port (typically 5540 per Matter §4.4).
	Port uint16
	// HostName is the bridge hostname.
	HostName string
	// Addresses are the local IPv6 / IPv4 addresses.
	Addresses []net.IP
	// SessionActiveThreshold is the Matter 1.4 SAT TXT key — the
	// minimum time the node SHOULD stay active after network activity
	// (matter.js packages/protocol/src/session/SessionIntervals.ts:29
	// default 4000 ms). Apple Home + chip-tool's MRP timer reads SAT
	// to size its retry budget; absence collapses retries to a stale
	// 4000 ms hardcode. Zero triggers the matter.js default.
	SessionActiveThreshold uint32
	// SessionIdleInterval / SessionActiveInterval are the MRP idle /
	// active retransmission tuning hints (Matter §4.3.1.6 SII / SAI).
	SessionIdleInterval   uint32
	SessionActiveInterval uint32
	// TCPClient / TCPServer flag bits (Matter 1.5 §4.3.1.6 TCPCommon).
	// 0 = unsupported, 1 = supported (TCPSupport feature).
	TCPClient bool
	TCPServer bool
	// ICD, when non-nil, emits the `ICD` TXT key in the operational
	// record. The bridge is not an ICD so this is always nil for the
	// default deployment; the field is present for completeness and
	// future ICD-proxy support.
	ICD *uint8
}

// BuildOperationalService constructs the operational `_matter._tcp`
// record from the bridge's runtime state.
func BuildOperationalService(cfg OperationalServiceConfig) Service {
	instance := fmt.Sprintf("%s-%016X", strings.ToUpper(hex.EncodeToString(cfg.CompressedFabricID[:])), cfg.NodeID)
	// Matter §4.3.1.7 `T` (TCP Support) bitmap: bit0 is reserved/deprecated,
	// bit1 = TCP client supported, bit2 = TCP server supported. Mirrors chip
	// src/lib/dnssd/Advertiser.h:63-64 (kTCPClient=1<<1, kTCPServer=1<<2).
	tcpFlag := uint8(0)
	if cfg.TCPClient {
		tcpFlag |= 1 << 1
	}
	if cfg.TCPServer {
		tcpFlag |= 1 << 2
	}
	sat := cfg.SessionActiveThreshold
	if sat == 0 {
		sat = defaultSessionActiveThreshold
	}
	// Matter §4.3.1.6: SII (SessionIdleInterval) and SAI
	// (SessionActiveInterval) are the MRP retransmission tuning hints.
	// Apple Home reads them post-CommissioningComplete to size its CASE
	// retry budget; SII=0 / SAI=0 on the wire collapses to a default
	// the controller cannot interpret and the bridge appears
	// "Reachable: NO / Nodeid: (null)" in the iPhone Home app. Mirrors
	// matter.js's per-session SII/SAI emission (NodeSession.ts:147
	// `Duration.format(this.idleInterval)`).
	//
	// The operational defaults differ from the commissionable defaults
	// (SII=5000ms, SAI=300ms). chip (src/lib/dnssd/Advertiser_ImplMinimalMdns.cpp)
	// and matter.js (packages/protocol/src/session/SessionIntervals.ts
	// MATTER_OPERATION_*_DEFAULT) both emit operational SII=500ms / SAI=300ms
	// (the post-CASE MRP-window tuning); the higher 5000ms applies to the
	// commissionable record so battery-backed commissioners can power
	// down between scans.
	sii := cfg.SessionIdleInterval
	if sii == 0 {
		sii = defaultOperationalSII
	}
	sai := cfg.SessionActiveInterval
	if sai == 0 {
		sai = defaultOperationalSAI
	}
	txt := []TXTRecord{
		{Key: "SII", Value: strconv.FormatUint(uint64(sii), 10)},
		{Key: "SAI", Value: strconv.FormatUint(uint64(sai), 10)},
		{Key: "SAT", Value: strconv.FormatUint(uint64(sat), 10)},
		{Key: "T", Value: strconv.FormatUint(uint64(tcpFlag), 10)},
	}
	// ICD operating-mode hint per Matter §4.3.1.6, matter.js
	// MdnsAdvertisement.ts:191-193. Non-ICD bridges omit this key
	// (nil); ICD-proxy support can supply it in future.
	if cfg.ICD != nil {
		txt = append(txt, TXTRecord{Key: "ICD", Value: strconv.FormatUint(uint64(*cfg.ICD), 10)})
	}
	host := cfg.HostName
	if host == "" {
		host = defaultHostName()
	}
	return Service{
		InstanceName: instance,
		ServiceType:  ServiceTypeOperational,
		Port:         cfg.Port,
		HostName:     host,
		Addresses:    append([]net.IP(nil), cfg.Addresses...),
		TXT:          txt,
		// `_I<COMPRESSED-FABRIC-ID>` per Matter §4.3.1.4 + matter.js
		// MdnsConsts.ts:34. Apple Home / chip-tool's operational
		// discovery scans `_I<myCompressedFabric>._sub._matter._tcp.local`
		// to enumerate every node belonging to its fabric — the
		// scan target is the COMPRESSED FABRIC ID, NOT the NodeID.
		// Publishing `_I<NodeID>` makes the freshly-paired bridge
		// invisible to the controller's post-CommissioningComplete
		// reachability probe and triggers RemoveFabric.
		Subtypes: []string{"_I" + strings.ToUpper(hex.EncodeToString(cfg.CompressedFabricID[:]))},
	}
}

// CommissionableServiceConfig drives [BuildCommissionableService].
type CommissionableServiceConfig struct {
	// InstanceID is the 16-byte randomly-generated identifier the
	// bridge advertises while the commissioning window is open.
	// Caller is responsible for cycling it on every window-open.
	InstanceID [8]byte
	// Discriminator is the 12-bit short discriminator (Matter
	// §4.3.1.5.1). Embedded in the `_S<short>` subtype.
	Discriminator uint16
	// VendorID is the bridge's IANA-assigned vendor identifier.
	VendorID uint16
	// ProductID is the vendor-assigned product identifier.
	ProductID uint16
	// CommissioningMode per Matter §4.3.1.6 (`CM` TXT key):
	//   0 = not in commissioning mode (should never appear on this
	//       service — the service is only emitted while open),
	//   1 = standard commissioning,
	//   2 = enhanced commissioning.
	CommissioningMode uint8
	// DeviceTypeID is the bridge's primary device type for UI hint
	// (`DT` TXT key). Aggregator = 0x000E.
	DeviceTypeID uint32
	// DeviceName is the operator-visible name (`DN` TXT key, max 32
	// utf-8 bytes).
	DeviceName string
	// PairingHint / PairingInstruction follow Matter §4.3.1.6 (`PH`
	// / `PI`). PairingHint defaults to
	// [PairingHintDefault] (powerCycle | deviceManual = 0x21) when
	// zero, matching matter.js
	// CommissionableMdnsAdvertisement.ts:90 DEFAULT_PAIRING_HINT.
	PairingHint        uint16
	PairingInstruction string
	// RotatingID is the optional Rotating Device Identifier (Matter
	// §4.3.1.6 `RI` TXT key). When non-empty, emitted verbatim so
	// controllers that use the rotating ID for "already added?"
	// checks can correlate devices.
	// matter.js CommissionableMdnsAdvertisement.ts (Scanner.ts:38
	// `RI?: string`) and chip
	// Advertiser_ImplMinimalMdns.cpp:878-881 both emit RI only when
	// the platform supplies it; openccu-loom does the same.
	RotatingID string
	// ICD, when non-nil, emits the `ICD` TXT key with the given
	// operating-mode value per Matter §4.3.1.6.
	// matter.js MdnsAdvertisement.ts:191-193; chip TxtFields.cpp:302.
	ICD *uint8
	// Port is the commissioning listener UDP port (typically 5540).
	Port uint16
	// HostName / Addresses mirror OperationalServiceConfig.
	HostName  string
	Addresses []net.IP
	// SessionIdleInterval / SessionActiveInterval are the MRP tuning
	// hints added to the commissioning TXT record in Matter 1.4
	// (`SII` / `SAI` keys per §4.3.1.6 and matter.js
	// packages/protocol/src/session/SessionIntervals.ts). The spec
	// recommends SII=5000 ms, SAI=300 ms as default values. Zero
	// triggers the defaults.
	SessionIdleInterval   uint32
	SessionActiveInterval uint32
	// SessionActiveThreshold is the SAT TXT key (Matter 1.4) — see
	// the operational config; default 4000 ms when zero.
	SessionActiveThreshold uint32
}

// PairingHintDefault is the bitmask openccu-loom emits for PH when the
// caller leaves PairingHint zero. Mirrors matter.js
// packages/protocol/src/mdns/MdnsConsts.ts:15-18 DEFAULT_PAIRING_HINT:
// { powerCycle: true, deviceManual: true }. Per
// packages/protocol/src/advertisement/PairingHintBitmap.ts, powerCycle
// is bit 0 (0x01) and deviceManual is bit 5 (0x20) of the Matter
// §4.3.1.6 PairingHintBitmap — combined value 0x21. Bit 4
// (customInstruction) is NOT part of the default: it requires a PI
// (custom instruction) value the bridge never supplies, so setting it
// without one is non-conformant.
const PairingHintDefault uint16 = 0x21

// defaultCommissionableSII is the Session Idle Interval in milliseconds
// advertised on the commissionable record (Matter §4.3.1.6). matter.js
// uses the same idle interval for every advertisement — 500 ms per
// packages/protocol/src/session/SessionIntervals.ts
// (SessionIntervals.defaults.idleInterval = Millis(500)); there is no
// separate 5000 ms commissioning default. A too-large SII makes
// commissioners space their PASE retransmits ~10× too slowly on a lossy
// link, slowing pairing.
const defaultCommissionableSII uint32 = 500

// defaultCommissionableSAI is the recommended Session Active Interval
// in milliseconds per Matter §4.3.1.6 and matter.js
// packages/protocol/src/session/SessionIntervals.ts MATTER_COMMISSION_SAI_DEFAULT.
const defaultCommissionableSAI uint32 = 300

// defaultOperationalSII is the recommended Session Idle Interval for
// the operational `_matter._tcp` record (Matter §4.3.1.6). chip emits
// 500 ms (src/lib/dnssd/Advertiser_ImplMinimalMdns.cpp) and matter.js
// emits 500 ms (packages/protocol/src/session/SessionIntervals.ts
// MATTER_OPERATION_SII_DEFAULT). The operational value is tighter than
// the commissionable 5000 ms because post-CASE traffic is bounded by
// MRP retransmit windows, not battery-backed commissioner scans.
const defaultOperationalSII uint32 = 500

// defaultOperationalSAI is the recommended Session Active Interval for
// the operational record (matter.js packages/protocol/src/session/SessionIntervals.ts
// MATTER_OPERATION_SAI_DEFAULT, chip Advertiser_ImplMinimalMdns.cpp).
const defaultOperationalSAI uint32 = 300

// defaultSessionActiveThreshold is the recommended Session Active
// Threshold (matter.js packages/protocol/src/session/
// SessionIntervals.ts:29 default 4000 ms). The value is the minimum
// time a node SHOULD stay active after network activity; controllers
// read SAT to size their MRP retry budget.
const defaultSessionActiveThreshold uint32 = 4000

// BuildCommissionableService constructs the `_matterc._udp` record.
// Subtypes:
//   - `_L<long>`         long discriminator  (always)
//   - `_S<short>`        short discriminator (always)
//   - `_V<vendor-id>`    vendor filter       (always)
//   - `_CM`              commissioning-mode  (per Matter §4.3.1.5.3)
//   - `_T<deviceTypeID>` device-type filter  (per Matter §4.3.1.5.4)
//
// TXT keys include `SII` and `SAI` per §4.3.1.6 and matter.js
// packages/protocol/src/session/SessionIntervals.ts defaults.
func BuildCommissionableService(cfg CommissionableServiceConfig) Service {
	instance := strings.ToUpper(hex.EncodeToString(cfg.InstanceID[:]))
	long := cfg.Discriminator
	short := (cfg.Discriminator >> 8) & 0x0F

	sii := cfg.SessionIdleInterval
	if sii == 0 {
		sii = defaultCommissionableSII
	}
	sai := cfg.SessionActiveInterval
	if sai == 0 {
		sai = defaultCommissionableSAI
	}
	sat := cfg.SessionActiveThreshold
	if sat == 0 {
		sat = defaultSessionActiveThreshold
	}

	txt := []TXTRecord{
		{Key: "D", Value: strconv.FormatUint(uint64(long), 10)},
		{Key: "CM", Value: strconv.FormatUint(uint64(cfg.CommissioningMode), 10)},
		{Key: "DT", Value: strconv.FormatUint(uint64(cfg.DeviceTypeID), 10)},
		// SII / SAI / SAT on the commissionable record so commissioners
		// can tune MRP timers before the operational session is up.
		// Mirrors matter.js packages/protocol/src/session/SessionIntervals.ts defaults.
		{Key: "SII", Value: strconv.FormatUint(uint64(sii), 10)},
		{Key: "SAI", Value: strconv.FormatUint(uint64(sai), 10)},
		{Key: "SAT", Value: strconv.FormatUint(uint64(sat), 10)},
	}
	// VP (Vendor+Product) only when a vendor id is set: VendorID 0 is
	// reserved/invalid, so "VP=0+0" is non-conformant. chip emits the
	// vendor-only form when ProductID is absent and omits VP entirely when
	// no vendor (Advertiser_ImplMinimalMdns.cpp AddCommonTxtEntries).
	if cfg.VendorID != 0 {
		vp := strconv.FormatUint(uint64(cfg.VendorID), 10)
		if cfg.ProductID != 0 {
			vp = fmt.Sprintf("%d+%d", cfg.VendorID, cfg.ProductID)
		}
		txt = append(txt, TXTRecord{Key: "VP", Value: vp})
	}
	if cfg.DeviceName != "" {
		txt = append(txt, TXTRecord{Key: "DN", Value: cfg.DeviceName})
	}
	// PH (PairingHint): always emitted per matter.js
	// CommissionableMdnsAdvertisement.ts:90 — DEFAULT_PAIRING_HINT
	// (powerCycle=true, deviceManual=true → 0x21) used when the
	// caller leaves PairingHint zero.
	ph := cfg.PairingHint
	if ph == 0 {
		ph = PairingHintDefault
	}
	txt = append(txt, TXTRecord{Key: "PH", Value: strconv.FormatUint(uint64(ph), 10)})
	if cfg.PairingInstruction != "" {
		txt = append(txt, TXTRecord{Key: "PI", Value: cfg.PairingInstruction})
	}
	// RI (Rotating Device Identifier) per Matter §4.3.1.6, matter.js
	// Scanner.ts:38 `RI?: string` — emitted only when the platform
	// supplies it. Absent on a non-RI bridge; present once a rotating
	// ID generator is wired.
	if cfg.RotatingID != "" {
		txt = append(txt, TXTRecord{Key: "RI", Value: cfg.RotatingID})
	}
	// ICD operating-mode hint per Matter §4.3.1.6.
	if cfg.ICD != nil {
		txt = append(txt, TXTRecord{Key: "ICD", Value: strconv.FormatUint(uint64(*cfg.ICD), 10)})
	}
	subtypes := []string{
		fmt.Sprintf("_L%d", long),
		fmt.Sprintf("_S%d", short),
		// _CM._sub._matterc._udp.local — commissioners filtering by
		// commissioning-mode browse for this subtype per Matter §4.3.1.5.3.
		// Mirrors matter.js packages/protocol/src/mdns/MdnsServer.ts subtype logic.
		"_CM",
		// _T<deviceTypeID>._sub._matterc._udp.local — device-type subtype
		// per Matter §4.3.1.5.4; lets commissioners pre-filter bridge vs.
		// sensor vs. plug. Mirrors matter.js packages/protocol/src/mdns/MdnsServer.ts.
		fmt.Sprintf("_T%d", cfg.DeviceTypeID),
	}
	// _V<vendor> vendor-filter subtype only when a vendor id is set
	// (consistent with the VP TXT gating above).
	if cfg.VendorID != 0 {
		subtypes = append(subtypes, fmt.Sprintf("_V%d", cfg.VendorID))
	}
	host := cfg.HostName
	if host == "" {
		host = defaultHostName()
	}
	return Service{
		InstanceName: instance,
		ServiceType:  ServiceTypeCommissionable,
		Port:         cfg.Port,
		HostName:     host,
		Addresses:    append([]net.IP(nil), cfg.Addresses...),
		TXT:          txt,
		Subtypes:     subtypes,
	}
}
