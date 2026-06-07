// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package bridge

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/wire"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/internal/north/matter/secure/setup"
)

// FailSafeChecker is the optional pre-open guard CommissioningWindow
// consults before opening a new commissioning window. Matter §11.19.7.1
// requires that OpenCommissioningWindow returns BUSY when a FailSafe is
// already armed by a different commissioner. The bridge wires this to
// GeneralCommissioning so OpenWindow can reject immediately rather than
// overwriting an active commissioning session.
//
// Returning true from FailSafeArmed causes OpenWindow to return
// [wire.ErrAdmCommBusy]. Implementations must be goroutine-safe.
type FailSafeChecker interface {
	FailSafeArmed() bool
}

// FailSafeArmer is the optional bridge-side hook CommissioningWindow
// calls after successfully opening a window. Matter §11.19.6 mandates
// that OpenCommissioningWindow MUST arm the fail-safe for the window
// duration — the cluster itself does not own the FailSafe state, so it
// delegates via this interface.
//
// Pass fabricIndex=0 for pre-commissioning windows (the window opener
// is not yet associated with any fabric). The implementation is wired
// from daemon.go to GeneralCommissioning.ArmFailSafe.
//
// Mirrors matter.js
// packages/node/src/behaviors/administrator-commissioning/
// AdministratorCommissioningServer.ts:openCommissioningWindow call to
// GeneralCommissioningBehavior.armFailSafeLogic.
type FailSafeArmer interface {
	ArmFailSafeFor(ctx context.Context, seconds uint32, fabricIndex uint8) error
}

// PaseSessionCloser is the optional bridge-side hook CommissioningWindow
// calls at the start of RevokeWindow. Matter §11.19.7.3 step 1 mandates
// closing any open PASE session regardless of window state. The bridge's
// secure-channel layer owns PASE session cleanup; this interface lets
// CommissioningWindow drive it without importing the secure package.
//
// Mirrors matter.js
// packages/node/src/behaviors/administrator-commissioning/
// AdministratorCommissioningServer.ts:revokeCommissioning call to
// paseCommissioner.close().
type PaseSessionCloser interface {
	ClosePaseSessions(ctx context.Context) error
}

// CommissioningWindow tracks an open enhanced-commissioning window.
// The bridge owns at most one live window at a time per ADR 0012's
// single-PASE-acceptor design — concurrent windows would require
// multiple verifiers, which is a separate v1.2 extension.
//
// Window state transitions:
//
//	closed → OpenWindow → enhanced
//	enhanced → RevokeWindow → closed (early)
//	enhanced → closeTimer fires → closed (expired)
//
// Each transition fires `onTransition` (for the WS event publisher
// + the AdministratorCommissioning cluster's WindowStatus attribute)
// and, when set, the `restore` closure the
// [CommissioningWindowOpener.EphemeralProvider] returned at open —
// guaranteeing the bridge's long-lived PASE acceptor is reinstated
// regardless of whether the window closed early or by timeout.
type CommissioningWindow struct {
	mu             sync.RWMutex
	open           bool
	expiresAt      time.Time
	discriminator  uint16
	durationSec    uint16
	adminFabric    uint8
	adminFabricSet bool
	adminVendor    uint16
	adminVendorSet bool
	// isBasicWindow records whether the window was opened via
	// OpenBasicCommissioningWindow (BC feature). When false, the
	// status reported by CurrentWindow is EnhancedWindowOpen (1);
	// when true, it is BasicWindowOpen (2).
	isBasicWindow bool

	// closeTimer fires when the window expires so the bridge auto-
	// reverts to "no PASE acceptor". Cancelled when RevokeWindow
	// closes the window early.
	closeTimer *time.Timer

	// restore, when non-nil, is invoked on every window-close
	// transition (early revoke or timer expiry) so the ephemeral
	// PASE adapter the opener installed for the window is replaced
	// by the bridge's long-lived configured acceptor. Nil when the
	// window opened in legacy mode (configured passcode reused —
	// no swap happened).
	restore func()

	// onTransition gets called every time the window state changes
	// — typically wired to the AdministratorCommissioning cluster's
	// SubscriptionEventReporter so subscribers see WindowStatus flip.
	onTransition func()

	// failSafeChecker, when non-nil, is consulted at the start of every
	// OpenWindow call. A currently-armed FailSafe window (set by a prior
	// commissioner) causes OpenWindow to return BUSY rather than
	// overwriting the active commissioning session. Nil in unit-test
	// setups that do not exercise the fail-safe guard.
	failSafeChecker FailSafeChecker

	// failSafeArmer, when non-nil, is called after every successful
	// window open to arm the fail-safe for the window duration per
	// Matter §11.19.6. Wired from the daemon bootstrap to the
	// GeneralCommissioning cluster's ArmFailSafe path via
	// [SetFailSafeArmer]; nil in unit-test setups that do not exercise
	// the fail-safe path.
	failSafeArmer FailSafeArmer

	// paseSessionCloser, when non-nil, is called at the start of
	// RevokeWindow to evict any open PASE session per Matter §11.19.7.3.
	paseSessionCloser PaseSessionCloser
}

// NewCommissioningWindow returns a fresh, closed window.
func NewCommissioningWindow() *CommissioningWindow {
	return &CommissioningWindow{}
}

// SetFailSafeChecker wires (or clears, when nil) the guard consulted at
// the start of every [OpenWindow] call. When the checker reports an armed
// FailSafe, OpenWindow returns [wire.ErrAdmCommBusy] rather than opening
// a second window that would overwrite the active commissioning session.
// Safe to call concurrently with [OpenWindow].
func (w *CommissioningWindow) SetFailSafeChecker(c FailSafeChecker) {
	w.mu.Lock()
	w.failSafeChecker = c
	w.mu.Unlock()
}

// SetFailSafeArmer wires (or clears, when nil) the hook called after
// a successful window open to arm the FailSafe timer per Matter §11.19.6.
// Safe to call concurrently with [OpenWindow].
func (w *CommissioningWindow) SetFailSafeArmer(a FailSafeArmer) {
	w.mu.Lock()
	w.failSafeArmer = a
	w.mu.Unlock()
}

// SetPaseSessionCloser wires (or clears, when nil) the hook called at
// the start of [RevokeWindow] to evict open PASE sessions per Matter
// §11.19.7.3.
// Safe to call concurrently with [RevokeWindow].
func (w *CommissioningWindow) SetPaseSessionCloser(c PaseSessionCloser) {
	w.mu.Lock()
	w.paseSessionCloser = c
	w.mu.Unlock()
}

// SetTransitionHook wires the optional callback fired on Open / Close.
// Pass nil to detach. Safe to call concurrently.
func (w *CommissioningWindow) SetTransitionHook(fn func()) {
	w.mu.Lock()
	w.onTransition = fn
	w.mu.Unlock()
}

// CurrentWindow implements [wire.WindowController]. Returns the
// snapshot the AdministratorCommissioning cluster reads attributes
// from.
func (w *CommissioningWindow) CurrentWindow() wire.WindowStatusSnapshot {
	w.mu.RLock()
	defer w.mu.RUnlock()

	if !w.open {
		return wire.WindowStatusSnapshot{
			Status:            wire.WindowStatusClosed,
			AdminFabricIsNull: true,
			AdminVendorIsNull: true,
		}
	}
	// Derive window status from the mode the window was opened in.
	// matter.js AdministratorCommissioningServer.ts:114-119 sets
	// state.windowStatus = BasicWindowOpen (2) for basic windows and
	// EnhancedWindowOpen (1) for enhanced.
	status := wire.WindowStatusEnhanced
	if w.isBasicWindow {
		status = wire.WindowStatusBasic
	}
	snap := wire.WindowStatusSnapshot{
		Status: status,
	}
	if w.adminFabricSet {
		snap.AdminFabricIndex = w.adminFabric
	} else {
		snap.AdminFabricIsNull = true
	}
	if w.adminVendorSet {
		snap.AdminVendorID = w.adminVendor
	} else {
		snap.AdminVendorIsNull = true
	}
	return snap
}

// OpenWindow implements [wire.WindowController]. The cluster command
// path supplies a verifier-based [wire.OpenWindowParams]; v1.1 stores
// the resulting state but does not yet swap a per-window verifier
// into the bridge's PASE adapter (the bridge already accepts PASE
// against the configured passcode for `duration_seconds`).
//
// A second OpenWindow while one is already live returns BUSY per
// Matter §11.19.7.1.
//
// After a successful state transition, the FailSafeArmer (if wired)
// is called to arm the fail-safe for the window duration per Matter
// §11.19.6. A FailSafe arm failure is logged but does not abort the
// window: the window is already open and the controller expects Success.
func (w *CommissioningWindow) OpenWindow(ctx context.Context, params wire.OpenWindowParams) error {
	// Reject if a FailSafe from a prior commissioning session is still
	// armed. Opening a window while another commissioner's FailSafe is
	// active would overwrite its state; return BUSY instead.
	w.mu.RLock()
	checker := w.failSafeChecker
	w.mu.RUnlock()
	if checker != nil && checker.FailSafeArmed() {
		return wire.ErrAdmCommBusy
	}

	w.mu.Lock()
	if w.open {
		w.mu.Unlock()
		return wire.ErrAdmCommBusy
	}
	maxTimeout := uint32(commissioningWindowMaxSec)
	if params.IsUncommissioned {
		// Uncommissioned devices (no fabric installed yet) MAY use an
		// extended 48-h window per Matter §11.19.8.1, which matches
		// chip CommissioningWindowManager.cpp::MaxCommissioningTimeout()
		// and matter.js AdministratorCommissioningServer.ts:283-290.
		// Commissioned bridges use the standard 900-s cap.
		maxTimeout = commissioningWindowMaxSecUncommissioned
	}
	if uint32(params.CommissioningTimeoutSeconds) < 180 || uint32(params.CommissioningTimeoutSeconds) > maxTimeout {
		w.mu.Unlock()
		return ErrCommissioningWindowDurationInvalid
	}
	w.open = true
	w.discriminator = params.Discriminator
	w.durationSec = params.CommissioningTimeoutSeconds
	w.expiresAt = time.Now().Add(time.Duration(params.CommissioningTimeoutSeconds) * time.Second)
	// Persist admin fabric / vendor metadata so the cluster can
	// reflect them via AdminFabricIndex / AdminVendorId attributes
	// per Matter §11.19.5.2.
	w.isBasicWindow = params.IsBasicWindow
	if params.AdminFabricIndex != 0 {
		w.adminFabric = params.AdminFabricIndex
		w.adminFabricSet = true
	}
	if params.AdminVendorID != 0 {
		w.adminVendor = params.AdminVendorID
		w.adminVendorSet = true
	}
	hook := w.onTransition
	armer := w.failSafeArmer
	w.closeTimer = time.AfterFunc(time.Duration(params.CommissioningTimeoutSeconds)*time.Second, func() {
		w.mu.Lock()
		w.open = false
		w.adminFabricSet = false
		w.adminVendorSet = false
		w.isBasicWindow = false
		closeHook := w.onTransition
		restore := w.restore
		w.restore = nil
		w.mu.Unlock()
		if restore != nil {
			restore()
		}
		if closeHook != nil {
			closeHook()
		}
	})
	w.mu.Unlock()
	// Matter §11.19.6: arm the FailSafe for the window duration after the
	// window state is committed. fabricIndex=0 — window opened pre-commissioning.
	// Mirrors matter.js AdministratorCommissioningServer.ts:openCommissioningWindow
	// → GeneralCommissioningBehavior.armFailSafeLogic(timeoutSeconds).
	if armer != nil {
		_ = armer.ArmFailSafeFor(ctx, uint32(params.CommissioningTimeoutSeconds), 0) //nolint:gosec // G115: timeout fits uint32 by spec; see #20
	}
	if hook != nil {
		hook()
	}
	return nil
}

// setRestore records the closure invoked on every close transition
// so the ephemeral PASE adapter installed by the opener is replaced
// by the bridge's long-lived configured acceptor regardless of which
// path closes the window. Called by the opener immediately after the
// successful OpenWindow.
//
// Returns false if the window has since closed (race against the
// timer) so the caller can revert the swap itself.
func (w *CommissioningWindow) setRestore(restore func()) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.open {
		return false
	}
	w.restore = restore
	return true
}

// RevokeWindow implements [wire.WindowController]. Per Matter §11.19.7.3
// step 1, any open PASE session is evicted first (via [PaseSessionCloser]
// when wired), regardless of whether a commissioning window is open.
// Then the window is closed (if open) and the transition hook fires.
func (w *CommissioningWindow) RevokeWindow(ctx context.Context) error {
	// Matter §11.19.7.3 step 1: close any open PASE session before
	// touching window state. Mirrors matter.js AdministratorCommissioningServer.ts:
	// revokeCommissioning → paseCommissioner.close().
	w.mu.RLock()
	closer := w.paseSessionCloser
	w.mu.RUnlock()
	if closer != nil {
		_ = closer.ClosePaseSessions(ctx)
	}

	w.mu.Lock()
	if !w.open {
		w.mu.Unlock()
		// Idempotent — internal Bridge.RevokeWindow is callable from
		// timer expiry, shutdown path, and the cluster wire handler.
		// The wire-layer WindowNotOpenError check (Matter §11.19.8.3 Step 2)
		// lives in cluster/wire/admincommissioning.go so the cluster server
		// can still call this method for PASE teardown without surfacing an
		// error on a no-op revoke.
		return nil
	}
	w.open = false
	w.adminFabricSet = false
	w.adminVendorSet = false
	w.isBasicWindow = false
	if w.closeTimer != nil {
		w.closeTimer.Stop()
		w.closeTimer = nil
	}
	hook := w.onTransition
	restore := w.restore
	w.restore = nil
	w.mu.Unlock()
	if restore != nil {
		restore()
	}
	if hook != nil {
		hook()
	}
	return nil
}

// EphemeralCredentials are the freshly-generated credentials for one
// commissioning window. The daemon-side [EphemeralProvider] builds
// them against a random salt + the configured iteration count and
// installs the corresponding PASE adapter into the bridge for the
// window's lifetime.
//
// Restore is invoked exactly once per successful generation, after
// the window closes (early revoke or timer expiry), so the bridge's
// long-lived configured PASE acceptor is re-armed. The opener wires
// it via [CommissioningWindow.setRestore] so both close paths fire
// it deterministically.
type EphemeralCredentials struct {
	Discriminator uint16
	Passcode      uint32
	Restore       func()
}

// EphemeralProvider is the daemon-side hook the
// [CommissioningWindowOpener] consumes when ephemeral mode is wired.
// `GenerateAndInstall` MUST: (1) compute a fresh Spake2+ verifier
// against a random passcode + salt, (2) install the resulting PASE
// adapter on the bridge — taking precedence over the configured
// long-lived adapter — and (3) return a Restore closure that re-arms
// the previous adapter on window close.
//
// Returning an error aborts the open: no swap happened, no Restore
// is owed. The opener never invokes Restore on the error path.
type EphemeralProvider interface {
	GenerateAndInstall(ctx context.Context) (EphemeralCredentials, error)
}

// CommissioningWindowOpener is the
// [handlers.MatterCommissioningOpener]-compatible REST adapter the
// daemon wires into [rest.Deps.MatterCommissioningOpener].
//
// Two operating modes:
//
//   - Configured mode (default): the bridge's PASE acceptor is open
//     against the configured passcode/discriminator. "Opening a
//     window" advertises that pair, returns the QR + manual code,
//     and starts the duration timer. Used by ops who want a fixed
//     pairing pad.
//   - Ephemeral mode (when [SetEphemeralProvider] is wired): each
//     OpenCommissioningWindow generates a fresh discriminator +
//     passcode + Spake2+ verifier, swaps them into the bridge's
//     PASE adapter for the window duration, and reverts on close.
//     Recommended for production: the configured passcode never
//     leaves the bridge process and pairing codes auto-rotate.
type CommissioningWindowOpener struct {
	window        *CommissioningWindow
	discriminator uint16
	passcode      uint32
	vendorID      uint16
	productID     uint16

	mu        sync.Mutex
	ephemeral EphemeralProvider
}

// NewCommissioningWindowOpener wires the opener against an existing
// window controller and the bridge's setup-payload parameters.
func NewCommissioningWindowOpener(w *CommissioningWindow, discriminator uint16, passcode uint32, vendorID, productID uint16) *CommissioningWindowOpener {
	return &CommissioningWindowOpener{
		window:        w,
		discriminator: discriminator,
		passcode:      passcode,
		vendorID:      vendorID,
		productID:     productID,
	}
}

// SetEphemeralProvider wires (or clears, when nil) the per-window
// credential generator. When set, the next call to
// [OpenCommissioningWindow] generates fresh credentials and installs
// a window-scoped PASE adapter on the bridge instead of reusing the
// configured one.
//
// Safe to call concurrently with [OpenCommissioningWindow]; the
// switch is observed atomically per call.
func (o *CommissioningWindowOpener) SetEphemeralProvider(p EphemeralProvider) {
	if o == nil {
		return
	}
	o.mu.Lock()
	o.ephemeral = p
	o.mu.Unlock()
}

func (o *CommissioningWindowOpener) snapshotEphemeral() EphemeralProvider {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.ephemeral
}

// OpenCommissioningWindowResult is the value the REST handler
// returns. Mirrors `handlers.MatterCommissioningWindowResult`
// without the import cycle.
type OpenCommissioningWindowResult struct {
	Discriminator   uint16
	Passcode        uint32
	DurationSeconds uint16
	QRCode          string
	ManualCode      string
}

// OpenCommissioningWindow generates the setup-payload artifacts and
// kicks the underlying [CommissioningWindow] into the open state.
// Returns a [ErrCommissioningWindowAlreadyOpen]-style error when a
// window is already live; the REST handler maps it to 409 Conflict.
//
// In ephemeral mode the call generates fresh credentials before
// touching the window. Failure paths after a successful generation
// invoke the Restore closure so the bridge's long-lived PASE adapter
// is re-armed even if the QR/manual code build or the window OpenWindow
// rejects later.
func (o *CommissioningWindowOpener) OpenCommissioningWindow(ctx context.Context, durationSeconds uint16) (OpenCommissioningWindowResult, error) {
	if o == nil || o.window == nil {
		return OpenCommissioningWindowResult{}, ErrCommissioningWindowNotConfigured
	}

	disc := o.discriminator
	pass := o.passcode
	var restore func()

	if provider := o.snapshotEphemeral(); provider != nil {
		creds, err := provider.GenerateAndInstall(ctx)
		if err != nil {
			return OpenCommissioningWindowResult{}, fmt.Errorf("ephemeral credentials: %w", err)
		}
		disc = creds.Discriminator
		pass = creds.Passcode
		restore = creds.Restore
	}

	if pass == 0 {
		if restore != nil {
			restore()
		}
		return OpenCommissioningWindowResult{}, ErrCommissioningWindowNotConfigured
	}

	if err := o.window.OpenWindow(ctx, wire.OpenWindowParams{
		CommissioningTimeoutSeconds: durationSeconds,
		Discriminator:               disc,
	}); err != nil {
		if restore != nil {
			restore()
		}
		if errors.Is(err, wire.ErrAdmCommBusy) {
			return OpenCommissioningWindowResult{}, ErrCommissioningWindowAlreadyOpen
		}
		return OpenCommissioningWindowResult{}, err
	}

	// Wire restore so both window-close paths (RevokeWindow + timer
	// expiry) re-arm the configured PASE adapter. setRestore returns
	// false if the window flipped closed already (lost the race with
	// the timer); restore inline in that case so we never leak the
	// ephemeral adapter.
	if restore != nil {
		if !o.window.setRestore(restore) {
			restore()
		}
	}

	qr, err := setup.QRCode(setup.Payload{
		Version:       0,
		VendorID:      o.vendorID,
		ProductID:     o.productID,
		Discriminator: disc,
		Passcode:      pass,
		DiscoveryCaps: setup.DiscoveryOnIP,
	})
	if err != nil {
		// Best-effort: revoke the window so the daemon doesn't dangle
		// a half-open state. RevokeWindow itself fires the restore.
		_ = o.window.RevokeWindow(ctx)
		return OpenCommissioningWindowResult{}, err
	}
	manual, err := setup.ManualCode(disc, pass)
	if err != nil {
		_ = o.window.RevokeWindow(ctx)
		return OpenCommissioningWindowResult{}, err
	}
	return OpenCommissioningWindowResult{
		Discriminator:   disc,
		Passcode:        pass,
		DurationSeconds: durationSeconds,
		QRCode:          qr,
		ManualCode:      manual,
	}, nil
}

// RandomDiscriminator returns a uniformly-random 12-bit discriminator
// suitable for a Matter setup payload. The Matter spec (§5.1.1.1.2)
// reserves no specific values, so any 0..0xFFF is legal.
func RandomDiscriminator() (uint16, error) {
	var b [2]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0, fmt.Errorf("rand discriminator: %w", err)
	}
	return binary.BigEndian.Uint16(b[:]) & 0x0FFF, nil
}

// RandomPasscode returns a uniformly-random 8-decimal-digit passcode
// from the §5.1.1.1.1 valid-passcode set. Per spec the following are
// invalid and excluded by re-rolling: the all-same-digit values
// 00000000..99999999 plus 12345678 and 87654321.
func RandomPasscode() (uint32, error) {
	invalid := map[uint32]struct{}{
		0:        {},
		11111111: {},
		22222222: {},
		33333333: {},
		44444444: {},
		55555555: {},
		66666666: {},
		77777777: {},
		88888888: {},
		99999999: {},
		12345678: {},
		87654321: {},
	}
	for {
		var b [4]byte
		if _, err := rand.Read(b[:]); err != nil {
			return 0, fmt.Errorf("rand passcode: %w", err)
		}
		// 99999998 inclusive — drops 99999999 anyway, then re-rolls
		// any other excluded value below.
		v := binary.BigEndian.Uint32(b[:]) % 99999999
		if _, bad := invalid[v]; bad {
			continue
		}
		return v, nil
	}
}

// RandomSalt returns 16 random bytes — the Matter §3.10.1.5 minimum
// salt length for the Spake2+ Verifier. The spec accepts up to 32
// bytes; 16 is the conventional choice.
func RandomSalt() ([]byte, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("rand salt: %w", err)
	}
	return b, nil
}

// Commissioning window timeout constants.
//
//   - commissioningWindowMaxSec — standard upper bound for a commissioned
//     bridge per Matter §11.19.8.1 (chip CommissioningWindowManager.cpp
//     kMaxCommissioningTimeout = 900 s).
//   - commissioningWindowMaxSecUncommissioned — extended upper bound for an
//     uncommissioned node (FabricCount == 0) per Matter §11.19.8.1 and chip
//     CommissioningWindowManager.cpp::MaxCommissioningTimeout() which returns
//     172 800 s (48 h) when the fabric table is empty.
const (
	commissioningWindowMaxSec               = uint16(900)
	commissioningWindowMaxSecUncommissioned = uint32(172800) // 48 h per Matter §11.19.8.1
)

// commWindowDurationInvalidErr is the typed [im.StatusCodeError] backing
// [ErrCommissioningWindowDurationInvalid]. The IM dispatcher type-asserts
// against [im.StatusCodeError] first, so this causes InvalidCommand (0x85)
// to be returned. Mirrors matter.js
// AdministratorCommissioningServer.ts:#assertCommissioningWindowRequirements
// (lines 199-209) which throws StatusResponseError with Status.InvalidCommand.
type commWindowDurationInvalidErr struct{}

func (commWindowDurationInvalidErr) Error() string {
	return "bridge: commissioning window duration out of range"
}

func (commWindowDurationInvalidErr) MatterStatusCode() im.StatusCode { return im.StatusInvalidCommand }

// Errors surfaced by the commissioning-window APIs.
var (
	// ErrCommissioningWindowAlreadyOpen surfaces when a second
	// OpenCommissioningWindow lands while a window is already live.
	// The REST handler maps it to 409 Conflict.
	ErrCommissioningWindowAlreadyOpen = errors.New("bridge: commissioning window already open")
	// ErrCommissioningWindowNotConfigured surfaces when the bridge
	// has no passcode configured. The REST handler maps it to 503.
	ErrCommissioningWindowNotConfigured = errors.New("bridge: commissioning not configured (no passcode)")
	// ErrCommissioningWindowDurationInvalid surfaces when the
	// requested duration is outside the §11.19.8.1 valid range.
	// For commissioned bridges the range is [180, 900] s; for
	// uncommissioned devices (FabricCount == 0) the range is
	// [180, 172800] s (48 h).
	// The typed backing value causes the IM dispatcher to return
	// InvalidCommand (0x85) per matter.js
	// AdministratorCommissioningServer.ts:#assertCommissioningWindowRequirements
	// (lines 199-209) which throws Status.InvalidCommand for out-of-range
	// timeout values.
	ErrCommissioningWindowDurationInvalid error = commWindowDurationInvalidErr{}
)
