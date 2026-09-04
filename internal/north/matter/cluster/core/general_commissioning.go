// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package core

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/pkg/mattercontract"
)

// GeneralCommissioning implements the Matter GeneralCommissioning
// cluster (0x0030) per Matter Core Specification 1.5.1 §11.10.
// Mandatory on the Root endpoint.
//
// Lifecycle:
//
//   - During PASE the commissioner calls ArmFailSafe — the bridge
//     enters a fail-safe window during which configuration writes
//     (NOC install, network setup) are reversible if the window
//     expires without a CommissioningComplete.
//   - SetRegulatoryConfig records the country / regulatory locale
//     so the bridge picks the right radio profiles (no-op for an
//     Ethernet-only bridge).
//   - CommissioningComplete locks the configuration in.
//
// openccu-loom ships an Ethernet-only bridge so the regulatory
// surface is purely informational; the FailSafe state machine
// reflects what the spec mandates.
type GeneralCommissioning struct {
	mu sync.RWMutex

	breadcrumb                   uint64
	regulatoryConfig             uint8
	locationCapability           uint8
	supportsConcurrentConnection bool

	// FailSafe state.
	failSafeArmed       bool
	failSafeExpiresAt   time.Time
	failSafeFabricIndex uint8

	// Cumulative FailSafe tracking: once the cumulative cap is hit the
	// bridge must refuse further ArmFailSafe calls with BusyWithOtherAdmin.
	// Mirrors chip src/app/FailSafeContext.cpp:106-133
	// HandleMaxCumulativeFailSafeTimer: a separate timer starts on the first
	// arm (non-zero ExpiryLengthSeconds) and cannot be reset by re-arms.
	failSafeCumulativeStarted  bool
	failSafeCumulativeDeadline time.Time

	// Static config.
	failSafeMaxSeconds       uint16
	cumulativeFailSafeMaxSec uint16

	// dataVersion tracks the per-cluster monotonic counter per Matter
	// §10.6.5. Bumped after every successful ArmFailSafe, SetRegulatoryConfig,
	// and CommissioningComplete so DataVersionFilter evaluation correctly
	// detects that the cluster changed.
	// Satisfies [mattercontract.ClusterDataVersion].
	// chip's general-commissioning-server uses ember dirty-marking for every
	// attribute write; matter.js behavior layer auto-tracks DataVersion for
	// state mutations. openccu-loom mirrors this with an explicit
	// DataVersionTracker (same pattern as OperationalCredentials).
	// Mirrors chip src/app/clusters/general-commissioning-server/
	// general-commissioning-server.cpp MarkAttributeDirty and matter.js
	// packages/node/src/behaviors/general-commissioning/
	// GeneralCommissioningServer.ts state mutation.
	dataVersion cluster.DataVersionTracker

	// Hooks invoked when the fail-safe expires without a
	// CommissioningComplete. Optional.
	onFailSafeExpired func(ctx context.Context, fabricIndex uint8)
	// Hook invoked on a successful CommissioningComplete. chip's
	// `CommissioningWindowManager` revokes the open commissioning window
	// automatically at this point — openccu-loom exposes the same hook
	// so the daemon-side wiring can call
	// `CommissioningWindow.RevokeWindow(ctx)`. Otherwise the window
	// stays open for the full duration (typ. 180 s) and a second,
	// unintended fabric can be admitted.
	onCommissioningComplete func(ctx context.Context, fabricIndex uint8)
	// Hook invoked every time ArmFailSafe successfully arms (or re-arms)
	// the FailSafe window. The bridge wires this to OperationalCredentials
	// so the pending-NOC / pending-trust-root state is reset for every
	// new commissioning attempt — Matter §11.10.6.2 + matter.js
	// OperationalCredentialsServer.ts model the FailSafeContext as
	// fabricIndex-scoped fresh state per arm. Without this hook Apple's
	// multi-admin "SystemCommissioner" flow (second CSRRequest +
	// AddTrustedRootCertificate + AddNOC on the same CASE session after
	// the iPhone fabric is installed) fails with CONSTRAINT_ERROR on
	// AddTrustedRootCertificate ("NOC command already invoked in this
	// FailSafe window") because the prior AddNOC's flags carry over.
	onFailSafeArmed func(ctx context.Context, fabricIndex uint8)

	// isCommissioningWindowOpen, when non-nil, reports whether an enhanced
	// commissioning window (PASE) is currently open. Used by ArmFailSafe to
	// detect and reject CASE-steal attempts per Matter §11.10.6.2.
	// Mirrors chip GeneralCommissioningCluster.cpp:419-427 which checks
	// IsCommissioningWindowOpen() alongside IsFailSafeArmed() to surface
	// BusyWithOtherAdmin. Wired by the daemon to
	// bridge.CommissioningWindowIsOpen after both clusters are built.
	// When nil the CASE-steal check is skipped (safe for test environments).
	isCommissioningWindowOpen func() bool
}

// RegulatoryLocationTypeEnum values per Matter §11.10.5.2.
const (
	RegulatoryIndoor        uint8 = 0
	RegulatoryOutdoor       uint8 = 1
	RegulatoryIndoorOutdoor uint8 = 2
)

// CommissioningErrorEnum values per Matter §11.10.5.1.
const (
	CommissioningErrorOK                            uint8 = 0
	CommissioningErrorValueOutsideRange             uint8 = 1
	CommissioningErrorInvalidAuthentication         uint8 = 2
	CommissioningErrorNoFailSafe                    uint8 = 3
	CommissioningErrorBusyWithOtherAdmin            uint8 = 4
	CommissioningErrorRequiredTCNotAccepted         uint8 = 5
	CommissioningErrorTCAcknowledgementsNotReceived uint8 = 6
	CommissioningErrorTCMinVersionNotMet            uint8 = 7
)

// Cluster ID + revision per Matter §11.10.
const (
	gencommClusterID       uint32 = 0x0030
	gencommClusterRevision uint16 = 2 // matter.js HEAD (@matter/model 0.16.11)

	gencommAttrBreadcrumb                   uint32 = 0x0000
	gencommAttrBasicCommissioningInfo       uint32 = 0x0001
	gencommAttrRegulatoryConfig             uint32 = 0x0002
	gencommAttrLocationCapability           uint32 = 0x0003
	gencommAttrSupportsConcurrentConnection uint32 = 0x0004

	gencommCmdArmFailSafe                   uint32 = 0x00
	gencommCmdArmFailSafeResponse           uint32 = 0x01
	gencommCmdSetRegulatoryConfig           uint32 = 0x02
	gencommCmdSetRegulatoryConfigResponse   uint32 = 0x03
	gencommCmdCommissioningComplete         uint32 = 0x04
	gencommCmdCommissioningCompleteResponse uint32 = 0x05
)

// errGencommInvalidArg is returned for malformed command payloads.
var errGencommInvalidArg = errors.New("matter: GeneralCommissioning invalid argument")

// GeneralCommissioningConfig drives [NewGeneralCommissioning].
type GeneralCommissioningConfig struct {
	// LocationCapability is the fixed regulatory capability (Indoor /
	// Outdoor / IndoorOutdoor) the bridge supports.
	LocationCapability uint8
	// SupportsConcurrentConnection reports whether the device can be
	// commissioned over one channel while keeping another active.
	// openccu-loom answers "true" — the bridge runs over Ethernet.
	SupportsConcurrentConnection bool
	// FailSafeMaxSeconds bounds how long an ArmFailSafe window stays
	// active. Matter spec floor is 60 s; v1.1 defaults to 900 s
	// (15 min) to keep parity with the pairing-window default in
	// the UI concept.
	FailSafeMaxSeconds uint16
	// CumulativeFailSafeMaxSeconds bounds the *total* time a
	// commissioner may spend across multiple ArmFailSafe calls in
	// one commissioning attempt.
	CumulativeFailSafeMaxSeconds uint16
	// OnFailSafeExpired is called when the fail-safe window times out
	// without a CommissioningComplete. The implementation rolls back
	// commissioning state (NOC install, network config) — the
	// detailed action depends on what the commissioner had written.
	// May be nil for tests or stub setups.
	OnFailSafeExpired func(ctx context.Context, fabricIndex uint8)
	// OnCommissioningComplete fires on a successful
	// CommissioningComplete invoke. The bridge wires this to
	// `CommissioningWindow.RevokeWindow` so a successful pair-completion
	// auto-closes the open commissioning window.
	OnCommissioningComplete func(ctx context.Context, fabricIndex uint8)
	// OnFailSafeArmed fires after every successful ArmFailSafe arm /
	// re-arm. The bridge wires this to OperationalCredentials.
	// ClearPendingState so a fresh commissioning attempt does not see
	// pending NOC / trust-root flags from a prior attempt. May be nil
	// for tests that exercise the FailSafe machinery in isolation.
	OnFailSafeArmed func(ctx context.Context, fabricIndex uint8)
	// IsCommissioningWindowOpen, when non-nil, is queried by ArmFailSafe
	// to detect CASE-steal attempts. When a commissioning window is open
	// (PASE) and an armed fail-safe is owned by a different CASE fabric,
	// ArmFailSafe returns BusyWithOtherAdmin per Matter §11.10.6.2.
	// Wired by the daemon post-construction; nil skips the check.
	IsCommissioningWindowOpen func() bool
}

// NewGeneralCommissioning constructs the cluster.
func NewGeneralCommissioning(cfg GeneralCommissioningConfig) (*GeneralCommissioning, error) {
	if cfg.LocationCapability > RegulatoryIndoorOutdoor {
		return nil, fmt.Errorf("matter: GeneralCommissioning LocationCapability=%d", cfg.LocationCapability)
	}
	if cfg.FailSafeMaxSeconds < 900 {
		// Matter §11.10.5.2 ArmFailSafe ExpiryLengthSeconds field default is
		// 900 s per matter.js packages/model/src/standard/elements/
		// general-commissioning.element.ts:66. The spec floor is 60 s; using
		// 900 s (the field default) gives commissioners the full recommended
		// window without requiring them to send an explicit larger value.
		cfg.FailSafeMaxSeconds = 900
	}
	if cfg.CumulativeFailSafeMaxSeconds == 0 {
		// Matter §11.10.5.3: spec-recommended default is 900 s (15 min).
		// matter.js HEAD GeneralCommissioningServer.ts seeds this when
		// the daemon left the field at its zero value.
		cfg.CumulativeFailSafeMaxSeconds = 900
	}
	if cfg.CumulativeFailSafeMaxSeconds < cfg.FailSafeMaxSeconds {
		cfg.CumulativeFailSafeMaxSeconds = cfg.FailSafeMaxSeconds
	}
	return &GeneralCommissioning{
		regulatoryConfig:             cfg.LocationCapability,
		locationCapability:           cfg.LocationCapability,
		supportsConcurrentConnection: cfg.SupportsConcurrentConnection,
		failSafeMaxSeconds:           cfg.FailSafeMaxSeconds,
		cumulativeFailSafeMaxSec:     cfg.CumulativeFailSafeMaxSeconds,
		onFailSafeExpired:            cfg.OnFailSafeExpired,
		onCommissioningComplete:      cfg.OnCommissioningComplete,
		onFailSafeArmed:              cfg.OnFailSafeArmed,
		isCommissioningWindowOpen:    cfg.IsCommissioningWindowOpen,
	}, nil
}

// SetOnFailSafeArmed wires the post-arm hook after construction. Used
// when the OperationalCredentials cluster the hook resets is built
// after GeneralCommissioning (avoids a constructor ordering cycle).
func (g *GeneralCommissioning) SetOnFailSafeArmed(fn func(ctx context.Context, fabricIndex uint8)) {
	g.mu.Lock()
	g.onFailSafeArmed = fn
	g.mu.Unlock()
}

// SetOnFailSafeExpired wires the FailSafe-expiry hook after construction.
// Same ordering motivation as SetOnFailSafeArmed: the OpCreds cluster
// the hook needs to invoke ClearPendingState on is built after
// GeneralCommissioning, so the daemon installs the hook post-build.
//
// Mirrors matter.js packages/node/src/behaviors/operational-credentials/
// OperationalCredentialsServer.ts:#handleFailsafeClosed which fires from
// fabricManager.events.failsafeClosed after the FailSafeContext rolls back.
func (g *GeneralCommissioning) SetOnFailSafeExpired(fn func(ctx context.Context, fabricIndex uint8)) {
	g.mu.Lock()
	g.onFailSafeExpired = fn
	g.mu.Unlock()
}

// SetOnCommissioningComplete replaces the commissioning-complete hook after
// construction. The daemon uses this to augment the window-revoke-only hook
// installed at construction time with OpCreds.ClearPendingState, which resets
// pendingInstallFabricIndex so a subsequent FailSafe expiry cannot
// accidentally revert an already-confirmed fabric.
// Mirrors chip FailSafeContext::Reset() called from the success path in
// CommissioningWindowManager::OnCommissioningComplete.
func (g *GeneralCommissioning) SetOnCommissioningComplete(fn func(ctx context.Context, fabricIndex uint8)) {
	g.mu.Lock()
	g.onCommissioningComplete = fn
	g.mu.Unlock()
}

// SetIsCommissioningWindowOpen wires the commissioning-window predicate used
// by ArmFailSafe to detect and reject CASE-steal attempts. Pass nil to detach.
// Mirrors chip GeneralCommissioningCluster.cpp:419-427 which checks
// IsCommissioningWindowOpen() + the current fail-safe owner before returning
// BusyWithOtherAdmin on a conflicting ArmFailSafe.
func (g *GeneralCommissioning) SetIsCommissioningWindowOpen(fn func() bool) {
	g.mu.Lock()
	g.isCommissioningWindowOpen = fn
	g.mu.Unlock()
}

// Compile-time assertions.
var (
	_ mattercontract.ClusterServer                  = (*GeneralCommissioning)(nil)
	_ mattercontract.ClusterCommandLister           = (*GeneralCommissioning)(nil)
	_ mattercontract.ClusterDataVersion             = (*GeneralCommissioning)(nil)
	_ mattercontract.ClusterCommandInvokePrivilege  = (*GeneralCommissioning)(nil)
	_ mattercontract.ClusterAttributeWritePrivilege = (*GeneralCommissioning)(nil)
)

// MatterDataVersion implements [mattercontract.ClusterDataVersion].
// Mirrors chip src/app/clusters/general-commissioning-server/
// general-commissioning-server.cpp MarkAttributeDirty(Attribute::Breadcrumb)
// and matter.js packages/node/src/behaviors/general-commissioning/
// GeneralCommissioningServer.ts state.breadcrumb / state.regulatoryConfig
// mutations that bump the cluster-level DataVersion automatically.
func (g *GeneralCommissioning) MatterDataVersion() uint32 { return g.dataVersion.Current() }

// MatterClusterID implements [mattercontract.ClusterServer].
func (g *GeneralCommissioning) MatterClusterID() uint32 { return gencommClusterID }

// MinInvokePrivilege implements [mattercontract.ClusterCommandInvokePrivilege].
// ArmFailSafe, SetRegulatoryConfig, and CommissioningComplete require
// Administer (5) per Matter §11.10 (access "A"). Mirrors matter.js
// packages/model/src/standard/elements/general-commissioning.element.ts:63,78,92.
func (g *GeneralCommissioning) MinInvokePrivilege(cmdID uint32) uint8 {
	switch cmdID {
	case gencommCmdArmFailSafe, gencommCmdSetRegulatoryConfig, gencommCmdCommissioningComplete:
		return 5 // Administer
	default:
		return 3 // Operate — standard default
	}
}

// MinWritePrivilege implements [mattercontract.ClusterAttributeWritePrivilege].
// Breadcrumb (0x0000) requires Administer (5) per Matter §11.10 (access
// "RW VA"). Mirrors matter.js packages/model/src/standard/elements/
// general-commissioning.element.ts:26.
func (g *GeneralCommissioning) MinWritePrivilege(attrID uint32) uint8 {
	switch attrID {
	case gencommAttrBreadcrumb:
		return 5 // Administer
	default:
		return 3 // Operate — standard default
	}
}

// BasicCommissioningInfoStruct mirrors Matter §11.10.5.3.
type BasicCommissioningInfoStruct struct {
	FailSafeExpiryLengthSeconds  uint16
	MaxCumulativeFailsafeSeconds uint16
}

// MatterRead implements [mattercontract.ClusterServer].
func (g *GeneralCommissioning) MatterRead(attrID uint32) (any, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	switch attrID {
	case gencommAttrBreadcrumb:
		return g.breadcrumb, true
	case gencommAttrBasicCommissioningInfo:
		return BasicCommissioningInfoStruct{
			FailSafeExpiryLengthSeconds:  g.failSafeMaxSeconds,
			MaxCumulativeFailsafeSeconds: g.cumulativeFailSafeMaxSec,
		}, true
	case gencommAttrRegulatoryConfig:
		return g.regulatoryConfig, true
	case gencommAttrLocationCapability:
		return g.locationCapability, true
	case gencommAttrSupportsConcurrentConnection:
		return g.supportsConcurrentConnection, true
	case cluster.AttrGlobalFeatureMap:
		return uint32(0), true
	case cluster.AttrGlobalClusterRevision:
		return gencommClusterRevision, true
	}
	return nil, false
}

// MatterWrite handles Breadcrumb writes (Matter §11.10.6.1). Other
// attributes are read-only.
func (g *GeneralCommissioning) MatterWrite(_ context.Context, attrID uint32, value any) error {
	if attrID != gencommAttrBreadcrumb {
		return fmt.Errorf("matter: GeneralCommissioning attribute 0x%04X is read-only", attrID)
	}
	v, ok := value.(uint64)
	if !ok {
		return fmt.Errorf("%w: Breadcrumb expected uint64, got %T", errGencommInvalidArg, value)
	}
	g.mu.Lock()
	g.breadcrumb = v
	g.mu.Unlock()
	return nil
}

// ArmFailSafeRequest mirrors Matter §11.10.6.2.
type ArmFailSafeRequest struct {
	ExpiryLengthSeconds uint16
	Breadcrumb          uint64
}

// ArmFailSafeResponse mirrors Matter §11.10.6.3.
type ArmFailSafeResponse struct {
	ErrorCode uint8
	DebugText string
}

// SetRegulatoryConfigRequest mirrors Matter §11.10.6.4.
type SetRegulatoryConfigRequest struct {
	NewRegulatoryConfig uint8
	CountryCode         string
	Breadcrumb          uint64
}

// SetRegulatoryConfigResponse mirrors Matter §11.10.6.5.
type SetRegulatoryConfigResponse struct {
	ErrorCode uint8
	DebugText string
}

// CommissioningCompleteResponse mirrors Matter §11.10.6.7.
type CommissioningCompleteResponse struct {
	ErrorCode uint8
	DebugText string
}

// MatterInvoke implements [mattercontract.ClusterServer].
func (g *GeneralCommissioning) MatterInvoke(ctx context.Context, cmdID uint32, fields any) (any, error) {
	cmdName := gencommCmdName(cmdID)
	slog.Default().Info("matter.gencomm.cmd",
		slog.String("cmd", cmdName),
		slog.String("cmdID", fmt.Sprintf("0x%02X", cmdID)))
	resp, err := g.dispatchCmd(ctx, cmdID, fields)
	if err != nil {
		slog.Default().Warn("matter.gencomm.cmd_err",
			slog.String("cmd", cmdName),
			slog.String("err", err.Error()))
	} else {
		slog.Default().Info("matter.gencomm.cmd_ok",
			slog.String("cmd", cmdName),
			slog.String("respType", fmt.Sprintf("%T", resp)))
	}
	return resp, err
}

func (g *GeneralCommissioning) dispatchCmd(ctx context.Context, cmdID uint32, fields any) (any, error) {
	switch cmdID {
	case gencommCmdArmFailSafe:
		return g.handleArmFailSafe(ctx, fields)
	case gencommCmdSetRegulatoryConfig:
		return g.handleSetRegulatoryConfig(fields)
	case gencommCmdCommissioningComplete:
		return g.handleCommissioningComplete(ctx)
	}
	return nil, im.UnsupportedCommandf("matter: GeneralCommissioning command 0x%02X not supported", cmdID)
}

func gencommCmdName(cmdID uint32) string {
	switch cmdID {
	case gencommCmdArmFailSafe:
		return "ArmFailSafe"
	case gencommCmdSetRegulatoryConfig:
		return "SetRegulatoryConfig"
	case gencommCmdCommissioningComplete:
		return "CommissioningComplete"
	default:
		return fmt.Sprintf("Cmd0x%02X", cmdID)
	}
}

// MatterReportable lists the subscribe-able attributes.
func (g *GeneralCommissioning) MatterReportable() []uint32 {
	return []uint32{gencommAttrBreadcrumb, gencommAttrRegulatoryConfig}
}

// MatterAttributes implements [mattercontract.ClusterAttributeLister]
// so wildcard subscribe enumerates the full cluster surface.
func (g *GeneralCommissioning) MatterAttributes() []uint32 {
	return []uint32{
		gencommAttrBreadcrumb,
		gencommAttrBasicCommissioningInfo,
		gencommAttrRegulatoryConfig,
		gencommAttrLocationCapability,
		gencommAttrSupportsConcurrentConnection,
	}
}

// MatterAcceptedCommands implements [mattercontract.ClusterCommandLister].
// Lists the command IDs the server handles via MatterInvoke.
// Mirrors matter.js packages/model/src/standard/elements/
// general-commissioning.element.ts accepted commands.
//
// Note: SetTCAcknowledgments (0x05) is Matter 1.3+ and not implemented
// in v1.1 — it is intentionally omitted.
func (g *GeneralCommissioning) MatterAcceptedCommands() []uint32 {
	return []uint32{
		gencommCmdArmFailSafe,           // 0x00
		gencommCmdSetRegulatoryConfig,   // 0x02
		gencommCmdCommissioningComplete, // 0x04
	}
}

// MatterGeneratedCommands implements [mattercontract.ClusterCommandLister].
// Lists the response command IDs this server may emit.
// Mirrors matter.js packages/model/src/standard/elements/
// general-commissioning.element.ts generated commands.
func (g *GeneralCommissioning) MatterGeneratedCommands() []uint32 {
	return []uint32{
		gencommCmdArmFailSafeResponse,           // 0x01
		gencommCmdSetRegulatoryConfigResponse,   // 0x03
		gencommCmdCommissioningCompleteResponse, // 0x05
	}
}

func (g *GeneralCommissioning) handleArmFailSafe(ctx context.Context, fields any) (any, error) {
	req, ok := fields.(ArmFailSafeRequest)
	if !ok {
		return nil, fmt.Errorf("%w: ArmFailSafeRequest expected, got %T", errGencommInvalidArg, fields)
	}
	g.mu.Lock()
	// The disarm branch unlocks explicitly before firing the revert hook
	// (so the OpCreds rollback never runs under our mutex); this guard
	// keeps the deferred unlock a no-op in that case while every other
	// return path still unlocks normally.
	unlocked := false
	defer func() {
		if !unlocked {
			g.mu.Unlock()
		}
	}()

	// sessFabric is the requesting fabric (0 == PASE / commissioning
	// session). It gates the fail-safe ownership rules below.
	_, sessFabric := im.FabricFilterFromContext(ctx)

	// (a) A CASE session may not arm the fail-safe while a commissioning
	// window is open for another admin and the fail-safe is not yet armed:
	// the short window is reserved for the PASE commissioner. Mirrors
	// matter.js GeneralCommissioningServer.ts:82-90 (`!isFailsafeArmed &&
	// windowStatus !== WindowNotOpen && !session.isPase` → BusyWithOtherAdmin).
	// sessFabric != 0 identifies a CASE session (a PASE invoke resolves to
	// fabric 0). The window hook is only consulted when wired; test paths
	// without it fall through (no window means no reservation to protect).
	if !g.failSafeArmed && sessFabric != 0 && g.isCommissioningWindowOpen != nil && g.isCommissioningWindowOpen() {
		return ArmFailSafeResponse{
			ErrorCode: CommissioningErrorBusyWithOtherAdmin,
			DebugText: "cannot arm fail-safe over CASE while a commissioning window is open",
		}, nil
	}

	// (b)+(c) Ownership: once the fail-safe is armed by a fabric, only that
	// same fabric may re-arm OR disarm it — regardless of window state. A
	// different fabric attempting either leaves the fail-safe unchanged and
	// is rejected with BusyWithOtherAdmin. This stops fabric B from
	// disarming fabric A's fail-safe mid-commissioning (which would roll
	// back A's pending NOC) or hijacking A's arm. Mirrors matter.js
	// FailsafeTimer.reArm (FailsafeTimer.ts:53-57): a fabricIndex mismatch
	// throws, which GeneralCommissioningServer maps to BusyWithOtherAdmin.
	if g.failSafeArmed && g.failSafeFabricIndex != sessFabric {
		return ArmFailSafeResponse{
			ErrorCode: CommissioningErrorBusyWithOtherAdmin,
			DebugText: fmt.Sprintf("fail-safe owned by fabric %d; requesting fabric %d rejected", g.failSafeFabricIndex, sessFabric),
		}, nil
	}

	if req.ExpiryLengthSeconds == 0 {
		// Spec: ExpiryLengthSeconds=0 disarms the fail-safe and resets the
		// cumulative cap so the next commissioning attempt starts fresh.
		// Mirrors chip FailSafeContext::Disarm() which stops both the
		// single-arm timer and the cumulative timer.
		//
		// A disarm before CommissioningComplete must run the SAME revert
		// path the timeout fires: chip's GeneralCommissioningCluster.cpp:429-432
		// (ForceFailSafeTimerExpiry) routes into FailSafeContext.cpp:66-76,
		// which performs the full cleanup (RevertPendingOpCerts + drop the
		// pending fabric). Without firing onFailSafeExpired here a disarm
		// would leak a half-installed NOC: AddNOC's pending fabric is never
		// rolled back, so a re-pair attempt collides with the orphaned entry.
		wasArmed := g.failSafeArmed
		expiredFabric := g.failSafeFabricIndex
		g.failSafeArmed = false
		g.failSafeFabricIndex = 0
		g.failSafeCumulativeStarted = false
		g.breadcrumb = req.Breadcrumb
		// Bump DataVersion on disarm so Apple's MTRDevice sees a version
		// change and invalidates its cached DataVersion=0.
		// Mirrors chip MarkAttributeDirty(Breadcrumb).
		g.dataVersion.Bump()
		// Release the cluster lock before firing the hook so
		// OpCreds.OnFailSafeExpiry (which acquires its own lock to perform
		// the AddNOC rollback) runs without our mutex held — matching the
		// timeout path in watchFailSafeExpiry.
		hook := g.onFailSafeExpired
		g.mu.Unlock()
		unlocked = true
		if wasArmed && hook != nil {
			hook(ctx, expiredFabric)
		}
		return ArmFailSafeResponse{ErrorCode: CommissioningErrorOK}, nil
	}
	if req.ExpiryLengthSeconds > g.failSafeMaxSeconds {
		return ArmFailSafeResponse{
			ErrorCode: CommissioningErrorValueOutsideRange,
			DebugText: fmt.Sprintf("ExpiryLengthSeconds=%d > max=%d", req.ExpiryLengthSeconds, g.failSafeMaxSeconds),
		}, nil
	}
	// Cumulative-cap enforcement: once the total commissioning time across
	// all re-arms exceeds MaxCumulativeFailsafeSeconds the bridge must
	// refuse further arms. Mirrors chip src/app/FailSafeContext.cpp:106-133
	// HandleMaxCumulativeFailSafeTimer — the cumulative timer starts on the
	// first arm and runs independently; re-arms cannot reset it.
	now := time.Now()
	if !g.failSafeCumulativeStarted {
		g.failSafeCumulativeStarted = true
		g.failSafeCumulativeDeadline = now.Add(time.Duration(g.cumulativeFailSafeMaxSec) * time.Second)
	} else if now.After(g.failSafeCumulativeDeadline) {
		// Cumulative cap exceeded: return BusyWithOtherAdmin per Matter §11.10.6.2.
		// chip returns FAILSAFE_REQUIRED_ERROR via StatusCode; matter.js
		// GeneralCommissioningServer.ts maps this to
		// CommissioningErrorBusyWithOtherAdmin (4).
		return ArmFailSafeResponse{
			ErrorCode: CommissioningErrorBusyWithOtherAdmin,
			DebugText: fmt.Sprintf("cumulative fail-safe cap of %d s exceeded", g.cumulativeFailSafeMaxSec),
		}, nil
	}
	g.failSafeArmed = true
	g.failSafeExpiresAt = now.Add(time.Duration(req.ExpiryLengthSeconds) * time.Second)
	g.breadcrumb = req.Breadcrumb
	// Capture the requesting fabric so CommissioningComplete can verify
	// that the *same* CASE fabric closes the window the PASE/CASE
	// session opened. PASE sessions surface as fabric==0, which is OK
	// here — only Complete is fabric-gated.
	g.failSafeFabricIndex = sessFabric
	// Bump DataVersion on successful arm so Apple's MTRDevice sees the cluster
	// changed and does not skip it with DataVersionFilter=0.
	// Mirrors chip src/app/clusters/general-commissioning-server/
	// general-commissioning-server.cpp MarkAttributeDirty.
	g.dataVersion.Bump()

	if g.onFailSafeExpired != nil {
		go g.watchFailSafeExpiry(ctx, g.failSafeFabricIndex, g.failSafeExpiresAt)
	}
	// Capture the hook + fabric before releasing the cluster lock so the
	// callback runs without holding our mutex (OpCreds.ClearPendingState
	// acquires its own lock).
	armedHook := g.onFailSafeArmed
	armedFabric := g.failSafeFabricIndex
	if armedHook != nil {
		// Mirrors matter.js OperationalCredentialsServer.ts:
		// FailSafeContext is recreated on every ArmFailSafe, surfacing a
		// fresh `rootCertSet` / `fabricIndex` set so multi-admin pairing
		// (Apple's SystemCommissioner adds the iCloud-Heim fabric after
		// the iPhone fabric is installed) can run a clean CSRRequest +
		// AddTrustedRootCertificate + AddNOC sequence on the same
		// CASE session.
		armedHook(ctx, armedFabric)
	}
	return ArmFailSafeResponse{ErrorCode: CommissioningErrorOK}, nil
}

// watchFailSafeExpiry blocks until expiresAt (or ctx is cancelled) and
// fires onFailSafeExpired iff the fail-safe is still armed at that
// moment. Runs in a fresh goroutine off ArmFailSafe; harmless if the
// commissioner calls CommissioningComplete or re-arms before timeout.
func (g *GeneralCommissioning) watchFailSafeExpiry(ctx context.Context, fabricIndex uint8, expiresAt time.Time) {
	timer := time.NewTimer(time.Until(expiresAt))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return
	case <-timer.C:
	}
	g.mu.Lock()
	stillArmed := g.failSafeArmed && !g.failSafeExpiresAt.After(time.Now())
	if stillArmed {
		g.failSafeArmed = false
		// chip's GeneralCommissioningCluster.cpp:146 (kFailSafeTimerExpired
		// branch) clears the Breadcrumb when the fail-safe expires —
		// Matter §11.10.6.1 "Breadcrumb SHALL be set to 0 on FailSafe
		// expiry". Apple's HomeKitDaemon reads Breadcrumb post-failsafe
		// and a stale non-zero value can confuse the commissioning-recovery
		// flow.
		g.breadcrumb = 0
		// Reset the cumulative cap so the next commissioning attempt
		// starts fresh. Mirrors chip FailSafeContext::HandleMaxCumulativeFailSafeTimer
		// which stops when the fail-safe expires.
		g.failSafeCumulativeStarted = false
	}
	hook := g.onFailSafeExpired
	g.mu.Unlock()
	if stillArmed && hook != nil {
		hook(ctx, fabricIndex)
	}
}

func (g *GeneralCommissioning) handleSetRegulatoryConfig(fields any) (any, error) {
	req, ok := fields.(SetRegulatoryConfigRequest)
	if !ok {
		return nil, fmt.Errorf("%w: SetRegulatoryConfigRequest expected, got %T", errGencommInvalidArg, fields)
	}
	if req.NewRegulatoryConfig > RegulatoryIndoorOutdoor {
		return SetRegulatoryConfigResponse{
			ErrorCode: CommissioningErrorValueOutsideRange,
			DebugText: fmt.Sprintf("NewRegulatoryConfig=%d", req.NewRegulatoryConfig),
		}, nil
	}
	if req.CountryCode != "" && len(req.CountryCode) != 2 {
		return SetRegulatoryConfigResponse{
			ErrorCode: CommissioningErrorValueOutsideRange,
			DebugText: fmt.Sprintf("CountryCode length=%d (want 0 or 2)", len(req.CountryCode)),
		}, nil
	}
	g.mu.Lock()
	g.regulatoryConfig = req.NewRegulatoryConfig
	g.breadcrumb = req.Breadcrumb
	// Bump DataVersion on successful config change.
	// Mirrors chip MarkAttributeDirty(RegulatoryConfig) and matter.js
	// GeneralCommissioningServer.ts state.regulatoryConfig mutation.
	g.dataVersion.Bump()
	g.mu.Unlock()
	return SetRegulatoryConfigResponse{ErrorCode: CommissioningErrorOK}, nil
}

func (g *GeneralCommissioning) handleCommissioningComplete(ctx context.Context) (any, error) {
	// Mirrors matter.js packages/node/src/behaviors/general-commissioning/
	// GeneralCommissioningServer.ts:216-265:
	//   1. Reject PASE (and Group) sessions with InvalidAuthentication.
	//   2. Reject when no fail-safe is armed → NoFailSafe.
	//   3. Reject when the requesting CASE fabric does not match the
	//      fabric that armed the fail-safe → InvalidAuthentication.
	//   4. On success: clear fail-safe state, reset Breadcrumb to 0
	//      (D-56), and report OK.
	//
	// The bridge stamps the active session's FabricIndex into the
	// invoke context via `WithFabricFilter`; FabricIndex==0 signals a
	// PASE session (or unsecured channel) — Apple Home, Google Home,
	// and chip-tool always send CommissioningComplete over CASE, so
	// the PASE branch is purely a defence-in-depth.
	_, sessFabric := im.FabricFilterFromContext(ctx)
	g.mu.Lock()
	defer g.mu.Unlock()
	if sessFabric == 0 {
		return CommissioningCompleteResponse{
			ErrorCode: CommissioningErrorInvalidAuthentication,
			DebugText: "Command must be executed over CASE session.",
		}, nil
	}
	if !g.failSafeArmed {
		return CommissioningCompleteResponse{
			ErrorCode: CommissioningErrorNoFailSafe,
			DebugText: "fail-safe not armed",
		}, nil
	}
	if g.failSafeFabricIndex != 0 && sessFabric != g.failSafeFabricIndex {
		return CommissioningCompleteResponse{
			ErrorCode: CommissioningErrorInvalidAuthentication,
			DebugText: fmt.Sprintf("session fabric %d != failsafe fabric %d", sessFabric, g.failSafeFabricIndex),
		}, nil
	}
	g.failSafeArmed = false
	g.failSafeFabricIndex = 0
	g.breadcrumb = 0
	// Reset the cumulative cap on successful CommissioningComplete so the
	// next pair attempt starts with a fresh cumulative window.
	g.failSafeCumulativeStarted = false
	// Bump DataVersion on commissioning complete so subscribers see the
	// cluster-state flip. Mirrors chip MarkAttributeDirty(Breadcrumb) on the
	// success path and matter.js GeneralCommissioningServer.ts state mutation
	// on CommissioningComplete.
	g.dataVersion.Bump()
	hook := g.onCommissioningComplete
	// Release the read lock before firing the hook — the hook may take
	// arbitrary locks (commissioning window mutex, mDNS unpublish, …)
	// and holding ours invites deadlock.
	g.mu.Unlock()
	if hook != nil {
		// chip's CommissioningWindowManager auto-closes the open
		// enhanced-commissioning window on a successful
		// CommissioningComplete (Matter §11.20.6.1). Wire the same
		// behaviour via the daemon-supplied hook so the window does
		// not stay open until its full timeout (typ. 180s) and admit
		// a second, unintended fabric.
		hook(ctx, sessFabric)
	}
	g.mu.Lock() // re-acquire so the deferred Unlock in MatterInvoke balances correctly
	return CommissioningCompleteResponse{ErrorCode: CommissioningErrorOK}, nil
}

// ArmFailSafeFor is the public entry-point called by the
// [bridge.FailSafeArmer] adapter when a commissioning window opens.
// Matter §11.19.6 requires the FailSafe to be armed for the window
// duration; the daemon wires this to [bridge.CommissioningWindow]'s
// [FailSafeArmer] interface so the window open-path actually arms
// the timer.
//
// Mirrors matter.js packages/node/src/behaviors/administrator-commissioning/
// AdministratorCommissioningServer.ts:openCommissioningWindow →
// GeneralCommissioningBehavior.armFailSafeLogic(timeoutSeconds) and
// chip CommissioningWindowManager.cpp ArmFailSafe() call.
func (g *GeneralCommissioning) ArmFailSafeFor(ctx context.Context, seconds uint32, fabricIndex uint8) error {
	if seconds == 0 {
		// A zero-second arm is a DISARM request. Its only caller is
		// CommissioningWindow.RevokeWindow, which disarms so the next
		// OpenCommissioningWindow is not Busy-locked. Treat it as a pure state
		// reset: do NOT run the arm logic below (now+0 is immediately expired,
		// so watchFailSafeExpiry would fire onFailSafeExpired at once) and do
		// NOT fire onFailSafeExpired here. Both RevokeWindow callers — the
		// CommissioningComplete hook and the onFailSafeExpired hook — already
		// ran the pending-NOC rollback (ClearPendingState / OnFailSafeExpiry)
		// before revoking, and onFailSafeExpired itself calls RevokeWindow:
		// firing the hook from here would re-enter RevokeWindow →
		// ArmFailSafeFor(0) → watcher → hook → … an unbounded loop that pegs a
		// core and floods the log.
		//
		// This differs from the cluster-wire ArmFailSafe(ExpiryLengthSeconds=0)
		// disarm in handleArmFailSafe, which DOES fire the hook: that path is a
		// commissioner-initiated disarm that owns the pending-NOC rollback and
		// is never reached from inside the hook, so it cannot recurse.
		g.mu.Lock()
		g.failSafeArmed = false
		g.failSafeFabricIndex = 0
		g.failSafeCumulativeStarted = false
		// Matter §11.10.6.1: Breadcrumb SHALL be 0 once the fail-safe is no
		// longer armed. Bump DataVersion so Apple's MTRDevice invalidates its
		// cached snapshot, matching the wire-disarm and expiry paths.
		g.breadcrumb = 0
		g.dataVersion.Bump()
		g.mu.Unlock()
		return nil
	}
	req := ArmFailSafeRequest{
		ExpiryLengthSeconds: uint16(seconds & 0xFFFF), // window timeout ≤ 900 s, fits uint16
		Breadcrumb:          0,                        // window open does not set Breadcrumb
	}
	// Temporarily inject the fabric index so handleArmFailSafe records it.
	// handleArmFailSafe re-derives it from ctx via FabricFilterFromContext;
	// override the context's fabric filter if needed, or set directly.
	g.mu.Lock()
	g.failSafeArmed = true
	g.failSafeExpiresAt = time.Now().Add(time.Duration(seconds) * time.Second)
	g.breadcrumb = req.Breadcrumb
	g.failSafeFabricIndex = fabricIndex
	g.dataVersion.Bump()
	expiresAt := g.failSafeExpiresAt
	hasCB := g.onFailSafeExpired != nil
	g.mu.Unlock()
	if hasCB {
		go g.watchFailSafeExpiry(ctx, fabricIndex, expiresAt)
	}
	return nil
}

// SetCurrentFabric records the fabric the active commissioning
// session belongs to. The bridge calls this from the IM dispatcher
// before invoking ArmFailSafe so the expiry hook knows which fabric
// to roll back.
func (g *GeneralCommissioning) SetCurrentFabric(idx uint8) {
	g.mu.Lock()
	g.failSafeFabricIndex = idx
	g.mu.Unlock()
}

// FailSafeArmed reports whether a fail-safe window is currently
// active. Used by the OperationalCredentials cluster (Stufe 4) to
// gate AddNOC / AddTrustedRootCertificate.
func (g *GeneralCommissioning) FailSafeArmed() bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.failSafeArmed && g.failSafeExpiresAt.After(time.Now())
}

// PaseSessionAutoArmSeconds is the default fail-safe window duration
// the bridge arms automatically when a new PASE session is established
// and no ArmFailSafe has been sent yet.
const PaseSessionAutoArmSeconds uint16 = 60

// AutoArmOnPaseEstablished arms a 60-second FailSafe window when the
// bridge receives a new PASE session and the fail-safe is not already
// armed. This mirrors the defensive safety-net that matter.js's
// GeneralCommissioningServer.ts applies on every new PASE session
// (packages/node/src/behaviors/general-commissioning/
// GeneralCommissioningServer.ts:67-82): the window ensures that any
// uncommitted configuration state is rolled back via the expiry hook
// if the commissioner disappears without calling CommissioningComplete.
//
// Callers that do not want the auto-arm (e.g. non-PASE paths) should
// not call this function. The daemon wires it from the PaseAdapter's
// onEstablished callback so the arm fires exactly once per successful
// Pake3 completion.
func (g *GeneralCommissioning) AutoArmOnPaseEstablished(ctx context.Context) {
	g.mu.Lock()
	alreadyArmed := g.failSafeArmed && g.failSafeExpiresAt.After(time.Now())
	g.mu.Unlock()
	if alreadyArmed {
		// Commissioner already sent ArmFailSafe explicitly; don't
		// overwrite the window they chose.
		return
	}
	_ = g.ArmFailSafeFor(ctx, uint32(PaseSessionAutoArmSeconds), 0)
}
