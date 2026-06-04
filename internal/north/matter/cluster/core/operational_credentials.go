// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package core

import (
	"context"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	hkdfPkg "crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"sync"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/internal/north/matter/secure/mattercert"
	"github.com/SukramJ/openccu-loom/internal/north/matter/store"
	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// OperationalCredentials implements the Matter OperationalCredentials
// cluster (0x003E) per Matter Core Specification 1.5.1 §11.18.
// Mandatory on the Root endpoint.
//
// Responsibilities:
//
//   - Attestation surface (AttestationRequest / CSRRequest /
//     CertificateChainRequest) — produces the inputs the commissioner
//     needs to issue a NOC.
//   - NOC installation (AddNOC / UpdateNOC) — validates the
//     commissioner-supplied operational certificate and persists the
//     identity via [store.Store].
//   - Fabric maintenance (UpdateFabricLabel / RemoveFabric) — relabels
//     or evicts a fabric.
//   - Trusted root management (AddTrustedRootCertificate) — installs
//     the fabric's root CA before AddNOC binds an identity to it.
//
// The cluster keeps no in-memory fabric list — every read goes
// through [store.Store] so the persisted state is the source of truth.
type OperationalCredentials struct {
	store StoreFacade

	mu                  sync.RWMutex
	supportedFabrics    uint8             // from OpcredsConfig; default 254 (matter.js HEAD) when unset
	pendingTrustRoot    []byte            // last AddTrustedRootCertificate decoded as the 65-byte EC-P256 pubkey; consumed by AddNOC for CASE lookups + CompressedFabricID HKDF
	pendingTrustRootDER []byte            // last AddTrustedRootCertificate full Matter RCAC TLV bytes (verbatim); served by TrustedRootCertificates read — Apple validates per-entry, see Bug-I memory
	pendingCSRNonce     []byte            // for binding AttestationRequest → CSRResponse
	pendingPrivKey      *ecdsa.PrivateKey // private key generated for next AddNOC
	currentFabric       uint8             // FabricIndex of the fabric the current request runs against
	devAttestKey        *ecdsa.PrivateKey // DAC private key (loaded from config)
	dacBytes            []byte            // Device Attestation Certificate (X.509 DER)
	paiBytes            []byte            // Product Attestation Intermediate (X.509 DER)
	cdBytes             []byte            // Certification Declaration (CMS-signed)
	attestationChalleng []byte            // current session AttestationChallenge for sig binding
	onFabricInstalled   func(ctx context.Context, fabricIndex uint8, fabricID, nodeID uint64, rootPublicKey []byte)
	onFabricRemoved     func(ctx context.Context, fabricIndex uint8)

	// pendingCSRSessionID is the session ID that issued the pending
	// CSRRequest. Set in handleCSRRequest; checked in handleAddNOC to
	// enforce the matter.js csrSessionId !== session.id guard per
	// Matter §11.18.7.5.5.
	// Mirrors matter.js OperationalCredentialsServer.ts:230-235.
	pendingCSRSessionID uint16

	// pendingCSRForUpdate captures `IsForUpdateNOC` from the CSRRequest
	// that produced [pendingPrivKey]. handleUpdateNOC requires it to be
	// true, handleAddNOC requires it to be false — otherwise the CSR
	// was issued for the wrong follow-up command.
	// Mirrors matter.js OperationalCredentialsServer.ts:#requiresFailsafe
	// + FailsafeContext.csrIsForUpdateNoc tracking and chip
	// operational-credentials-server.cpp `mAdminVendorIdForUpdateNoc`
	// pairing logic.
	pendingCSRForUpdate bool

	// nocWasInvoked records whether AddNOC or UpdateNOC has been called
	// in the current FailSafe window. Set to true on a successful NOC
	// command dispatch; cleared by clearPendingState (FailSafe expiry,
	// CommissioningComplete, RemoveFabric).
	// Mirrors chip operational-credentials-server.cpp
	// FailSafeContext::NocCommandHasBeenInvoked() and matter.js
	// OperationalCredentialsServer.ts:addTrustedRootCertificate
	// `failsafeContext.fabricIndex !== undefined` guard.
	nocWasInvoked bool

	// pendingInstallFabricIndex, when non-zero, is the FabricIndex that
	// AddNOC has persisted but CommissioningComplete has not confirmed.
	// On FailSafe expiry this slot must be reverted to avoid a half-paired
	// fabric lingering. Mirrors chip
	// CommissioningWindowManager::OnFailSafeTimerExpired which calls
	// RevertPendingOpCertsExceptRoot to undo the incomplete commissioning.
	// Cleared by clearPendingState.
	pendingInstallFabricIndex uint8

	// isFailSafeArmed is the runtime accessor to [GeneralCommissioning]'s
	// FailSafe state, wired via [OpcredsConfig.IsFailSafeArmed]. When
	// nil, the FailsafeRequired guard is skipped (test setups).
	isFailSafeArmed func() bool

	// onFabricUpdated fires after a successful UpdateNOC. The daemon
	// wires this hook to close all CASE sessions for the updated fabric
	// except the invoking one, so the commissioner must re-CASE with the
	// new NOC. Mirrors matter.js FabricManager.ts `replacing` event →
	// SessionManager.closeAllSessionsForFabricExcept and chip
	// operational-credentials-server.cpp
	// FabricTable::AbortAllOtherCommunicationOnFabric. May be nil —
	// the cluster works without session teardown; callers that need strict
	// re-CASE enforcement must wire the hook.
	onFabricUpdated func(ctx context.Context, fabricIndex uint8)

	// onMDNSReannounce, when non-nil, is called after a successful
	// RemoveFabric so the mDNS advertiser withdraws the per-fabric
	// _matter._tcp record and republishes without the removed fabric.
	// Without this, the operational record keeps advertising a
	// CompressedFabricID that no longer exists; chip-tool and Apple Home
	// then resolve a stale identity and fail CASE with
	// CHIP_ERROR_NOT_FOUND. Mirrors matter.js Fabric.remove() which calls
	// FabricManager.removeFabric, triggering a
	// MdnsServer.reannounceInstance call for the updated fabric set.
	onMDNSReannounce func(ctx context.Context)

	// dataVersion tracks the per-cluster monotonic counter per Matter
	// §10.6.5. Bumped after every successful fabric mutation (AddNOC,
	// UpdateNOC, UpdateFabricLabel, RemoveFabric) so DataVersionFilter
	// evaluation correctly detects the cluster changed.
	// Satisfies [interfaces.MatterClusterDataVersion].
	dataVersion cluster.DataVersionTracker
}

// SetOnFabricRemoved wires the fabric-removed hook after construction.
// The bridge wires its operational + subscription managers after the
// cluster is built (since both managers depend on the bridge that
// owns the cluster), so the hook cannot be passed via [OpcredsConfig]
// at NewOperationalCredentials time. Idempotent — re-wiring (e.g. on
// topology rebuild) replaces the closure cleanly. Pass nil to detach.
func (o *OperationalCredentials) SetOnFabricRemoved(hook func(ctx context.Context, fabricIndex uint8)) {
	o.mu.Lock()
	o.onFabricRemoved = hook
	o.mu.Unlock()
}

// SetAttestationChallenge installs the AttestationChallenge derived
// during PASE/CASE session establishment. The cluster signs every
// AttestationRequest / CSRRequest response over (TLV-elements ||
// AttestationChallenge) per Matter §11.18.4.7. Caller is the bridge's
// PASE-onSessionEstablished hook; sets a fresh challenge on every
// new session, since each session derives its own.
func (o *OperationalCredentials) SetAttestationChallenge(challenge []byte) {
	o.mu.Lock()
	o.attestationChalleng = append(o.attestationChalleng[:0], challenge...)
	o.mu.Unlock()
}

// invokeSessionCtxKey is the context key for the operational session ID
// carried into cluster command handlers. The bridge's receive loop
// stamps WithInvokeSessionID(ctx, requestHdr.SessionID) before calling
// the IM dispatcher, so handleCSRRequest and handleAddNOC can enforce
// the csrSessionId !== session.id binding per Matter §11.18.7.5.5 and
// matter.js OperationalCredentialsServer.ts:230-235.
//
// TODO: wire from daemon.go / bridge receive loop:
//
//	invokeCtx = core.WithInvokeSessionID(invokeCtx, requestHdr.SessionID)
type invokeSessionCtxKey struct{}

// WithInvokeSessionID returns a derived context carrying the operational
// session ID of the current invoke request. The bridge stamps this before
// dispatching commands to the IM layer; cluster handlers read it via
// [InvokeSessionIDFromContext].
func WithInvokeSessionID(ctx context.Context, sessionID uint16) context.Context {
	return context.WithValue(ctx, invokeSessionCtxKey{}, sessionID)
}

// InvokeSessionIDFromContext extracts the session ID stamped by
// [WithInvokeSessionID]. Returns 0 when no session ID is present
// (safe default: PASE / test paths without session wiring).
func InvokeSessionIDFromContext(ctx context.Context) uint16 {
	v, _ := ctx.Value(invokeSessionCtxKey{}).(uint16)
	return v
}

// StoreFacade is the subset of [store.Store] this cluster consumes.
// Defined as an interface so tests substitute an in-memory fake.
type StoreFacade interface {
	ListFabrics(ctx context.Context) ([]store.FabricRecord, error)
	GetFabric(ctx context.Context, fabricIndex uint8) (store.FabricRecord, error)
	AddFabric(ctx context.Context, rec store.FabricRecord) (uint8, error)
	UpdateFabricLabel(ctx context.Context, fabricIndex uint8, label string) error
	RemoveFabric(ctx context.Context, fabricIndex uint8) error
	UpsertIdentity(ctx context.Context, rec store.IdentityRecord) error
	GetIdentity(ctx context.Context, fabricIndex uint8) (store.IdentityRecord, error)
	// ReplaceACL is the AddNOC default-entry insertion path; required
	// by Matter §11.18.6.8.1 so the freshly-paired controller can
	// read AccessControl.acl without ACCESS_DENIED.
	ReplaceACL(ctx context.Context, fabricIndex uint8, entries []store.ACLEntry) error
	// UpsertGroupKeySet installs an epoch key slot for the given fabric.
	// AddNOC calls this to populate KeySetID=0 (the IPK slot) per
	// Matter §11.18.6.8.6 and chip
	// operational-credentials-server.cpp:484-496.
	UpsertGroupKeySet(ctx context.Context, rec store.GroupKeySet) error
	// RemoveGroupKeysByFabric erases all group-key-set rows for the
	// given fabric. Called in the AddNOC failure rollback path after the
	// fabric row exists (so IPK epoch-key slots may already have been
	// written) but a subsequent step fails. Ensures no orphaned key
	// material persists for a fabric that never completed commissioning.
	RemoveGroupKeysByFabric(ctx context.Context, fabricIndex uint8) error
}

// Cluster ID + revision per Matter §11.18.
const (
	opcredsClusterID       uint32 = 0x003E
	opcredsClusterRevision uint16 = 2 // 1.5.1 baseline

	opcredsAttrNOCs                    uint32 = 0x0000
	opcredsAttrFabrics                 uint32 = 0x0001
	opcredsAttrSupportedFabrics        uint32 = 0x0002
	opcredsAttrCommissionedFabrics     uint32 = 0x0003
	opcredsAttrTrustedRootCertificates uint32 = 0x0004
	opcredsAttrCurrentFabricIndex      uint32 = 0x0005

	opcredsCmdAttestationRequest          uint32 = 0x00
	opcredsCmdAttestationResponse         uint32 = 0x01
	opcredsCmdCertificateChainRequest     uint32 = 0x02
	opcredsCmdCertificateChainResponse    uint32 = 0x03
	opcredsCmdCSRRequest                  uint32 = 0x04
	opcredsCmdCSRResponse                 uint32 = 0x05
	opcredsCmdAddNOC                      uint32 = 0x06
	opcredsCmdUpdateNOC                   uint32 = 0x07
	opcredsCmdNOCResponse                 uint32 = 0x08
	opcredsCmdUpdateFabricLabel           uint32 = 0x09
	opcredsCmdRemoveFabric                uint32 = 0x0A
	opcredsCmdAddTrustedRootCertificate   uint32 = 0x0B
	opcredsCmdSetVidVerificationStatement uint32 = 0x0C
	opcredsCmdSignVidVerificationRequest  uint32 = 0x0D
	opcredsCmdSignVidVerificationResponse uint32 = 0x0E
)

// CertificateChainTypeEnum (Matter §11.18.5.4).
const (
	CertChainTypeDAC uint8 = 1
	CertChainTypePAI uint8 = 2
)

// NodeOperationalCertStatusEnum (Matter §11.18.5.5).
const (
	NOCStatusOK                  uint8 = 0
	NOCStatusInvalidPublicKey    uint8 = 1
	NOCStatusInvalidNodeOpID     uint8 = 2
	NOCStatusInvalidNOC          uint8 = 3
	NOCStatusMissingCsr          uint8 = 4
	NOCStatusTableFull           uint8 = 5
	NOCStatusInvalidAdminSubject uint8 = 6
	NOCStatusFabricConflict      uint8 = 9
	NOCStatusLabelConflict       uint8 = 10
	NOCStatusInvalidFabricIndex  uint8 = 11
)

// Errors.
var errOpcredsInvalidArg = errors.New("matter: OperationalCredentials invalid argument")

// OpcredsConfig drives [NewOperationalCredentials].
type OpcredsConfig struct {
	// SupportedFabrics is the maximum number of fabrics the bridge
	// admits. Mirrors matter.js's default 254 when unset; legal
	// range per Matter §11.18.4.4 is 1..254.
	SupportedFabrics uint8
	// DACPrivateKey is the bridge's Device Attestation Certificate
	// private key, used to sign the AttestationResponse signature.
	DACPrivateKey *ecdsa.PrivateKey
	// DAC is the Device Attestation Certificate (X.509 DER bytes per
	// Matter §6.2; opaque to this cluster — passed through to
	// CertificateChainResponse).
	DAC []byte
	// PAI is the Product Attestation Intermediate cert (X.509 DER).
	PAI []byte
	// CertificationDeclaration is the Matter Certification Declaration
	// (CMS-signed CD per Matter §6.3).
	CertificationDeclaration []byte
	// OnFabricInstalled fires after a successful AddNOC has persisted
	// a new fabric. The daemon uses this to re-publish the operational
	// mDNS record with the just-installed CompressedFabricID + NodeID
	// so commissioners' `FindOperationalForStayActive` step resolves.
	// May be nil — the cluster works regardless.
	OnFabricInstalled func(ctx context.Context, fabricIndex uint8, fabricID, nodeID uint64, rootPublicKey []byte)
	// OnFabricRemoved fires after a successful RemoveFabric has dropped
	// a fabric from persistent storage. The daemon uses this to evict
	// every operational session, every active subscription, and every
	// resumption record bound to the gone fabric — without the eviction
	// stale state survives the fabric's death and a subsequent pair
	// retry collides on session-id 1 with `aesccm: authentication
	// failed`, the chip-tool / Apple Home pair retry chain abort.
	// May be nil — the cluster works regardless.
	OnFabricRemoved func(ctx context.Context, fabricIndex uint8)
	// IsFailSafeArmed reports whether a FailSafe window is currently
	// active. Bound to [GeneralCommissioning.FailSafeArmed]. Used by
	// AddNOC, UpdateNOC and AddTrustedRootCertificate to enforce the
	// Matter §11.18.6.4/.8/.9 FailsafeRequired status code.
	// Mirrors matter.js OperationalCredentialsServer.ts:218
	// `#failsafeContext` existence check and chip
	// operational-credentials-server.cpp:399
	// `VerifyOrExit(failSafeContext.IsFailSafeArmed(...), errorStatus =
	// Status::FailsafeRequired)`.
	// When nil, the guard is skipped (test setups + legacy callers).
	IsFailSafeArmed func() bool
	// OnFabricUpdated fires after a successful UpdateNOC has persisted
	// the new operational certificate. The daemon wires this hook to
	// abort all CASE sessions for the updated fabric (except the
	// invoking one) so the commissioner must re-establish CASE with the
	// new NOC. Mirrors matter.js FabricManager.ts `replacing` event →
	// SessionManager and chip operational-credentials-server.cpp
	// FabricTable::AbortAllOtherCommunicationOnFabric. May be nil.
	OnFabricUpdated func(ctx context.Context, fabricIndex uint8)
	// OnMDNSReannounce, when non-nil, is called after RemoveFabric
	// removes a fabric so the mDNS advertiser withdraws the stale
	// per-fabric _matter._tcp record and republishes the remaining
	// fabric set. Mirrors matter.js Fabric.remove() which calls
	// FabricManager.removeFabric, triggering MdnsServer.reannounceInstance.
	// May be nil — the cluster functions without it; the mDNS record
	// will be stale until the next AddNOC or daemon restart.
	OnMDNSReannounce func(ctx context.Context)
}

// NewOperationalCredentials constructs the cluster.
func NewOperationalCredentials(s StoreFacade, cfg OpcredsConfig) (*OperationalCredentials, error) {
	if s == nil {
		return nil, fmt.Errorf("matter: OperationalCredentials store is required")
	}
	if cfg.SupportedFabrics == 0 {
		// Mirrors matter.js packages/node/src/behaviors/
		// operational-credentials/OperationalCredentialsServer.ts:87 —
		// when no SupportedFabrics is configured, advertise the spec
		// maximum (254). The previous "floor 5" was a non-spec
		// invention; multi-admin commissioners (Apple iCloud Hub +
		// iPhone + AppleTV) routinely install 3+ fabrics, so capping
		// near the floor causes the 6th admin to see
		// `TableFull` and abort.
		cfg.SupportedFabrics = 254
	}
	return &OperationalCredentials{
		store:             s,
		supportedFabrics:  cfg.SupportedFabrics,
		devAttestKey:      cfg.DACPrivateKey,
		dacBytes:          append([]byte(nil), cfg.DAC...),
		paiBytes:          append([]byte(nil), cfg.PAI...),
		cdBytes:           append([]byte(nil), cfg.CertificationDeclaration...),
		onFabricInstalled: cfg.OnFabricInstalled,
		onFabricRemoved:   cfg.OnFabricRemoved,
		onFabricUpdated:   cfg.OnFabricUpdated,
		onMDNSReannounce:  cfg.OnMDNSReannounce,
		isFailSafeArmed:   cfg.IsFailSafeArmed,
	}, nil
}

// SetIsFailSafeArmed wires the FailSafe accessor after construction.
// Mirrors [SetOnFabricRemoved]'s post-construction wiring pattern —
// [GeneralCommissioning] is built alongside [OperationalCredentials]
// in the bridge and the back-reference is wired once both are
// constructed. Idempotent. Pass nil to detach.
func (o *OperationalCredentials) SetIsFailSafeArmed(fn func() bool) {
	o.mu.Lock()
	o.isFailSafeArmed = fn
	o.mu.Unlock()
}

// SetOnFabricUpdated wires the fabric-updated hook after construction.
// The daemon wires this once both the cluster and the session manager
// are constructed. Idempotent. Pass nil to detach.
func (o *OperationalCredentials) SetOnFabricUpdated(hook func(ctx context.Context, fabricIndex uint8)) {
	o.mu.Lock()
	o.onFabricUpdated = hook
	o.mu.Unlock()
}

// clearPendingState zeroes all pending-commissioning fields. Called on
// FailSafe expiry (D2), RemoveFabric for the current fabric (D6), and
// CommissioningComplete. Caller must hold o.mu or call without contention.
//
// Mirrors matter.js OperationalCredentialsServer.ts:#handleFailsafeClosed
// which calls #updateFabrics (re-derives from FabricManager) and drops the
// ephemeral FabricBuilder; chip FailSafeContext::Reset() deallocates the
// PendingFabric pointer.
func (o *OperationalCredentials) clearPendingState() {
	o.pendingPrivKey = nil
	o.pendingTrustRoot = nil
	o.pendingTrustRootDER = nil
	o.pendingCSRNonce = nil
	o.pendingCSRSessionID = 0
	o.pendingCSRForUpdate = false
	o.nocWasInvoked = false
	o.pendingInstallFabricIndex = 0
}

// ClearPendingState resets all FailSafe-window-scoped state on this
// OpCreds instance. Called by GeneralCommissioning every time
// ArmFailSafe arms (or re-arms) the FailSafe window — Matter §11.10.6.2
// + matter.js OperationalCredentialsServer.ts requires the
// FailSafeContext to surface a fresh state for every new commissioning
// attempt, so Apple's multi-admin "SystemCommissioner" flow (a second
// CSRRequest + AddTrustedRootCertificate + AddNOC sequence on the same
// CASE session after the iPhone fabric has been installed) does not
// trip on stale `nocWasInvoked` / `pendingTrustRoot` flags carried from
// the previous arm.
//
// Acquires the cluster lock so callers do not need to coordinate with
// the read-side. Safe to invoke even when no pending state exists.
func (o *OperationalCredentials) ClearPendingState() {
	o.mu.Lock()
	o.clearPendingState()
	o.mu.Unlock()
}

// OnFailSafeExpiry is the explicit cluster-level FailSafe-expiry handler.
// When the FailSafe timer fires without a CommissioningComplete, all
// pending commissioning state must be cancelled: pendingTrustRoot,
// pendingTrustRootDER, pendingPrivKey, pendingCSRNonce,
// pendingCSRSessionID, pendingCSRForUpdate, and nocWasInvoked are all
// reset to their zero values.
//
// The method is safe to wire directly as the GeneralCommissioning
// OnFailSafeExpired hook:
//
//	gc.SetOnFailSafeExpired(func(ctx context.Context, fabricIndex uint8) {
//	    opcreds.OnFailSafeExpiry(ctx, fabricIndex)
//	})
//
// It is also safe to call from any other expiry path — it is idempotent
// and acquires its own lock.
func (o *OperationalCredentials) OnFailSafeExpiry(ctx context.Context, _ uint8) {
	o.mu.Lock()
	fabricToRevert := o.pendingInstallFabricIndex
	o.clearPendingState()
	o.mu.Unlock()
	// Roll back the half-paired fabric when AddNOC completed but
	// CommissioningComplete was never received. Without the rollback the
	// fabric slot stays occupied and the next pair attempt collides on
	// FabricConflict. Mirrors chip
	// CommissioningWindowManager::OnFailSafeTimerExpired which calls
	// RevertPendingOpCertsExceptRoot to evict the pending fabric record.
	if fabricToRevert != 0 {
		o.revertAddNOC(ctx, fabricToRevert)
	}
}

// opcredsFailsafeRequiredErr is the typed [im.StatusCodeError] returned
// when AddNOC, UpdateNOC, or AddTrustedRootCertificate is invoked
// without an armed FailSafe. The IM dispatcher unwraps the
// [im.StatusCodeError] interface and returns Status::FailsafeRequired
// (0xCA) per Matter §11.18.6.4/.8/.9.
type opcredsFailsafeRequiredErr struct{}

func (opcredsFailsafeRequiredErr) Error() string {
	return "matter: OperationalCredentials: FailSafe must be armed before fabric mutation"
}

func (opcredsFailsafeRequiredErr) MatterStatusCode() im.StatusCode { return im.StatusFailsafeRequired }

var errOpcredsFailsafeRequired error = opcredsFailsafeRequiredErr{}

// Compile-time assertions.
var (
	_ interfaces.MatterClusterServer        = (*OperationalCredentials)(nil)
	_ interfaces.FabricScopedReader         = (*OperationalCredentials)(nil)
	_ interfaces.MatterClusterDataVersion   = (*OperationalCredentials)(nil)
	_ interfaces.MatterClusterCommandLister = (*OperationalCredentials)(nil)
)

// MatterClusterID implements [interfaces.MatterClusterServer].
func (o *OperationalCredentials) MatterClusterID() uint32 { return opcredsClusterID }

// MatterDataVersion implements [interfaces.MatterClusterDataVersion].
// Returns the current per-cluster monotonic counter bumped after every
// successful fabric mutation (AddNOC, UpdateNOC, UpdateFabricLabel,
// RemoveFabric). Mirrors matter.js OperationalCredentialsServer.ts
// DataVersion tracking on fabric-list attribute mutations.
func (o *OperationalCredentials) MatterDataVersion() uint32 { return o.dataVersion.Current() }

// FabricDescriptorStruct mirrors Matter §11.18.5.6.
type FabricDescriptorStruct struct {
	RootPublicKey            []byte
	VendorID                 uint16
	FabricID                 uint64
	NodeID                   uint64
	Label                    string
	VidVerificationStatement []byte // optional (field 0x06, max 85 bytes); nil when not set
	FabricIndex              uint8
}

// NOCStruct mirrors Matter §11.18.5.7.
// Vvsc (Tag 3) is the VID Verification Signed Credential blob; nil when no
// VID-Verification Statement has been installed for this fabric via
// SetVidVerificationStatement.
type NOCStruct struct {
	NOC         []byte
	ICAC        []byte
	Vvsc        []byte // optional; nil when not set
	FabricIndex uint8
}

// MatterRead implements [interfaces.MatterClusterServer].
func (o *OperationalCredentials) MatterRead(attrID uint32) (any, bool) {
	ctx := context.Background()
	switch attrID {
	case opcredsAttrNOCs:
		fabrics, err := o.store.ListFabrics(ctx)
		if err != nil {
			return nil, false
		}
		out := make([]NOCStruct, 0, len(fabrics))
		for _, f := range fabrics {
			id, err := o.store.GetIdentity(ctx, f.FabricIndex)
			if err != nil {
				continue
			}
			out = append(out, NOCStruct{
				NOC:         id.NOC,
				ICAC:        id.ICAC,
				FabricIndex: f.FabricIndex,
			})
		}
		return out, true
	case opcredsAttrFabrics:
		fabrics, err := o.store.ListFabrics(ctx)
		if err != nil {
			return nil, false
		}
		out := make([]FabricDescriptorStruct, 0, len(fabrics))
		for _, f := range fabrics {
			out = append(out, FabricDescriptorStruct{
				RootPublicKey: f.RootPublicKey,
				VendorID:      f.VendorID,
				FabricID:      f.FabricID,
				NodeID:        f.NodeID,
				Label:         f.Label,
				FabricIndex:   f.FabricIndex,
			})
		}
		return out, true
	case opcredsAttrSupportedFabrics:
		// Return the configured value (clamped to the spec floor of 5
		// in NewOperationalCredentials). The previous hardcoded 16 was
		// superseded now that OpcredsConfig.SupportedFabrics is stored
		// on the struct. Default in cfg: 5 (Matter §11.18.6.4 floor).
		return o.supportedFabrics, true
	case opcredsAttrCommissionedFabrics:
		fabrics, err := o.store.ListFabrics(ctx)
		if err != nil {
			return nil, false
		}
		//nolint:gosec // fabric count capped at 254 by SupportedFabrics
		return uint8(len(fabrics)), true
	case opcredsAttrTrustedRootCertificates:
		// Matter §11.18.5.13: list<octet_string<400>> where each entry is
		// the full Matter Certificate TLV envelope (the RCAC bytes the
		// commissioner sent via AddTrustedRootCertificate). Mirrors
		// matter.js OperationalCredentialsServer.ts:457-459. Apple Home
		// validates every entry as a Matter Certificate TLV and silently
		// drops the entire Subscribe-Initial ReportData stream on schema
		// mismatch — see Bug-I memory.
		//
		// Legacy fabric rows (persisted before migration 012) carry
		// RootCert == nil; those are omitted from the list rather than
		// re-served as the 65-byte EC pubkey, which would re-trigger
		// Bug I. Affected commissioners must re-pair.
		//
		// Include the pending root (set by AddTrustedRootCertificate but not
		// yet committed by AddNOC) so a second commissioner reading 0x0004
		// between AddTrustedRootCertificate and AddNOC sees the same view as
		// matter.js. matter.js reference: OperationalCredentialsServer.ts:458-459
		// pushes rootCaCertificate into state.trustedRootCertificates
		// immediately; openccu-loom mirrors by prepending pendingTrustRootDER
		// to the live list when it is non-nil.
		fabrics, err := o.store.ListFabrics(ctx)
		if err != nil {
			return nil, false
		}
		o.mu.RLock()
		pending := o.pendingTrustRootDER
		o.mu.RUnlock()
		out := make([][]byte, 0, len(fabrics)+1)
		// Prepend pending root (pre-AddNOC window) per matter.js semantics.
		if len(pending) > 0 {
			out = append(out, append([]byte(nil), pending...))
		}
		for _, f := range fabrics {
			if len(f.RootCert) == 0 {
				continue
			}
			out = append(out, append([]byte(nil), f.RootCert...))
		}
		return out, true
	case opcredsAttrCurrentFabricIndex:
		o.mu.RLock()
		current := o.currentFabric
		o.mu.RUnlock()
		return current, true
	case cluster.AttrGlobalFeatureMap:
		return uint32(0), true
	case cluster.AttrGlobalClusterRevision:
		return opcredsClusterRevision, true
	// Global attributes 0xFFF8–0xFFFB: Apple Home caches
	// GeneratedCommandList / AcceptedCommandList / EventList /
	// AttributeList during the initial subscribe sweep and marks the
	// cluster unknown when they return UnsupportedAttribute. Adding these
	// cases mirrors matter.js ClusterServer auto-populated globalAttributes
	// (packages/node/src/behaviors/ClusterBehavior.ts) and chip
	// endpoint_config.h cluster metadata tables.
	case cluster.AttrGlobalGeneratedCommandList:
		// OpCreds generated commands: AttestationResponse (0x01),
		// CertificateChainResponse (0x03), CSRResponse (0x05),
		// NOCResponse (0x08), SignVidVerificationResponse (0x0E).
		return []uint32{
			opcredsCmdAttestationResponse,         // 0x01
			opcredsCmdCertificateChainResponse,    // 0x03
			opcredsCmdCSRResponse,                 // 0x05
			opcredsCmdNOCResponse,                 // 0x08
			opcredsCmdSignVidVerificationResponse, // 0x0E
		}, true
	case cluster.AttrGlobalAcceptedCommandList:
		// OpCreds accepted commands per Matter §11.18.
		// SetVidVerificationStatement (0x0C) and SignVidVerificationRequest
		// (0x0D) are mandatory in the current cluster revision.
		return []uint32{
			opcredsCmdAttestationRequest,          // 0x00
			opcredsCmdCertificateChainRequest,     // 0x02
			opcredsCmdCSRRequest,                  // 0x04
			opcredsCmdAddNOC,                      // 0x06
			opcredsCmdUpdateNOC,                   // 0x07
			opcredsCmdUpdateFabricLabel,           // 0x09
			opcredsCmdRemoveFabric,                // 0x0A
			opcredsCmdAddTrustedRootCertificate,   // 0x0B
			opcredsCmdSetVidVerificationStatement, // 0x0C
			opcredsCmdSignVidVerificationRequest,  // 0x0D
		}, true
	case cluster.AttrGlobalEventList:
		// OpCreds has no events per matter.js operational-credentials.
		// element.ts. Apple iOS 26 suppresses EventList (by-design PFAD-
		// ASYMMETRIE) but the attribute must be served for non-Apple
		// commissioners that do not suppress it.
		return []uint32{}, true
	case cluster.AttrGlobalAttributeList:
		// Full attribute list per Matter §11.18.4 + global attrs.
		return []uint32{
			opcredsAttrNOCs,                        // 0x0000
			opcredsAttrFabrics,                     // 0x0001
			opcredsAttrSupportedFabrics,            // 0x0002
			opcredsAttrCommissionedFabrics,         // 0x0003
			opcredsAttrTrustedRootCertificates,     // 0x0004
			opcredsAttrCurrentFabricIndex,          // 0x0005
			cluster.AttrGlobalFeatureMap,           // 0xFFFC
			cluster.AttrGlobalClusterRevision,      // 0xFFFD
			cluster.AttrGlobalGeneratedCommandList, // 0xFFF8
			cluster.AttrGlobalAcceptedCommandList,  // 0xFFF9
			cluster.AttrGlobalEventList,            // 0xFFFA
			cluster.AttrGlobalAttributeList,        // 0xFFFB
		}, true
	}
	return nil, false
}

// MatterReadFiltered implements [interfaces.FabricScopedReader].
// When the request carries FabricFiltered=true and a non-zero
// FabricIndex, fabric-sensitive list attributes (Fabrics, NOCs) are
// projected to only the entries owned by the requesting fabric.
// Non-fabric attributes fall through to (nil, false) so that MatterRead
// handles them on the regular path.
//
// Mirrors matter.js OperationalCredentialsServer.ts:fabrics getter —
// packages/node/src/behaviors/operational-credentials/
// OperationalCredentialsServer.ts — which returns a filtered list when
// fabric-filtered is true.
func (o *OperationalCredentials) MatterReadFiltered(ctx context.Context, attrID uint32) (any, bool) {
	filtered, fabricIndex := im.FabricFilterFromContext(ctx)
	// CurrentFabricIndex per Matter §11.18.6.6 is the FabricIndex of
	// the requesting session. On CASE (fabricIndex > 0) we MUST return
	// the session-fabric — Apple Multi-Admin Hub#1 reads on its CASE
	// session 1 and expects `1`, not Hub#2's `2` (which the global
	// `o.currentFabric` would leak after a second AddNOC). On PASE
	// (fabricIndex == 0) we fall through to MatterRead → o.currentFabric,
	// which AddNOC stamps on success — Apple's Hub#1 reads PASE right
	// after AddNOC to confirm the install and expects the fresh
	// fabric_index.
	if attrID == opcredsAttrCurrentFabricIndex && fabricIndex != 0 {
		return fabricIndex, true
	}
	// When no filter is active (PASE / fabricIndex==0 / FabricFiltered=false),
	// fall through so the unfiltered MatterRead path serves the attribute.
	if !filtered || fabricIndex == 0 {
		return o.MatterRead(attrID) //nolint:contextcheck // MatterRead is the unfiltered cluster-interface read; it takes no ctx by the Matter cluster-server contract
	}

	switch attrID {
	case opcredsAttrFabrics:
		fabrics, err := o.store.ListFabrics(ctx)
		if err != nil {
			return nil, false
		}
		out := make([]FabricDescriptorStruct, 0, 1)
		for _, f := range fabrics {
			if f.FabricIndex != fabricIndex {
				continue // filter to requesting fabric per Matter §11.18.6.5
			}
			out = append(out, FabricDescriptorStruct{
				RootPublicKey: f.RootPublicKey,
				VendorID:      f.VendorID,
				FabricID:      f.FabricID,
				NodeID:        f.NodeID,
				Label:         f.Label,
				FabricIndex:   f.FabricIndex,
			})
		}
		return out, true
	case opcredsAttrNOCs:
		fabrics, err := o.store.ListFabrics(ctx)
		if err != nil {
			return nil, false
		}
		out := make([]NOCStruct, 0, 1)
		for _, f := range fabrics {
			if f.FabricIndex != fabricIndex {
				continue
			}
			id, err := o.store.GetIdentity(ctx, f.FabricIndex)
			if err != nil {
				continue
			}
			out = append(out, NOCStruct{
				NOC:         id.NOC,
				ICAC:        id.ICAC,
				FabricIndex: f.FabricIndex,
			})
		}
		return out, true
	default:
		// All other attributes (scalars, TrustedRootCertificates, …) are
		// not fabric-scoped per Matter §11.18; forward to MatterRead.
		return o.MatterRead(attrID) //nolint:contextcheck // MatterRead is the unfiltered cluster-interface read; it takes no ctx by the Matter cluster-server contract
	}
}

// MatterWrite always rejects — every state change goes through commands.
func (o *OperationalCredentials) MatterWrite(_ context.Context, attrID uint32, _ any, _ hmenum.CommandPriority) error {
	return fmt.Errorf("matter: OperationalCredentials is read-only via attributes (got 0x%04X)", attrID)
}

// Request / response payload structs.

// AttestationRequest fields (Matter §11.18.7.1).
type AttestationRequest struct {
	AttestationNonce []byte // 32 bytes
}

// AttestationResponse (Matter §11.18.7.2).
type AttestationResponse struct {
	AttestationElements  []byte // TLV-encoded
	AttestationSignature []byte // 64-byte ECDSA r||s over (elements || attestation_challenge)
}

// CertificateChainRequest (Matter §11.18.7.3).
type CertificateChainRequest struct {
	CertificateType uint8 // 1=DAC, 2=PAI
}

// CertificateChainResponse (Matter §11.18.7.4).
type CertificateChainResponse struct {
	Certificate []byte
}

// CSRRequest (Matter §11.18.7.5).
type CSRRequest struct {
	CSRNonce       []byte // 32 bytes
	IsForUpdateNOC bool
}

// CSRResponse (Matter §11.18.7.6).
type CSRResponse struct {
	NOCSRElements        []byte // TLV-encoded { csr, csr_nonce, vendor_reserved* }
	AttestationSignature []byte
}

// AddNOCRequest (Matter §11.18.7.7).
type AddNOCRequest struct {
	NOCValue         []byte
	ICACValue        []byte
	IPKValue         []byte // 16 bytes
	CaseAdminSubject uint64
	AdminVendorID    uint16
}

// UpdateNOCRequest (Matter §11.18.7.8).
type UpdateNOCRequest struct {
	NOCValue  []byte
	ICACValue []byte
}

// NOCResponse (Matter §11.18.7.9).
type NOCResponse struct {
	StatusCode  uint8
	FabricIndex uint8
	DebugText   string
}

// UpdateFabricLabelRequest (Matter §11.18.7.10).
type UpdateFabricLabelRequest struct {
	Label string
}

// RemoveFabricRequest (Matter §11.18.7.11).
type RemoveFabricRequest struct {
	FabricIndex uint8
}

// AddTrustedRootCertificateRequest (Matter §11.18.7.12).
type AddTrustedRootCertificateRequest struct {
	RootCACertificate []byte
}

// SetVidVerificationStatementRequest (Matter §11.18.7.13, command 0x0C).
// The controller may supply a VendorId, a VidVerificationStatement (max 85
// bytes), or a Vvsc (VID Verification Signed Credential, max 400 bytes).
// All three fields are optional per the "O.a+" conformance rule.
type SetVidVerificationStatementRequest struct {
	VendorID                 uint16
	VidVerificationStatement []byte // max 85 bytes
	Vvsc                     []byte // max 400 bytes
}

// SignVidVerificationRequest (Matter §11.18.7.14, command 0x0D).
type SignVidVerificationRequest struct {
	FabricIndex     uint8
	ClientChallenge []byte // 32 bytes
}

// SignVidVerificationResponse (Matter §11.18.7.15, command 0x0E).
type SignVidVerificationResponse struct {
	FabricIndex          uint8
	FabricBindingVersion uint8
	Signature            []byte
}

// MatterInvoke implements [interfaces.MatterClusterServer].
func (o *OperationalCredentials) MatterInvoke(ctx context.Context, cmdID uint32, fields any, _ hmenum.CommandPriority) (any, error) {
	cmdName := opcredsCmdName(cmdID)
	slog.Default().Info("matter.opcreds.cmd",
		slog.String("cmd", cmdName),
		slog.String("cmdID", fmt.Sprintf("0x%02X", cmdID)))
	resp, err := o.dispatchCmd(ctx, cmdID, fields)
	if err != nil {
		slog.Default().Warn("matter.opcreds.cmd_err",
			slog.String("cmd", cmdName),
			slog.String("err", err.Error()))
	} else {
		slog.Default().Info("matter.opcreds.cmd_ok",
			slog.String("cmd", cmdName),
			slog.String("respType", fmt.Sprintf("%T", resp)))
	}
	return resp, err
}

func (o *OperationalCredentials) dispatchCmd(ctx context.Context, cmdID uint32, fields any) (any, error) {
	switch cmdID {
	case opcredsCmdAttestationRequest:
		return o.handleAttestationRequest(fields)
	case opcredsCmdCertificateChainRequest:
		return o.handleCertificateChainRequest(fields)
	case opcredsCmdCSRRequest:
		return o.handleCSRRequest(ctx, fields)
	case opcredsCmdAddNOC:
		return o.handleAddNOC(ctx, fields)
	case opcredsCmdUpdateNOC:
		return o.handleUpdateNOC(ctx, fields)
	case opcredsCmdUpdateFabricLabel:
		return o.handleUpdateFabricLabel(ctx, fields)
	case opcredsCmdRemoveFabric:
		return o.handleRemoveFabric(ctx, fields)
	case opcredsCmdAddTrustedRootCertificate:
		return o.handleAddTrustedRootCertificate(fields)
	case opcredsCmdSetVidVerificationStatement:
		return o.handleSetVidVerificationStatement(ctx, fields)
	case opcredsCmdSignVidVerificationRequest:
		return o.handleSignVidVerificationRequest(ctx, fields)
	}
	return nil, fmt.Errorf("matter: OperationalCredentials command 0x%02X not supported", cmdID)
}

func opcredsCmdName(cmdID uint32) string {
	switch cmdID {
	case opcredsCmdAttestationRequest:
		return "AttestationRequest"
	case opcredsCmdCertificateChainRequest:
		return "CertificateChainRequest"
	case opcredsCmdCSRRequest:
		return "CSRRequest"
	case opcredsCmdAddNOC:
		return "AddNOC"
	case opcredsCmdUpdateNOC:
		return "UpdateNOC"
	case opcredsCmdUpdateFabricLabel:
		return "UpdateFabricLabel"
	case opcredsCmdRemoveFabric:
		return "RemoveFabric"
	case opcredsCmdAddTrustedRootCertificate:
		return "AddTrustedRootCertificate"
	case opcredsCmdSetVidVerificationStatement:
		return "SetVidVerificationStatement"
	case opcredsCmdSignVidVerificationRequest:
		return "SignVidVerificationRequest"
	default:
		return fmt.Sprintf("Cmd0x%02X", cmdID)
	}
}

// MatterReportable lists subscribe-able attributes.
func (o *OperationalCredentials) MatterReportable() []uint32 {
	return []uint32{opcredsAttrFabrics, opcredsAttrCommissionedFabrics, opcredsAttrCurrentFabricIndex}
}

// MatterAttributes implements [interfaces.MatterClusterAttributeLister]
// so wildcard subscribe enumerates every attribute. Apple Home reads
// the full set during HAP-service construction — without NOCs +
// SupportedFabrics + TrustedRootCertificates Apple cannot validate
// the fabric-credentials chain.
//
// Global attributes 0xFFF8–0xFFFB included so Apple's initial subscribe
// sweep can cache GeneratedCommandList, AcceptedCommandList, EventList
// and AttributeList for cluster 0x3E.
func (o *OperationalCredentials) MatterAttributes() []uint32 {
	return []uint32{
		opcredsAttrNOCs,
		opcredsAttrFabrics,
		opcredsAttrSupportedFabrics,
		opcredsAttrCommissionedFabrics,
		opcredsAttrTrustedRootCertificates,
		opcredsAttrCurrentFabricIndex,
		cluster.AttrGlobalFeatureMap,
		cluster.AttrGlobalClusterRevision,
		cluster.AttrGlobalGeneratedCommandList,
		cluster.AttrGlobalAcceptedCommandList,
		cluster.AttrGlobalEventList,
		cluster.AttrGlobalAttributeList,
	}
}

// MatterAcceptedCommands implements [interfaces.MatterClusterCommandLister].
// Lists the command IDs the server handles via MatterInvoke. Only commands
// whose handlers are implemented in dispatchCmd are included.
//
// Note: UpdateNOC (0x07) is included — it is implemented (handleUpdateNOC).
// SetVidVerificationStatement (0x0C) and SignVidVerificationRequest (0x0D)
// are mandatory per the cluster schema and return InvalidCommand when
// VID-Verification mode is not supported.
func (o *OperationalCredentials) MatterAcceptedCommands() []uint32 {
	return []uint32{
		opcredsCmdAttestationRequest,          // 0x00
		opcredsCmdCertificateChainRequest,     // 0x02
		opcredsCmdCSRRequest,                  // 0x04
		opcredsCmdAddNOC,                      // 0x06
		opcredsCmdUpdateNOC,                   // 0x07
		opcredsCmdUpdateFabricLabel,           // 0x09
		opcredsCmdRemoveFabric,                // 0x0A
		opcredsCmdAddTrustedRootCertificate,   // 0x0B
		opcredsCmdSetVidVerificationStatement, // 0x0C
		opcredsCmdSignVidVerificationRequest,  // 0x0D
	}
}

// MatterGeneratedCommands implements [interfaces.MatterClusterCommandLister].
// Lists the response command IDs this server may emit.
func (o *OperationalCredentials) MatterGeneratedCommands() []uint32 {
	return []uint32{
		opcredsCmdAttestationResponse,         // 0x01
		opcredsCmdCertificateChainResponse,    // 0x03
		opcredsCmdCSRResponse,                 // 0x05
		opcredsCmdNOCResponse,                 // 0x08
		opcredsCmdSignVidVerificationResponse, // 0x0E
	}
}

func (o *OperationalCredentials) handleAttestationRequest(fields any) (any, error) {
	req, ok := fields.(AttestationRequest)
	if !ok {
		return nil, fmt.Errorf("%w: AttestationRequest expected, got %T", errOpcredsInvalidArg, fields)
	}
	if len(req.AttestationNonce) != 32 {
		return nil, fmt.Errorf("%w: AttestationNonce length=%d (want 32)", errOpcredsInvalidArg, len(req.AttestationNonce))
	}
	o.mu.RLock()
	cd := o.cdBytes
	dacKey := o.devAttestKey
	challenge := o.attestationChalleng
	o.mu.RUnlock()

	elements := encodeAttestationElements(cd, req.AttestationNonce)
	sig, err := signAttestationPayload(dacKey, elements, challenge)
	if err != nil {
		return nil, fmt.Errorf("matter: AttestationRequest sign: %w", err)
	}
	return AttestationResponse{
		AttestationElements:  elements,
		AttestationSignature: sig,
	}, nil
}

// encodeAttestationElements assembles the AttestationElements TLV per
// Matter §11.18.4.7 — anonymous-tagged Structure with three or four fields:
//
//	[1] CertificationDeclaration (octets)
//	[2] AttestationNonce         (octets, 32 bytes)
//	[3] Timestamp                (uint32, fixed-width 4 bytes; 0 per matter.js
//	                              OperationalCredentialsServer.ts:109)
//	[4] FirmwareInformation      (octets, optional — omitted when empty per
//	                              matter.js protocol/src/common/
//	                              OperationalCredentialsTypes.ts:14)
//
// The bytes returned are the inner Structure encoding (no outer
// wrapper); chip-tool's `DataModel/Attestation.cpp` decodes them
// directly.
func encodeAttestationElements(cd, nonce []byte) []byte {
	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutOctets(tlv.ContextTag(1), cd)
	enc.PutOctets(tlv.ContextTag(2), nonce)
	// Matter §11.18.4.7 — Timestamp is uint32 epoch-seconds. PutUint
	// auto-downsizes a value < 65536 to uint16 (or uint8); chip-tool's
	// AttestationVerifier reads exactly 4 bytes per the spec schema and
	// rejects a narrower encoding with `WrongTLVType`. matter.js sets
	// timestamp to 0; wire-byte parity requires PutUint32 with value 0.
	enc.PutUint32(tlv.ContextTag(3), 0)
	// FirmwareInformation (field 4) is TlvOptionalField per matter.js
	// OperationalCredentialsTypes.ts:14. matter.js omits it when empty;
	// emitting an empty octstr produces a spurious TLV field that some
	// strict decoders flag. Omit field 4 entirely (no PutOctets call).
	_ = enc.EndContainer()
	out, _ := enc.Bytes()
	return out
}

// signAttestationPayload computes ECDSA-SHA256(elements || challenge)
// and returns the raw r || s 64-byte signature shape Matter §3.5.5
// requires. dac may be nil — callers handle the missing-key case as
// "test-only daemon, attestation will not validate".
func signAttestationPayload(dac *ecdsa.PrivateKey, elements, challenge []byte) ([]byte, error) {
	if dac == nil {
		// No key wired — return a 64-byte zero-stub so the response
		// is wire-shaped. chip-tool with --bypass-attestation-verifier
		// accepts it; production deployments must wire a real DAC key.
		return make([]byte, 64), nil
	}
	h := sha256.New()
	h.Write(elements)
	h.Write(challenge)
	digest := h.Sum(nil)
	r, s, err := ecdsa.Sign(rand.Reader, dac, digest)
	if err != nil {
		return nil, err
	}
	return rawSignature(r, s), nil
}

// rawSignature serialises (r, s) into the 64-byte raw form Matter
// uses on the wire (32 bytes r || 32 bytes s, big-endian, zero-padded).
func rawSignature(r, s *big.Int) []byte {
	out := make([]byte, 64)
	rb := r.Bytes()
	sb := s.Bytes()
	copy(out[32-len(rb):32], rb)
	copy(out[64-len(sb):64], sb)
	return out
}

func (o *OperationalCredentials) handleCertificateChainRequest(fields any) (any, error) {
	req, ok := fields.(CertificateChainRequest)
	if !ok {
		return nil, fmt.Errorf("%w: CertificateChainRequest expected, got %T", errOpcredsInvalidArg, fields)
	}
	o.mu.RLock()
	dac := o.dacBytes
	pai := o.paiBytes
	o.mu.RUnlock()
	switch req.CertificateType {
	case CertChainTypeDAC:
		return CertificateChainResponse{Certificate: dac}, nil
	case CertChainTypePAI:
		return CertificateChainResponse{Certificate: pai}, nil
	default:
		return nil, fmt.Errorf("%w: CertificateType=%d", errOpcredsInvalidArg, req.CertificateType)
	}
}

func (o *OperationalCredentials) handleCSRRequest(ctx context.Context, fields any) (any, error) {
	req, ok := fields.(CSRRequest)
	if !ok {
		return nil, fmt.Errorf("%w: CSRRequest expected, got %T", errOpcredsInvalidArg, fields)
	}
	if len(req.CSRNonce) != 32 {
		return nil, fmt.Errorf("%w: CSRNonce length=%d (want 32)", errOpcredsInvalidArg, len(req.CSRNonce))
	}
	// Mirrors matter.js OperationalCredentialsServer.ts:126-130 —
	// `IsForUpdateNOC=true` with a PASE session is illegal; UpdateNOC
	// is a CASE-only operation that re-keys an existing fabric. PASE
	// is the bootstrap channel and only AddNOC is valid over it.
	_, sessFabric := im.FabricFilterFromContext(ctx)
	if req.IsForUpdateNOC && sessFabric == 0 {
		return nil, fmt.Errorf("matter: CSRRequest: invalid command argument: IsForUpdateNOC requires CASE session (got PASE)")
	}
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("matter: CSRRequest: generate key: %w", err)
	}
	csr, err := buildPKCS10CSR(priv)
	if err != nil {
		return nil, fmt.Errorf("matter: CSRRequest: build CSR: %w", err)
	}
	nocsrElements := encodeNOCSRElements(csr, req.CSRNonce)

	// Store the session ID of this CSRRequest so handleAddNOC can enforce
	// that AddNOC is issued from the same session per Matter §11.18.7.5.5
	// (matter.js OperationalCredentialsServer.ts:230-235; chip
	// operational-credentials-server.cpp CSR failsafe binding).
	csrSessionID := InvokeSessionIDFromContext(ctx)

	o.mu.Lock()
	o.pendingPrivKey = priv
	o.pendingCSRNonce = append([]byte(nil), req.CSRNonce...)
	o.pendingCSRSessionID = csrSessionID
	// Capture the IsForUpdateNOC flag so the follow-up AddNOC/UpdateNOC
	// handler can verify that the CSR was produced for the matching command.
	// Without this both commands happily consume any pending CSR.
	o.pendingCSRForUpdate = req.IsForUpdateNOC
	dacKey := o.devAttestKey
	challenge := append([]byte(nil), o.attestationChalleng...)
	o.mu.Unlock()

	sig, err := signAttestationPayload(dacKey, nocsrElements, challenge)
	if err != nil {
		return nil, fmt.Errorf("matter: CSRRequest: sign: %w", err)
	}
	return CSRResponse{
		NOCSRElements:        nocsrElements,
		AttestationSignature: sig,
	}, nil
}

// buildPKCS10CSR creates a minimal DER-encoded PKCS#10 certification
// request with `priv` as the subject key. Subject DN carries the
// fixed Matter-conventional `CN=CSR` placeholder; the CA replaces
// it with the operational subject DN when issuing the NOC. Uses
// ECDSA-SHA256 per Matter §6.4.1.2.
func buildPKCS10CSR(priv *ecdsa.PrivateKey) ([]byte, error) {
	tmpl := x509.CertificateRequest{
		Subject:            pkix.Name{CommonName: "CSR"},
		SignatureAlgorithm: x509.ECDSAWithSHA256,
	}
	return x509.CreateCertificateRequest(rand.Reader, &tmpl, priv)
}

// encodeNOCSRElements assembles the NOCSRElements TLV per Matter
// §11.18.7.6 — anonymous-tagged Structure with two octet fields:
//
//	[1] CSR        (DER-encoded PKCS#10)
//	[2] CSRNonce   (32 bytes, echoed from request)
func encodeNOCSRElements(csr, nonce []byte) []byte {
	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutOctets(tlv.ContextTag(1), csr)
	enc.PutOctets(tlv.ContextTag(2), nonce)
	_ = enc.EndContainer()
	out, _ := enc.Bytes()
	return out
}

// _ swallow unused-import false-positives when only specific helpers
// from `asn1` get exercised in tests.
var _ = asn1.NullBytes

// computeCompressedFabricID derives the 8-byte compressed fabric ID
// per Matter §4.13.2.4. salt = fabricID big-endian, IKM = rootPubKey
// without the 0x04 prefix, info = "CompressedFabric", L = 8.
//
// Inlined here (instead of importing internal/.../fabric) to avoid
// the cluster package taking a dependency on the fabric package
// — the fabric package historically pulled in the broader Matter
// runtime stack.
func computeCompressedFabricID(rootPubKey []byte, fabricID uint64) ([]byte, error) {
	if len(rootPubKey) != 65 || rootPubKey[0] != 0x04 {
		return nil, fmt.Errorf("compressed fabric id: invalid root pub key (len=%d)", len(rootPubKey))
	}
	var salt [8]byte
	salt[0] = byte(fabricID >> 56) //nolint:gosec // G115: big-endian byte extraction; each shift result fits uint8
	salt[1] = byte(fabricID >> 48) //nolint:gosec // G115: big-endian byte extraction; each shift result fits uint8
	salt[2] = byte(fabricID >> 40) //nolint:gosec // G115: big-endian byte extraction; each shift result fits uint8
	salt[3] = byte(fabricID >> 32) //nolint:gosec // G115: big-endian byte extraction; each shift result fits uint8
	salt[4] = byte(fabricID >> 24) //nolint:gosec // G115: big-endian byte extraction; each shift result fits uint8
	salt[5] = byte(fabricID >> 16) //nolint:gosec // G115: big-endian byte extraction; each shift result fits uint8
	salt[6] = byte(fabricID >> 8)  //nolint:gosec // G115: big-endian byte extraction; each shift result fits uint8
	salt[7] = byte(fabricID)       //nolint:gosec // G115: big-endian byte extraction; each shift result fits uint8
	out, err := hkdfSHA256(rootPubKey[1:], salt[:], []byte("CompressedFabric"), 8)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// hkdfSHA256 wraps the stdlib `crypto/hkdf.Key` so the call site
// stays terse.
func hkdfSHA256(ikm, salt, info []byte, length int) ([]byte, error) {
	return hkdfPkg.Key(sha256.New, ikm, salt, string(info), length)
}

func (o *OperationalCredentials) handleAddNOC(ctx context.Context, fields any) (any, error) {
	req, ok := fields.(AddNOCRequest)
	if !ok {
		return nil, fmt.Errorf("%w: AddNOCRequest expected, got %T", errOpcredsInvalidArg, fields)
	}
	// Matter §11.18.6.8 requires Status::FailsafeRequired when AddNOC is
	// invoked outside an armed FailSafe window. Mirrors chip
	// operational-credentials-server.cpp:399 and matter.js
	// OperationalCredentialsServer.ts:218 #failsafeContext check.
	o.mu.RLock()
	checkArmed := o.isFailSafeArmed
	o.mu.RUnlock()
	if checkArmed != nil && !checkArmed() {
		return nil, errOpcredsFailsafeRequired
	}
	if len(req.IPKValue) != 16 {
		return NOCResponse{StatusCode: NOCStatusInvalidNOC, DebugText: "IPK length"}, nil
	}
	// NOC and ICAC byte-length caps per Matter §11.18.6.7 and chip
	// operational-credentials-server.cpp kMaxCHIPCertLength (400 bytes).
	// Oversized certificates indicate a malformed or non-Matter cert;
	// reject early so the TLV decoder never sees unbounded input.
	if len(req.NOCValue) > 400 {
		return NOCResponse{StatusCode: NOCStatusInvalidNOC, DebugText: "NOC exceeds 400-byte limit"}, nil
	}
	if len(req.ICACValue) > 400 {
		return NOCResponse{StatusCode: NOCStatusInvalidNOC, DebugText: "ICAC exceeds 400-byte limit"}, nil
	}

	// Mirrors matter.js OperationalCredentialsServer.ts:218-251.
	// Guard ordering: CSR-pending → trust-root → TableFull →
	// cert validation. Each failure is reported via
	// NOCResponse.StatusCode (in-band) per Matter §11.18.6.7,
	// NOT via the IM error channel.
	//
	// Apple's Multi-Admin flow legitimately invokes AddNOC over an
	// existing CASE session (Hub#1 facilitates Hub#2's commissioning).
	// matter.js makes no PASE-only check on AddNOC for this reason —
	// the only session-related guard would be `csrSessionId !==
	// session.id` (MissingCsr), which the pendingPrivKey == nil
	// branch covers below.
	// Enforce CSR session-ID binding per Matter §11.18.7.5.5 — reject
	// AddNOC from a session that did not issue the pending CSRRequest.
	// Mirrors matter.js OperationalCredentialsServer.ts:230-235:
	//   if (failsafeContext.csrSessionId !== this.context.session.id)
	//       return { statusCode: MissingCsr, debugText: "CSR not found in failsafe context" }
	//
	// chip: operational-credentials-server.cpp CSR failsafe context check.
	//
	// NOTE: When the context carries sessionID==0 (PASE / test paths that
	// have not been migrated to WithInvokeSessionID) we skip the check —
	// two zero-IDs would always match, so this is safe for existing callers
	// while still enforcing the guard on CASE sessions (sessionID > 0).
	addNOCSessionID := InvokeSessionIDFromContext(ctx)

	o.mu.Lock()
	priv := o.pendingPrivKey
	pendingCSRSessID := o.pendingCSRSessionID
	pendingForUpdate := o.pendingCSRForUpdate
	root := o.pendingTrustRoot
	rootDER := o.pendingTrustRootDER
	supportedFabrics := o.supportedFabrics
	o.mu.Unlock()
	if priv == nil {
		return NOCResponse{StatusCode: NOCStatusMissingCsr, DebugText: "CSR not issued"}, nil
	}
	// Session-ID mismatch: a non-zero AddNOC session must match the CSR
	// session. Both zero ⇒ PASE / unset context ⇒ skip (backwards compatible).
	if addNOCSessionID != 0 && pendingCSRSessID != 0 && addNOCSessionID != pendingCSRSessID {
		return NOCResponse{StatusCode: NOCStatusMissingCsr, DebugText: "CSR not found in failsafe context"}, nil
	}
	// A CSR issued with IsForUpdateNOC=true must NOT be consumed by AddNOC
	// — that would let a commissioner update an existing fabric's NOC via
	// the add path, skipping the fabric-authentication guard on UpdateNOC.
	// Mirrors matter.js OperationalCredentialsServer.ts:#addNoc
	// !csrIsForUpdateNoc check.
	if pendingForUpdate {
		return NOCResponse{StatusCode: NOCStatusMissingCsr, DebugText: "CSR was issued for UpdateNOC"}, nil
	}
	if len(root) == 0 {
		return NOCResponse{StatusCode: NOCStatusInvalidNOC, DebugText: "no trusted root"}, nil
	}
	if !isOperationalAdminVendorID(req.AdminVendorID) {
		// chip OperationalCredentialsCluster.cpp:437
		// `IsVendorIdValidOperationally(adminVendorId)` rejects 0 / 0xFFFF
		// (anonymous) and the test-VID range 0xFFF1..0xFFF4 outside lab
		// use. Apple's commissioner mirrors the guard; without it a
		// malformed VID is accepted into the ACL admin-subject and the
		// next CASE lookup fails silently. Placed after the CSR /
		// trust-root checks so a missing CSR still surfaces as
		// MissingCsr (chip orders this guard earlier; the failure
		// surface to Apple is identical either way).
		return NOCResponse{StatusCode: NOCStatusInvalidAdminSubject, DebugText: "AdminVendorID invalid"}, nil
	}
	// TableFull-Guard before persisting (matter.js
	// OperationalCredentialsServer.ts:244-250). Without this, the
	// store fails the AddFabric INSERT and we'd surface FabricConflict
	// — semantically wrong: the fabric is not in conflict, the table
	// is exhausted.
	if existing, lerr := o.store.ListFabrics(ctx); lerr == nil && uint8(len(existing)) >= supportedFabrics { //nolint:gosec // ListFabrics returns ≤ supportedFabrics by spec
		return NOCResponse{
			StatusCode: NOCStatusTableFull,
			DebugText:  fmt.Sprintf("fabric table at capacity (%d/%d)", len(existing), supportedFabrics),
		}, nil
	}

	// Verify the NOC chain against the pending trust root before
	// extracting any subject fields.  The verifier checks:
	//   • cert type (IsNOC / IsICA), validity window, ECDSA signatures,
	//   • chain linkage (ICAC.Issuer == Root.Subject when ICAC present).
	// Mirrors chip OperationalCredentialsCluster.cpp:496-501 and
	// matter.js Fabric.create(root, icac, noc) → verifyCredentials().
	verifier, verr := mattercert.NewVerifier(root, nil)
	if verr != nil {
		return NOCResponse{StatusCode: NOCStatusInvalidNOC, DebugText: "trust root invalid: " + verr.Error()}, nil //nolint:nilerr // cluster-command failure encoded in NOCResponse.StatusCode, not via the IM-error channel
	}
	if _, verr = verifier.VerifyAndExtractPubKey(req.NOCValue, req.ICACValue); verr != nil {
		return NOCResponse{StatusCode: NOCStatusInvalidNOC, DebugText: "NOC chain verification failed: " + verr.Error()}, nil //nolint:nilerr // cluster-command failure encoded in NOCResponse.StatusCode, not via the IM-error channel
	}

	// Decode is safe after chain verification has passed.
	noc, err := mattercert.Decode(req.NOCValue)
	if err != nil {
		return NOCResponse{StatusCode: NOCStatusInvalidNOC, DebugText: err.Error()}, nil //nolint:nilerr // cluster-command failure encoded in NOCResponse.StatusCode, not via the IM-error channel
	}
	if !noc.IsNOC() {
		return NOCResponse{StatusCode: NOCStatusInvalidNOC, DebugText: "not a NOC"}, nil
	}

	rec := store.FabricRecord{
		FabricID:      noc.Subject.MatterFabricID,
		NodeID:        noc.Subject.MatterNodeID,
		RootPublicKey: append([]byte(nil), root...),
		RootCert:      append([]byte(nil), rootDER...),
		VendorID:      req.AdminVendorID,
		// Matter spec allows an empty Label at commission time, but
		// Apple Home reads OperationalCredentials.Fabrics right after
		// CommissioningComplete and silently sends RemoveFabric ~10 s
		// later when the entry's `Label` field is empty (post-pairing
		// cross-validation step in iCloud-Heim). matter.js servers
		// avoid this by exposing whatever the application configured
		// (see Fabric.ts:547-553); we mirror the behaviour with a
		// daemon-default label so first-pair attempts pass Apple's
		// validator before the controller has a chance to send
		// UpdateFabricLabel.
		Label: "openccu-loom",
	}
	slog.Default().Debug("matter.opcreds.addnoc.params",
		slog.String("noc_subject_fabric_id", fmt.Sprintf("0x%016X", noc.Subject.MatterFabricID)),
		slog.String("noc_subject_node_id", fmt.Sprintf("0x%016X", noc.Subject.MatterNodeID)),
		slog.String("admin_vendor_id", fmt.Sprintf("0x%04X", req.AdminVendorID)),
		slog.String("case_admin_subject", fmt.Sprintf("0x%016X", req.CaseAdminSubject)),
		slog.Int("ipk_len", len(req.IPKValue)),
		slog.Int("noc_bytes", len(req.NOCValue)),
		slog.Int("icac_bytes", len(req.ICACValue)))
	// CompressedID = HKDF-SHA256(IKM=rootPubKey[1:], salt=fabricID-BE,
	// info="CompressedFabric", L=8) per Matter §4.13.2.4. Computed
	// here so the post-AddNOC mDNS announce can stamp the right
	// instance name.
	if cid, derr := computeCompressedFabricID(rec.RootPublicKey, rec.FabricID); derr == nil {
		copy(rec.CompressedID[:], cid)
	}
	idx, err := o.store.AddFabric(ctx, rec)
	if err != nil {
		return NOCResponse{StatusCode: NOCStatusFabricConflict, DebugText: err.Error()}, nil //nolint:nilerr // cluster-command failure encoded in NOCResponse.StatusCode, not via the IM-error channel
	}
	// Record the fabric slot so FailSafe expiry can revert it when
	// CommissioningComplete is never received. Mirrors chip
	// CommissioningWindowManager::OnFailSafeTimerExpired calling
	// RevertPendingOpCertsExceptRoot on the pending fabric index.
	o.mu.Lock()
	o.pendingInstallFabricIndex = idx
	o.mu.Unlock()

	identity := store.IdentityRecord{
		FabricIndex: idx,
		NOC:         append([]byte(nil), req.NOCValue...),
		ICAC:        append([]byte(nil), req.ICACValue...),
		PrivateKey:  encodePrivateKey(priv),
		IPK:         append([]byte(nil), req.IPKValue...),
	}
	if err := o.store.UpsertIdentity(ctx, identity); err != nil {
		o.revertAddNOC(ctx, idx)
		return NOCResponse{StatusCode: NOCStatusInvalidNOC, DebugText: err.Error()}, nil //nolint:nilerr // cluster-command failure encoded in NOCResponse.StatusCode, not via the IM-error channel
	}

	// Install the IPK as epoch key in GroupKeyManagement KeySetID=0 per
	// Matter §11.18.6.8.6. chip operational-credentials-server.cpp:484-496
	// writes via groupDataProvider.SetKeySet(newFabricIndex, compressed_fabric_id,
	// keyset{id=0, policy=TrustFirst, num_keys=1, epoch_keys[0].start=0, key=ipk}).
	// matter.js notes this as a TODO (OperationalCredentialsServer.ts:293-294)
	// but embeds the IPK in Fabric.create for operational HKDF derivation;
	// we match the chip behaviour to populate the GroupKeyManagement table so
	// KeySetReadAllIndices correctly advertises the IPK slot.
	ipkRec := store.GroupKeySet{
		FabricIndex:    idx,
		GroupKeySetID:  0, // kIdentityProtectionKeySetId
		SecurityPolicy: store.SecurityPolicyTrustFirst,
		EpochKey0:      append([]byte(nil), req.IPKValue...),
		EpochStart0:    0, // sentinel: IPK epoch start is 0 per Matter §11.18.6.8.6
	}
	if err := o.store.UpsertGroupKeySet(ctx, ipkRec); err != nil {
		// Non-fatal: the identity and fabric rows are already committed;
		// log and continue so the pair does not fail over a GKM write
		// error. Matches chip's tolerant handling of secondary state.
		slog.Default().Warn("matter.opcreds.addnoc.ipk_gkm_write_failed",
			slog.Int("fabric_index", int(idx)),
			slog.String("err", err.Error()))
	}

	// Validate CaseAdminSubject range before installing the ACL.
	// Mirrors chip OperationalCredentialsCluster.cpp:452-454:
	//   VerifyOrExit(IsOperationalNodeId(caseAdminSubject) || IsCASEAuthTag(caseAdminSubject),
	//                nocResponse = kInvalidAdminSubject)
	// and matter.js AdministratorCommissioningServer.ts caseAdminSubject validation.
	// Operational Node IDs: 0x0000_0000_0000_0001 .. 0xFFFF_FFFF_FFFF_FFEF.
	// CASE Auth Tag (CAT): 0xFFFF_FFFD_0000_0000 .. 0xFFFF_FFFD_FFFF_FFFF.
	// Reserved/group/PASE ranges in between must be rejected to prevent
	// anonymous subjects gaining Administer privilege in the ACL.
	if !isOperationalNodeID(req.CaseAdminSubject) && !isCASEAuthTag(req.CaseAdminSubject) {
		return NOCResponse{StatusCode: NOCStatusInvalidAdminSubject, DebugText: "CaseAdminSubject is not a valid operational node ID or CASE auth tag"}, nil
	}

	// Per Matter Core §11.18.6.8.1 the AddNOC implementation MUST
	// install a default Access Control entry granting the
	// CaseAdminSubject Administer privilege on every cluster of every
	// endpoint. Without it the freshly-paired controller's first read
	// of AccessControl.acl returns empty / ACCESS_DENIED, and Apple
	// Home tears the fabric down via RemoveFabric immediately after
	// CommissioningComplete — surfaces in the controller UI as a generic
	// add-failed error even though the handshake completed cleanly.
	defaultACL := []store.ACLEntry{{
		FabricIndex: idx,
		Privilege:   store.PrivilegeAdminister,
		AuthMode:    store.AuthModeCASE,
		Subjects:    []uint64{req.CaseAdminSubject},
		Targets:     nil, // nil ⇒ all clusters / endpoints / device-types
		Position:    0,
	}}
	if err := o.store.ReplaceACL(ctx, idx, defaultACL); err != nil {
		o.revertAddNOC(ctx, idx)
		return NOCResponse{StatusCode: NOCStatusInvalidNOC, DebugText: err.Error()}, nil //nolint:nilerr // cluster-command failure encoded in NOCResponse.StatusCode
	}

	o.mu.Lock()
	// Mark that a NOC command was invoked so a subsequent
	// AddTrustedRootCertificate in the same FailSafe window is rejected
	// with CONSTRAINT_ERROR.
	o.nocWasInvoked = true
	o.pendingPrivKey = nil
	o.pendingTrustRoot = nil
	o.pendingTrustRootDER = nil
	o.pendingCSRNonce = nil
	o.pendingCSRForUpdate = false
	o.currentFabric = idx
	hook := o.onFabricInstalled
	o.mu.Unlock()

	// Bump DataVersion after successful AddNOC — fabric list changed.
	// Must happen AFTER all store writes succeed (AddFabric + UpsertIdentity + ReplaceACL).
	o.dataVersion.Bump()

	if hook != nil {
		hook(ctx, idx, rec.FabricID, rec.NodeID, rec.RootPublicKey)
	}

	return NOCResponse{StatusCode: NOCStatusOK, FabricIndex: idx}, nil
}

func (o *OperationalCredentials) handleUpdateNOC(ctx context.Context, fields any) (any, error) {
	req, ok := fields.(UpdateNOCRequest)
	if !ok {
		return nil, fmt.Errorf("%w: UpdateNOCRequest expected, got %T", errOpcredsInvalidArg, fields)
	}
	// Matter §11.18.6.9 requires Status::FailsafeRequired when UpdateNOC is
	// invoked outside an armed FailSafe window. Mirrors chip
	// operational-credentials-server.cpp:HandleUpdateNOC FailSafe check.
	o.mu.RLock()
	checkArmed := o.isFailSafeArmed
	priv := o.pendingPrivKey
	pendingForUpdate := o.pendingCSRForUpdate
	o.mu.RUnlock()
	if checkArmed != nil && !checkArmed() {
		return nil, errOpcredsFailsafeRequired
	}
	if priv == nil {
		return NOCResponse{StatusCode: NOCStatusMissingCsr, DebugText: "CSR not issued"}, nil
	}
	// UpdateNOC requires the pending CSR was issued with IsForUpdateNOC=true.
	// Otherwise the CSR belongs to AddNOC and must not be consumed here.
	// Mirrors matter.js OperationalCredentialsServer.ts:#updateNoc
	// csrIsForUpdateNoc check.
	if !pendingForUpdate {
		return NOCResponse{StatusCode: NOCStatusMissingCsr, DebugText: "CSR was issued for AddNOC"}, nil
	}
	// UpdateNOC is fabric-scoped (Matter §11.18.6.9 — the NOC update
	// SHALL target the fabric of the invoking session). Read the
	// FabricIndex from the IM context, NOT a globally stamped
	// currentFabric (only ever set on a successful AddNOC). Mirrors
	// matter.js OperationalCredentialsServer.ts:#updateNoc which reads
	// `session.fabric.fabricIndex`.
	_, idx := im.FabricFilterFromContext(ctx)
	if idx == 0 {
		o.mu.RLock()
		idx = o.currentFabric
		o.mu.RUnlock()
	}
	if idx == 0 {
		return NOCResponse{StatusCode: NOCStatusInvalidFabricIndex, DebugText: "no current fabric"}, nil
	}
	identity := store.IdentityRecord{
		FabricIndex: idx,
		NOC:         append([]byte(nil), req.NOCValue...),
		ICAC:        append([]byte(nil), req.ICACValue...),
		PrivateKey:  encodePrivateKey(priv),
	}
	// IPK is not changed on UpdateNOC; copy it from the existing
	// identity.
	existing, err := o.store.GetIdentity(ctx, idx)
	if err != nil {
		return NOCResponse{StatusCode: NOCStatusInvalidFabricIndex, DebugText: err.Error()}, nil //nolint:nilerr // cluster-command failure encoded in NOCResponse.StatusCode, not via the IM-error channel
	}
	identity.IPK = existing.IPK
	if err := o.store.UpsertIdentity(ctx, identity); err != nil {
		return NOCResponse{StatusCode: NOCStatusInvalidNOC, DebugText: err.Error()}, nil //nolint:nilerr // cluster-command failure encoded in NOCResponse.StatusCode, not via the IM-error channel
	}
	o.mu.Lock()
	// Mark that a NOC command was invoked so a subsequent
	// AddTrustedRootCertificate in the same FailSafe window is rejected.
	o.nocWasInvoked = true
	o.pendingPrivKey = nil
	o.pendingCSRForUpdate = false
	hook := o.onFabricUpdated
	o.mu.Unlock()
	// Bump DataVersion after successful UpdateNOC — fabric list changed.
	o.dataVersion.Bump()
	// Abort all CASE sessions for the updated fabric (except the invoking
	// one). The commissioner must re-CASE with the new NOC. Mirrors chip
	// operational-credentials-server.cpp
	// FabricTable::AbortAllOtherCommunicationOnFabric(fabricIndex) and
	// matter.js FabricManager.ts `replacing` event →
	// SessionManager.closeAllSessionsForFabricExcept(sessionID). The hook
	// is nil in test environments and bridge setups that handle session
	// cleanup elsewhere.
	if hook != nil {
		hook(ctx, idx)
	}
	return NOCResponse{StatusCode: NOCStatusOK, FabricIndex: idx}, nil
}

func (o *OperationalCredentials) handleUpdateFabricLabel(ctx context.Context, fields any) (any, error) {
	req, ok := fields.(UpdateFabricLabelRequest)
	if !ok {
		return nil, fmt.Errorf("%w: UpdateFabricLabelRequest expected, got %T", errOpcredsInvalidArg, fields)
	}
	if len(req.Label) > 32 {
		// chip OperationalCredentialsCluster.cpp:678-682 returns
		// IM-InvalidCommand (0x85) for a label that exceeds 32 bytes,
		// not NOCResponse{LabelConflict}. LabelConflict means the label
		// is already in use by another fabric; an oversized label is a
		// constraint violation that maps to InvalidCommand.
		return nil, opcredsInvalidCommandErr{"UpdateFabricLabel: label exceeds 32 UTF-8 bytes"}
	}
	// Fabric-scoped command (Matter §11.18.6.10): SHALL target the
	// fabric of the session that issued the Invoke, NOT a globally
	// stamped `currentFabric` (which is only ever set on a successful
	// AddNOC and on ACL writes — zero on every other CASE session
	// touching this cluster). Mirrors matter.js
	// OperationalCredentialsServer.ts:#updateFabricLabel where the
	// FabricIndex is read from `session.fabric.fabricIndex`.
	//
	// Strict controllers invoke UpdateFabricLabel right after CASE
	// re-connect with the human-set hub label. The pre-fix path
	// returned NOCStatusInvalidFabricIndex ("no current fabric") on
	// every retry — the controller kept retransmitting the same
	// Invoke at the MRP retry cadence because the bridge never
	// confirmed the label-write with StatusCode=OK.
	_, ctxIdx := im.FabricFilterFromContext(ctx)
	o.mu.RLock()
	curIdx := o.currentFabric
	o.mu.RUnlock()
	idx := ctxIdx
	if idx == 0 {
		idx = curIdx
	}
	slog.Default().Debug("matter.opcreds.update_fabric_label",
		slog.Int("ctx_fabric_index", int(ctxIdx)),
		slog.Int("current_fabric", int(curIdx)),
		slog.Int("resolved_idx", int(idx)),
		slog.String("label", req.Label))
	if idx == 0 {
		return NOCResponse{StatusCode: NOCStatusInvalidFabricIndex, DebugText: "no current fabric"}, nil
	}
	// Mirrors matter.js packages/node/src/behaviors/operational-credentials/
	// OperationalCredentialsServer.ts:381-388 — reject non-empty Labels
	// already used by another fabric on this node with LabelConflict.
	// Empty Labels are always allowed; the spec treats a fabric with
	// empty Label as "unnamed" and multi-fabric admins may legitimately
	// share that state.
	if req.Label != "" {
		fabrics, err := o.store.ListFabrics(ctx)
		if err == nil {
			for _, f := range fabrics {
				if f.FabricIndex != idx && f.Label == req.Label {
					return NOCResponse{
						StatusCode: NOCStatusLabelConflict,
						DebugText:  fmt.Sprintf("label %q already used by fabric %d", req.Label, f.FabricIndex),
					}, nil
				}
			}
		}
	}
	if err := o.store.UpdateFabricLabel(ctx, idx, req.Label); err != nil {
		return NOCResponse{StatusCode: NOCStatusInvalidFabricIndex, DebugText: err.Error()}, nil //nolint:nilerr // cluster-command failure encoded in NOCResponse.StatusCode, not via the IM-error channel
	}
	// Bump DataVersion after successful label update — fabric list changed.
	o.dataVersion.Bump()
	return NOCResponse{StatusCode: NOCStatusOK, FabricIndex: idx}, nil
}

func (o *OperationalCredentials) handleRemoveFabric(ctx context.Context, fields any) (any, error) {
	req, ok := fields.(RemoveFabricRequest)
	if !ok {
		return nil, fmt.Errorf("%w: RemoveFabricRequest expected, got %T", errOpcredsInvalidArg, fields)
	}
	if err := o.store.RemoveFabric(ctx, req.FabricIndex); err != nil {
		return NOCResponse{StatusCode: NOCStatusInvalidFabricIndex, DebugText: err.Error()}, nil //nolint:nilerr // cluster-command failure encoded in NOCResponse.StatusCode, not via the IM-error channel
	}

	// Explicit in-memory cleanup for the removed fabric. FK CASCADE has
	// already wiped the DB rows (matter_node_identities, matter_group_keys,
	// matter_group_key_map, matter_acl_entries). Clear any pending
	// commissioning state that belongs to this fabric so a subsequent pair
	// attempt does not reuse stale CSR / trust-root state. Mirrors matter.js
	// FabricManager.ts:241-248 #handleFabricDeleted which calls
	// fabric.storage?.clearAll() for all fabric-scoped in-memory state.
	o.mu.Lock()
	if o.currentFabric == req.FabricIndex {
		o.currentFabric = 0
		// Clear pending commissioning state that was created for this
		// fabric — pendingPrivKey/TrustRoot/CSRNonce are fabric-scoped
		// (they are consumed by a single AddNOC for the in-flight fabric).
		// If RemoveFabric is called for a fabric that was mid-commissioning
		// (fail-safe expiry path), zeroing them here prevents stale CSR
		// reuse on the next attempt. Also clears nocWasInvoked.
		o.clearPendingState()
	}
	hook := o.onFabricRemoved
	mdnsHook := o.onMDNSReannounce
	o.mu.Unlock()

	// Bump DataVersion after successful RemoveFabric — fabric list changed.
	o.dataVersion.Bump()

	if hook != nil {
		// Fires AFTER fabric persistence is gone but BEFORE we return
		// NOCResponse to the controller. The daemon wires this hook to
		// evict all operational sessions, subscriptions, and resumption
		// records bound to the removed fabric — without the eviction
		// stale state survives the fabric's death and a subsequent pair
		// retry collides on session-id 1. Mirrors matter.js
		// packages/protocol/src/fabric/FabricManager.ts:removeFabric
		// (which fans out to SessionManager + InteractionServer).
		// Concrete daemon-side fan-out: opMgr.CloseFabric + subMgr.CloseFabric
		// + store.RemoveResumptionsByFabric.
		hook(ctx, req.FabricIndex)
	}
	if mdnsHook != nil {
		// Withdraw the stale per-fabric _matter._tcp record and republish
		// the remaining fabric set so commissioners do not resolve a
		// CompressedFabricID that no longer exists. Mirrors matter.js
		// Fabric.remove() triggering MdnsServer.reannounceInstance.
		mdnsHook(ctx)
	}
	return NOCResponse{StatusCode: NOCStatusOK, FabricIndex: req.FabricIndex}, nil
}

func (o *OperationalCredentials) handleAddTrustedRootCertificate(fields any) (any, error) {
	req, ok := fields.(AddTrustedRootCertificateRequest)
	if !ok {
		return nil, fmt.Errorf("%w: AddTrustedRootCertificateRequest expected, got %T", errOpcredsInvalidArg, fields)
	}
	// Matter §11.18.6.4 requires Status::FailsafeRequired when the cluster
	// command is invoked outside an armed FailSafe window. Mirrors chip
	// operational-credentials-server.cpp ~line 612
	// (HandleAddTrustedRootCertificate VerifyOrExit on IsFailSafeArmed).
	o.mu.RLock()
	checkArmed := o.isFailSafeArmed
	o.mu.RUnlock()
	if checkArmed != nil && !checkArmed() {
		return nil, errOpcredsFailsafeRequired
	}
	// Guards per Matter §11.18.6.4:
	//
	//   (a) pendingTrustRoot != nil → the root was already set for this
	//       FailSafe window; a second call is a CONSTRAINT_ERROR. Mirrors
	//       matter.js OperationalCredentialsServer.ts:451-453
	//       `if (failsafeContext.rootCertSet) throw ConstraintError` and
	//       chip HandleAddTrustedRootCertificate
	//       `VerifyOrExit(!failsafeContext.AddTrustedRootCertHasBeenInvoked(),
	//       ConstraintError)`.
	//
	//   (b) nocWasInvoked → AddNOC or UpdateNOC was already called in
	//       this FailSafe window; adding a different root afterwards is
	//       illegal. Mirrors chip
	//       `VerifyOrExit(!failsafeContext.NocCommandHasBeenInvoked(),
	//       ConstraintError)` and matter.js
	//       `if (failsafeContext.fabricIndex !== undefined) throw ConstraintError`.
	o.mu.RLock()
	dupRoot := o.pendingTrustRoot != nil
	nocInvoked := o.nocWasInvoked
	o.mu.RUnlock()
	if dupRoot {
		return nil, fmt.Errorf("matter: AddTrustedRootCertificate: constraint error: root already set for this FailSafe window")
	}
	if nocInvoked {
		return nil, fmt.Errorf("matter: AddTrustedRootCertificate: constraint error: NOC command already invoked in this FailSafe window")
	}

	root, err := mattercert.Decode(req.RootCACertificate)
	if err != nil {
		return nil, fmt.Errorf("%w: decode root: %w", errOpcredsInvalidArg, err)
	}
	if !root.IsRoot() {
		return nil, fmt.Errorf("%w: cert is not a root CA", errOpcredsInvalidArg)
	}
	// Validate RCAC structural invariants: self-signed (Subject==Issuer),
	// KeyUsage keyCertSign+cRLSign bits present, BasicConstraints PathLen>0.
	// Mirrors chip OperationalCredentialsCluster.cpp HandleAddTrustedRootCertificate
	// which calls ValidateChipRCAC before storing the root.
	if err := mattercert.ValidateRCAC(root); err != nil {
		return nil, fmt.Errorf("%w: RCAC validation: %w", errOpcredsInvalidArg, err)
	}
	// Stash both representations for the next AddNOC:
	//   - pendingTrustRoot: 65-byte uncompressed EC-P256 pubkey, fed into
	//     CASE responder + CompressedFabricID HKDF (Matter §4.13.2.4).
	//   - pendingTrustRootDER: full Matter RCAC TLV bytes, served verbatim
	//     by OperationalCredentials.TrustedRootCertificates (Matter
	//     §11.18.5.13). Mirrors matter.js Fabric.rootCert
	//     (Fabric.ts:68); Apple validates each list entry as a Matter
	//     Certificate TLV — bare pubkey trips Bug I.
	o.mu.Lock()
	o.pendingTrustRoot = append([]byte(nil), root.PublicKey...)
	o.pendingTrustRootDER = append([]byte(nil), req.RootCACertificate...)
	o.mu.Unlock()
	return nil, nil
}

// SetCurrentFabric is called by the message dispatcher before
// invoking a fabric-scoped command, so MatterRead's
// CurrentFabricIndex returns the right value.
func (o *OperationalCredentials) SetCurrentFabric(idx uint8) {
	o.mu.Lock()
	o.currentFabric = idx
	o.mu.Unlock()
}

// encodePrivateKey returns the 32-byte raw P-256 scalar via the
// crypto/ecdh shim (avoids the Go 1.26 deprecation of
// ecdsa.PrivateKey.D for raw access).
func encodePrivateKey(priv *ecdsa.PrivateKey) []byte {
	ek, err := priv.ECDH()
	if err != nil {
		// Falls the input is not a P-256 key. Caller passes only
		// keys generated by ecdsa.GenerateKey(elliptic.P256, ...),
		// so this path is unreachable in production.
		return nil
	}
	return ek.Bytes()
}

// EncodeECDHFromPrivate is exposed for tests / commissioning code
// that need the ECDH-formatted private key (for HKDF derivations).
func EncodeECDHFromPrivate(priv *ecdsa.PrivateKey) (*ecdh.PrivateKey, error) {
	return ecdh.P256().NewPrivateKey(encodePrivateKey(priv))
}

// revertAddNOC is the canonical rollback sequence for a failed AddNOC.
// It mirrors chip's `needRevert` exit block in
// OperationalCredentialsCluster.cpp::HandleAddNOC which executes:
//
//  1. RevertPendingOpCertsExceptRoot (fabric table revert)
//  2. groupDataProvider.RemoveFabric (group key removal)
//  3. accessControl.DeleteAllEntriesForFabric (ACL cleanup)
//
// All three errors are intentionally ignored: the cleanup is best-effort
// and callers must not see a secondary error masking the root cause. Using
// a single helper ensures that any future AddNOC state (e.g. ARLs) is
// rolled back in one place rather than scattered across each failure branch.
func (o *OperationalCredentials) revertAddNOC(ctx context.Context, fabricIndex uint8) {
	// Clear any ACL entries that were written for this fabric before
	// reverting the fabric record. Mirrors chip
	// operational-credentials-server.cpp HandleAddNOC needRevert exit block
	// step 3: accessControl.DeleteAllEntriesForFabric.
	_ = o.store.ReplaceACL(ctx, fabricIndex, nil)
	_ = o.store.RemoveGroupKeysByFabric(ctx, fabricIndex)
	_ = o.store.RemoveFabric(ctx, fabricIndex)
}

// isOperationalNodeID reports whether id is in the Operational Node ID range.
// Mirrors chip src/lib/core/NodeId.h IsOperationalNodeId predicate:
//
//	id >= 0x0000_0000_0000_0001 && id <= 0xFFFF_FFFF_FFFF_FFEF
//
// Values 0 (kUndefinedNodeId) and 0xFFFF_FFFF_FFFF_FFF0..0xFFFF_FFFF_FFFF_FFFF
// (reserved / group / PASE ranges) are excluded.
func isOperationalNodeID(id uint64) bool {
	return id >= 0x0000_0000_0000_0001 && id <= 0xFFFF_FFFF_FFFF_FFEF
}

// isCASEAuthTag reports whether id is a CASE Auth Tag (CAT) node-id encoding.
// Mirrors chip src/lib/core/NodeId.h IsCASEAuthTag predicate:
//
//	(id >> 32) == 0xFFFF_FFFD
//
// The lower 32 bits encode the CAT value + version.
func isCASEAuthTag(id uint64) bool {
	return (id >> 32) == 0xFFFF_FFFD
}

// opcredsInvalidCommandErr is returned by VID-Verification commands when
// the bridge does not support VID-Verification mode. The IM layer maps
// this to StatusInvalidCommand (0x85) per Matter §8.9.
type opcredsInvalidCommandErr struct{ msg string }

func (e opcredsInvalidCommandErr) Error() string                   { return e.msg }
func (e opcredsInvalidCommandErr) MatterStatusCode() im.StatusCode { return im.StatusInvalidCommand }

// handleSetVidVerificationStatement handles command 0x0C
// (SetVidVerificationStatement). The command is mandatory per the cluster
// schema. This bridge does not run a VID-Verification-capable fabric, so
// the handler returns InvalidCommand per Matter §11.18.6.13 conformance:
// a device that does not support VID Verification SHALL return
// INVALID_COMMAND for this request.
//
// When VID-Verification support is added in a future revision, this handler
// should persist the VidVerificationStatement and Vvsc fields into the
// FabricRecord and update the NOCStruct.Vvsc attribute read path.
func (o *OperationalCredentials) handleSetVidVerificationStatement(_ context.Context, fields any) (any, error) {
	if _, ok := fields.(SetVidVerificationStatementRequest); !ok {
		return nil, fmt.Errorf("%w: SetVidVerificationStatementRequest expected, got %T", errOpcredsInvalidArg, fields)
	}
	return nil, opcredsInvalidCommandErr{"matter: SetVidVerificationStatement: VID-Verification mode not supported"}
}

// handleSignVidVerificationRequest handles command 0x0D
// (SignVidVerificationRequest). The command is mandatory per the cluster
// schema. This bridge does not support VID-Verification, so it returns
// InvalidCommand. When VID-Verification support is added, this handler
// must sign the TBS (FabricIndex || FabricBindingVersion || ClientChallenge)
// with the fabric's operational key and return SignVidVerificationResponse
// (0x0E) with the resulting signature.
func (o *OperationalCredentials) handleSignVidVerificationRequest(_ context.Context, fields any) (any, error) {
	if _, ok := fields.(SignVidVerificationRequest); !ok {
		return nil, fmt.Errorf("%w: SignVidVerificationRequest expected, got %T", errOpcredsInvalidArg, fields)
	}
	return nil, opcredsInvalidCommandErr{"matter: SignVidVerificationRequest: VID-Verification mode not supported"}
}

// isOperationalAdminVendorID reports whether v is a Vendor-ID a
// commissioner may legitimately bind into a fabric's admin subject.
// Mirrors chip's `IsVendorIdValidOperationally`
// (data-model-providers helper invoked at
// OperationalCredentialsCluster.cpp:437):
//
//   - 0x0000 is the "anonymous / unspecified" placeholder — rejected.
//   - 0xFFFF is the "not-applicable" reserved value — rejected.
//   - 0xFFF5..0xFFFE are reserved by CHIPVendorIdentifiers.hpp and are
//     rejected by chip; the bridge matches chip's stricter gate.
//   - 0xFFF1..0xFFF4 (Test-VID range) are INTENTIONALLY accepted:
//     chip-tool commissions with VID 0xFFF1 by default. Rejecting the
//     Test-VID range would break every development and CSA-cert path.
//     Apple's production commissioner enforces VID-vs-attestation
//     alignment via the DAC chain, not the AdminVendorID field.
//
// Returns false for the two universal placeholders and the reserved
// 0xFFF5..0xFFFE range; returns true for everything else including
// the Test-VID range 0xFFF1..0xFFF4.
func isOperationalAdminVendorID(v uint16) bool {
	if v == 0x0000 || v == 0xFFFF {
		return false
	}
	// Reserved range per CHIPVendorIdentifiers.hpp.
	if v >= 0xFFF5 && v <= 0xFFFE {
		return false
	}
	return true
}
