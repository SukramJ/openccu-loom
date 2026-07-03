// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package wire

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// AdministratorCommissioning implements Matter cluster 0x003C per
// Matter Core Specification 1.5.1 §11.19. The cluster surfaces the
// "open commissioning window" command-set Matter controllers use to
// share a fabric with another commissioner.
//
// v1.1 implementation: read-only attribute surface mirrors the
// bridge's current PASE state; commands route to a programmatic
// [WindowController] hook the daemon plumbs in (typically
// `bridge.Bridge.OpenCommissioningWindow`). When no controller is
// wired the commands return Status `BUSY` so commissioners can
// gracefully retry.
//
// Mandatory attributes (Matter §11.19.5):
//
//   - 0x0000 WindowStatus      (CommissioningWindowStatus enum, mandatory)
//   - 0x0001 AdminFabricIndex  (FabricIndex, nullable, mandatory)
//   - 0x0002 AdminVendorId     (VendorId, nullable, mandatory)
//   - 0xFFFC FeatureMap        (uint32; v1.1 advertises 0 — no
//     basic-commissioning-method support yet)
//   - 0xFFFD ClusterRevision   (uint16 = 1)
//
// Commands (Matter §11.19.8):
//
//   - 0x00 OpenCommissioningWindow      (enhanced; verifier-driven)
//   - 0x01 OpenBasicCommissioningWindow (BC feature; not in v1.1
//     FeatureMap, returns UnsupportedCommand)
//   - 0x02 RevokeCommissioning          (no fields)
const (
	matterClusterAdminCommissioning uint32 = 0x003C

	matterAttrAdmCommWindowStatus    uint32 = 0x0000
	matterAttrAdmCommAdminFabric     uint32 = 0x0001
	matterAttrAdmCommAdminVendorID   uint32 = 0x0002
	matterAttrAdmCommFeatureMap      uint32 = 0xFFFC
	matterAttrAdmCommClusterRevision uint32 = 0xFFFD

	matterCmdAdmCommOpenWindow      uint32 = 0x00
	matterCmdAdmCommOpenBasicWindow uint32 = 0x01
	matterCmdAdmCommRevoke          uint32 = 0x02

	admCommClusterRevision uint16 = 1
)

// CommissioningWindowStatus mirrors the Matter §11.19.6.1 enum.
type CommissioningWindowStatus uint8

// CommissioningWindowStatus values.
const (
	// WindowStatusClosed — no commissioning window open.
	WindowStatusClosed CommissioningWindowStatus = 0
	// WindowStatusEnhanced — enhanced (verifier-driven) window open.
	WindowStatusEnhanced CommissioningWindowStatus = 1
	// WindowStatusBasic — basic-commissioning-method window open.
	WindowStatusBasic CommissioningWindowStatus = 2
)

// WindowController is the bridge-side surface
// AdministratorCommissioning routes its commands through. The daemon
// wires this to [bridge.Bridge.OpenCommissioningWindow] — the
// cluster server itself owns no PASE / verifier state.
//
// CurrentWindow returns the live status the cluster reports back via
// [AdministratorCommissioning.MatterRead]. Open / Revoke drive the
// state transition; both return spec-mapped Status codes (encoded as
// errors here — the bridge's invoke dispatcher converts them to the
// Matter wire status).
type WindowController interface {
	CurrentWindow() WindowStatusSnapshot
	OpenWindow(ctx context.Context, params OpenWindowParams) error
	RevokeWindow(ctx context.Context) error
}

// WindowStatusSnapshot captures every attribute the cluster's read
// side surfaces. AdminFabricIsNull / AdminVendorIsNull encode "not
// applicable" (Matter NULL TLV) — controllers expect both to be null
// while no window is open or the window was opened by a non-fabric
// (commissioning-mode) caller.
type WindowStatusSnapshot struct {
	Status            CommissioningWindowStatus
	AdminFabricIndex  uint8
	AdminFabricIsNull bool
	AdminVendorID     uint16
	AdminVendorIsNull bool
}

// OpenWindowParams are the fields decoded from the §11.19.8.1
// OpenCommissioningWindow command. The cluster passes them through
// verbatim; the bridge implementation owns verifier validation +
// timeout scheduling.
//
// AdminFabricIndex / AdminVendorID are populated by the cluster server
// from the IM dispatcher's fabric context so the bridge can reflect
// them back via [AdministratorCommissioning.MatterRead]. Mirrors
// matter.js AdministratorCommissioningServer.ts:176-180
// `this.state.adminFabricIndex = adminFabric.fabricIndex`.
type OpenWindowParams struct {
	CommissioningTimeoutSeconds uint16
	PAKEPasscodeVerifier        []byte
	Discriminator               uint16
	Iterations                  uint32
	Salt                        []byte
	// AdminFabricIndex is the FabricIndex of the admin that opened the
	// window, derived from im.FabricFilterFromContext in MatterInvoke.
	// Zero means "not set" (e.g. opened before commissioning).
	AdminFabricIndex uint8
	// AdminVendorID is the vendor ID of the admin fabric, derived from
	// the fabric's rootVendorId. Zero when not set.
	AdminVendorID uint16
	// IsBasicWindow indicates the window was opened via
	// OpenBasicCommissioningWindow (BC feature). When false (default)
	// the window is Enhanced (verifier-based).
	IsBasicWindow bool
	// IsUncommissioned, when true, indicates the bridge has no fabrics
	// installed yet (FabricCount == 0). The timeout upper bound extends
	// from 900 s to 172 800 s (48 h) per Matter §11.19.8.1 — matching
	// chip CommissioningWindowManager.cpp::MaxCommissioningTimeout().
	// The daemon sets this field from the fabric store before calling
	// OpenWindow. Defaults to false (commissioned), which applies the
	// standard 900-s cap.
	IsUncommissioned bool
}

// AdministratorCommissioning is the cluster server.
type AdministratorCommissioning struct {
	mu             sync.RWMutex
	controller     WindowController
	vendorResolver VendorIDResolver
	fabricCounter  FabricCounter
	// isFailSafeArmed is an optional hook the daemon wires from
	// GeneralCommissioning.FailSafeArmed. When non-nil it is called
	// before OpenCommissioningWindow to enforce the "FailSafe must be
	// fully disarmed" pre-condition per chip
	// AdministratorCommissioningCluster.cpp OpenCommissioningWindow
	// VerifyOrExit(IsFailSafeFullyDisarmed, ...).
	isFailSafeArmed func() bool
}

// VendorIDResolver returns the rootVendorId of a fabric. The daemon
// wires this to the fabric store so a Multi-Admin OpenCommissioningWindow
// over CASE reflects the calling admin's VendorID in AdminVendorId per
// matter.js AdministratorCommissioningServer.ts:176-180
// (`adminFabric.rootVendorId`). Return 0 if the fabric is unknown.
type VendorIDResolver func(ctx context.Context, fabricIndex uint8) uint16

// FabricCounter is an optional hook the daemon wires to the fabric store
// so AdministratorCommissioning can determine whether the bridge has any
// fabrics installed before opening a commissioning window. When
// FabricCount returns 0 the bridge is uncommissioned and the 48-h timeout
// upper bound applies per Matter §11.19.8.1.
type FabricCounter func(ctx context.Context) (int, error)

// NewAdministratorCommissioning constructs the cluster with no
// controller wired — every command returns Status BUSY until the
// daemon plumbs a [WindowController] in via [SetController].
func NewAdministratorCommissioning() *AdministratorCommissioning {
	return &AdministratorCommissioning{}
}

// SetController wires the bridge-side window controller. Pass nil to
// detach (commands then return BUSY).
func (a *AdministratorCommissioning) SetController(c WindowController) {
	a.mu.Lock()
	a.controller = c
	a.mu.Unlock()
}

// SetVendorIDResolver wires the fabric-store-backed VendorID lookup so
// Multi-Admin OpenCommissioningWindow invocations over CASE populate
// AdminVendorId from the calling admin's fabric record. Pass nil to
// detach (AdminVendorId then stays null in that path).
func (a *AdministratorCommissioning) SetVendorIDResolver(r VendorIDResolver) {
	a.mu.Lock()
	a.vendorResolver = r
	a.mu.Unlock()
}

// SetFabricCounter wires the fabric-store FabricCount query used to
// determine whether the bridge is uncommissioned before opening a window.
// Pass nil to detach (FabricCount then defaults to 1, applying the 900-s
// cap, which is the safe fallback for a bridge that may already be paired).
func (a *AdministratorCommissioning) SetFabricCounter(fc FabricCounter) {
	a.mu.Lock()
	a.fabricCounter = fc
	a.mu.Unlock()
}

// SetIsFailSafeArmed wires the FailSafe-armed accessor from
// GeneralCommissioning.FailSafeArmed so OpenCommissioningWindow can
// enforce the pre-condition that the FailSafe window is fully disarmed
// before a new commissioning window may be opened. Pass nil to detach
// (the check is then skipped — safe for test setups).
func (a *AdministratorCommissioning) SetIsFailSafeArmed(fn func() bool) {
	a.mu.Lock()
	a.isFailSafeArmed = fn
	a.mu.Unlock()
}

// MatterClusterID identifies the cluster.
func (a *AdministratorCommissioning) MatterClusterID() uint32 {
	return matterClusterAdminCommissioning
}

// MinInvokePrivilege implements [interfaces.MatterClusterCommandInvokePrivilege].
// OpenCommissioningWindow, OpenBasicCommissioningWindow, and
// RevokeCommissioning all require Administer (5) per Matter §11.19
// (access "A T"). Mirrors matter.js
// packages/model/src/standard/elements/administrator-commissioning.element.ts:31,43,49.
func (a *AdministratorCommissioning) MinInvokePrivilege(cmdID uint32) uint8 {
	switch cmdID {
	case matterCmdAdmCommOpenWindow, matterCmdAdmCommOpenBasicWindow, matterCmdAdmCommRevoke:
		return 5 // Administer
	default:
		return 3 // Operate — standard default
	}
}

// MatterRead resolves attribute reads.
func (a *AdministratorCommissioning) MatterRead(attrID uint32) (any, bool) {
	a.mu.RLock()
	c := a.controller
	a.mu.RUnlock()

	var snap WindowStatusSnapshot
	if c != nil {
		snap = c.CurrentWindow()
	} else {
		snap = WindowStatusSnapshot{
			Status:            WindowStatusClosed,
			AdminFabricIsNull: true,
			AdminVendorIsNull: true,
		}
	}

	switch attrID {
	case matterAttrAdmCommWindowStatus:
		return uint8(snap.Status), true
	case matterAttrAdmCommAdminFabric:
		if snap.AdminFabricIsNull {
			// Caller (bridge wire encoder) interprets a nil any as
			// "encode TLV null" — see AttributeValueWriter implementations.
			return nil, true
		}
		return snap.AdminFabricIndex, true
	case matterAttrAdmCommAdminVendorID:
		if snap.AdminVendorIsNull {
			return nil, true
		}
		return snap.AdminVendorID, true
	case matterAttrAdmCommFeatureMap:
		// FeatureMap = 0: basic-commissioning-method (BC feature) not
		// advertised. Enhanced commissioning (the default) does not
		// require a feature bit.
		return uint32(0), true
	case matterAttrAdmCommClusterRevision:
		return admCommClusterRevision, true
	default:
		return nil, false
	}
}

// MatterWrite rejects every attribute write — every Matter §11.19.5
// attribute is read-only.
func (a *AdministratorCommissioning) MatterWrite(_ context.Context, attrID uint32, _ any, _ hmenum.CommandPriority) error {
	return fmt.Errorf("matter: AdministratorCommissioning attribute 0x%04X is read-only", attrID)
}

// MatterInvoke routes the OpenCommissioningWindow / RevokeCommissioning
// commands.
func (a *AdministratorCommissioning) MatterInvoke(ctx context.Context, cmdID uint32, fields any, _ hmenum.CommandPriority) (any, error) {
	a.mu.RLock()
	c := a.controller
	vr := a.vendorResolver
	fc := a.fabricCounter
	failSafeCheck := a.isFailSafeArmed
	a.mu.RUnlock()

	switch cmdID {
	case matterCmdAdmCommOpenWindow:
		params, ok := fields.(OpenWindowParams)
		if !ok {
			// fields can be a pointer in some dispatcher paths; be
			// liberal in what we accept here.
			if p, okp := fields.(*OpenWindowParams); okp && p != nil {
				params = *p
			} else {
				return nil, errAdmCommInvalidFields
			}
		}
		// Validate PAKE parameters per Matter §11.19.8.1.2 FIRST — before
		// the PASE / Busy / fail-safe gates. Mirrors matter.js
		// AdministratorCommissioningServer.ts:82-97 (openCommissioningWindow
		// checks the verifier length, iterations, and salt bounds — each
		// raising PakeParameterError — BEFORE #assertCommissioningWindowRequirements
		// runs the window-already-open / fail-safe Busy checks). A
		// malformed-and-busy request therefore surfaces the PAKEParameterError
		// (cluster status 0x03), not Busy. chip's
		// administrator-commissioning-server.cpp likewise validates PAKE
		// parameters before the window-state check.
		if params.Iterations < pakeIterationsMin || params.Iterations > pakeIterationsMax {
			return nil, fmt.Errorf("%w: Iterations=%d not in [%d, %d]", errAdmCommPakeParameter, params.Iterations, pakeIterationsMin, pakeIterationsMax)
		}
		if n := len(params.Salt); n < pakeSaltMinBytes || n > pakeSaltMaxBytes {
			return nil, fmt.Errorf("%w: Salt length=%d not in [%d, %d]", errAdmCommPakeParameter, n, pakeSaltMinBytes, pakeSaltMaxBytes)
		}
		if n := len(params.PAKEPasscodeVerifier); n != pakeVerifierBytes {
			return nil, fmt.Errorf("%w: PAKEPasscodeVerifier length=%d, want %d", errAdmCommPakeParameter, n, pakeVerifierBytes)
		}
		// OpenCommissioningWindow must be refused when called over a PASE
		// session (fabricIndex == 0). Multi-Admin is a CASE-only operation.
		// Mirrors chip AdministratorCommissioningCluster.cpp
		// OpenCommissioningWindow VerifyOrExit(session.IsSecureSession(), ...).
		_, fabIdx := im.FabricFilterFromContext(ctx)
		if fabIdx == 0 {
			return nil, errAdmCommBusy
		}
		// A FailSafe window being armed indicates another commissioning
		// flow is already in progress; opening a second window on top
		// produces overlapping fail-safe expiry races. Mirrors chip
		// AdministratorCommissioningCluster.cpp
		// VerifyOrExit(IsFailSafeFullyDisarmed, ...).
		if failSafeCheck != nil && failSafeCheck() {
			return nil, errAdmCommBusy
		}
		if c == nil {
			return nil, errAdmCommBusy
		}
		// Populate AdminFabricIndex from IM dispatcher context per
		// Matter §11.19.5.2 and matter.js
		// AdministratorCommissioningServer.ts:176-180
		// `this.state.adminFabricIndex = adminFabric.fabricIndex`.
		// AdminVendorID comes from the daemon-wired VendorIDResolver
		// (fabric store lookup); fabricIndex==0 (PASE) keeps the
		// VendorID at zero, which the bridge controller surfaces as
		// AdminVendorIsNull.
		params.AdminFabricIndex = fabIdx
		if fabIdx != 0 && vr != nil && params.AdminVendorID == 0 {
			params.AdminVendorID = vr(ctx, fabIdx)
		}
		// Determine whether the bridge is uncommissioned (FabricCount == 0)
		// so the window controller applies the 48-h timeout upper bound for
		// a first-time pairing scenario per Matter §11.19.8.1. Default to
		// false (commissioned, 900-s cap) when no counter is wired.
		if fc != nil {
			if n, err := fc(ctx); err == nil && n == 0 {
				params.IsUncommissioned = true
			}
		}
		if err := c.OpenWindow(ctx, params); err != nil {
			return nil, err
		}
		return nil, nil
	case matterCmdAdmCommOpenBasicWindow:
		// FeatureMap bit 0 (BC) not advertised; controllers should
		// see UnsupportedCommand. Returning a wrapped error is the
		// dispatcher contract.
		return nil, errAdmCommUnsupportedCommand
	case matterCmdAdmCommRevoke:
		if c == nil {
			return nil, errAdmCommBusy
		}
		// Matter §11.19.8.3 Step 1 closes any active PASE session
		// unconditionally; Step 2 fails the command with kWindowNotOpen
		// (cluster-specific 0x04) if no window is open. Snapshot the
		// window state BEFORE calling RevokeWindow so the PASE close still
		// fires when the window is already closed — matter.js
		// AdministratorCommissioningServer.ts:140-147 + chip
		// AdministratorCommissioningLogic.cpp:108-119.
		preStatus := c.CurrentWindow().Status
		if err := c.RevokeWindow(ctx); err != nil {
			return nil, err
		}
		if preStatus == WindowStatusClosed {
			return nil, ErrAdmCommWindowNotOpen
		}
		return nil, nil
	default:
		return nil, fmt.Errorf("%w: 0x%02X", errAdmCommUnsupportedCommand, cmdID)
	}
}

// MatterReportable lists the attributes that emit Matter reports.
// WindowStatus changes whenever a commissioning window opens / closes;
// AdminFabricIndex + AdminVendorId track the active commissioner.
func (a *AdministratorCommissioning) MatterReportable() []uint32 {
	return []uint32{
		matterAttrAdmCommWindowStatus,
		matterAttrAdmCommAdminFabric,
		matterAttrAdmCommAdminVendorID,
	}
}

// MatterAttributes lists every AdministratorCommissioning (0x003C)
// attribute the server implements via MatterRead. Apple Home's HAP
// service rebuild reads the full attribute set; without this the
// dispatcher falls back to MatterReportable's three-attribute surface.
// FeatureMap (0xFFFC) and ClusterRevision (0xFFFD) must be enumerated
// so the initial Subscribe pre-populates Apple's cache. Mirrors chip
// AdministratorCommissioningCluster.cpp:53-56 ReadAttribute(FeatureMap,
// ClusterRevision) and matter.js cluster-behavior inheritance of global
// attributes.
func (a *AdministratorCommissioning) MatterAttributes() []uint32 {
	return []uint32{
		matterAttrAdmCommWindowStatus,
		matterAttrAdmCommAdminFabric,
		matterAttrAdmCommAdminVendorID,
		matterAttrAdmCommFeatureMap,
		matterAttrAdmCommClusterRevision,
	}
}

// Typed error values that implement [im.StatusCodeError] so the
// dispatcher's type-assert path takes priority over the string heuristic.
// Mirrors matter.js AdministratorCommissioningServer.ts
// StatusResponseError usage.

// admCommBusyErr is the typed backing value for [ErrAdmCommBusy].
type admCommBusyErr struct{}

func (admCommBusyErr) Error() string {
	return "matter: AdministratorCommissioning busy (no controller wired)"
}
func (admCommBusyErr) MatterStatusCode() im.StatusCode { return im.StatusBusy }

// admCommPakeErr is the typed backing value for [ErrAdmCommPakeParameter].
// Matter §11.19.7.3 mandates a cluster-specific status code for
// PAKEParameterError returned via StatusIB.ClusterStatus alongside
// the generic IM Status code. matter.js packages/model/src/standard/elements/
// administrator-commissioning.element.ts:61 defines PakeParameterError with
// id 0x3. The wire result is Status=Failure(0x01), ClusterStatus=0x03.
type admCommPakeErr struct{}

func (admCommPakeErr) Error() string {
	return "matter: AdministratorCommissioning PAKE parameter out of range"
}
func (admCommPakeErr) MatterStatusCode() im.StatusCode { return im.StatusFailure }
func (admCommPakeErr) MatterClusterStatus() uint8      { return 0x03 } // PAKEParameterError

// admCommWindowNotOpenErr is the typed backing value for
// [ErrAdmCommWindowNotOpen]. Matter §11.19.8.3 Step 2 mandates returning
// a cluster-specific failure when RevokeCommissioning is called while no
// window is open. Both matter.js
// AdministratorCommissioningServer.ts:143-147 (throws WindowNotOpenError)
// and chip AdministratorCommissioningLogic.cpp:115-119
// (ClusterStatusCode::kWindowNotOpen = 0x04) require ClusterStatus=0x04.
type admCommWindowNotOpenErr struct{}

func (admCommWindowNotOpenErr) Error() string {
	return "matter: AdministratorCommissioning window not open"
}
func (admCommWindowNotOpenErr) MatterStatusCode() im.StatusCode { return im.StatusFailure }
func (admCommWindowNotOpenErr) MatterClusterStatus() uint8      { return 0x04 } // WindowNotOpen

// Compile-time assertions: typed errors satisfy [im.StatusCodeError]
// and (for the cluster-specific paths) [im.MatterClusterStatusError].
var (
	_ im.StatusCodeError          = admCommBusyErr{}
	_ im.StatusCodeError          = admCommPakeErr{}
	_ im.MatterClusterStatusError = admCommPakeErr{}
	_ im.StatusCodeError          = admCommWindowNotOpenErr{}
	_ im.MatterClusterStatusError = admCommWindowNotOpenErr{}
)

// Cluster-side errors. The bridge invoke dispatcher maps these to
// Matter wire status codes:
//
//   - [ErrAdmCommBusy]                → §11.19.7.1 Busy (0x9c)
//   - [ErrAdmCommUnsupportedCommand]  → §10.6.7.4 UnsupportedCommand (0x81)
//   - [ErrAdmCommInvalidFields]       → §10.6.7.4 InvalidCommand   (0x85)
//   - [ErrAdmCommWindowNotOpen]       → §11.19.8.3 WindowNotOpen cluster-specific failure
var (
	errAdmCommBusy               = admCommBusyErr{}
	errAdmCommUnsupportedCommand = errors.New("matter: AdministratorCommissioning command unsupported")
	errAdmCommInvalidFields      = errors.New("matter: AdministratorCommissioning fields malformed")
	// errAdmCommPakeParameter mirrors Matter §11.19.7.3
	// PakeParameterError (0x02). Surfaces when OpenCommissioningWindow
	// receives Iterations / Salt / PAKEPasscodeVerifier values outside
	// the spec-mandated ranges.
	errAdmCommPakeParameter = admCommPakeErr{}
	// errAdmCommWindowNotOpen mirrors Matter §11.19.8.3 Step 2 WindowNotOpen
	// cluster-specific failure.
	errAdmCommWindowNotOpen = admCommWindowNotOpenErr{}
)

// ErrAdmCommBusy is exported so the bridge dispatcher can translate
// it into the §11.19.7.1 BUSY wire status when the cluster command
// returns it.
var ErrAdmCommBusy im.StatusCodeError = errAdmCommBusy

// ErrAdmCommPakeParameter is exported so the bridge dispatcher can
// translate it into the §11.19.7.3 PakeParameterError (0x03) wire
// status when OpenCommissioningWindow receives invalid PAKE params.
var ErrAdmCommPakeParameter im.StatusCodeError = errAdmCommPakeParameter

// ErrAdmCommWindowNotOpen is exported so the bridge commissioning
// window can return a spec-conformant error when RevokeCommissioning
// is invoked while no window is open. Matter §11.19.8.3 Step 2.
var ErrAdmCommWindowNotOpen im.StatusCodeError = errAdmCommWindowNotOpen

// Matter §11.19.8.1 OpenCommissioningWindow argument ranges. Values
// outside these bounds MUST be rejected with PakeParameterError per
// the spec table 11.19.8.1.2.
const (
	pakeIterationsMin uint32 = 1000   // spec lower bound (Matter §3.10)
	pakeIterationsMax uint32 = 100000 // spec upper bound
	pakeSaltMinBytes         = 16     // spec §3.10.3
	pakeSaltMaxBytes         = 32     // spec §3.10.3
	pakeVerifierBytes        = 97     // spec §3.10.5 — fixed length
)

// MatterAcceptedCommands implements [interfaces.MatterClusterCommandLister].
// Lists the command IDs the server handles via MatterInvoke.
// Mirrors matter.js packages/model/src/standard/elements/
// administrator-commissioning.element.ts accepted commands.
//
// Note: OpenBasicCommissioningWindow (0x01) is NOT listed — the BC feature
// is not advertised in our FeatureMap and the handler returns UnsupportedCommand.
func (a *AdministratorCommissioning) MatterAcceptedCommands() []uint32 {
	return []uint32{
		matterCmdAdmCommOpenWindow, // 0x00
		matterCmdAdmCommRevoke,     // 0x02
	}
}

// MatterGeneratedCommands implements [interfaces.MatterClusterCommandLister].
// AdministratorCommissioning has no generated response commands — all outcomes
// are communicated via Matter StatusResponse.
// Mirrors matter.js packages/model/src/standard/elements/
// administrator-commissioning.element.ts generated commands (none for 0x003C).
func (a *AdministratorCommissioning) MatterGeneratedCommands() []uint32 {
	return []uint32{}
}

// Compile-time assertions: [AdministratorCommissioning] satisfies the
// bridge's cluster-server contract, the attribute-lister capability,
// and the command-lister capability.
var (
	_ interfaces.MatterClusterServer                 = (*AdministratorCommissioning)(nil)
	_ interfaces.MatterClusterAttributeLister        = (*AdministratorCommissioning)(nil)
	_ interfaces.MatterClusterCommandLister          = (*AdministratorCommissioning)(nil)
	_ interfaces.MatterClusterCommandInvokePrivilege = (*AdministratorCommissioning)(nil)
)
