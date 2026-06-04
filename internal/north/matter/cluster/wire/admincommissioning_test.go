// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package wire_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/wire"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ---- attribute ID constants ----

const (
	admCommClusterID   uint32 = 0x003C
	admCommAttrWindow  uint32 = 0x0000
	admCommAttrFabric  uint32 = 0x0001
	admCommAttrVendor  uint32 = 0x0002
	admCommAttrFeatMap uint32 = 0xFFFC
	admCommAttrRev     uint32 = 0xFFFD

	admCommCmdOpenWindow      uint32 = 0x00
	admCommCmdOpenBasicWindow uint32 = 0x01
	admCommCmdRevoke          uint32 = 0x02
)

// ---- fakeWindowController ----

type fakeWindowController struct {
	snapshot        wire.WindowStatusSnapshot
	openWindowErr   error
	revokeErr       error
	capturedParams  wire.OpenWindowParams
	openWindowCalls int
	revokeCalls     int
}

func (f *fakeWindowController) CurrentWindow() wire.WindowStatusSnapshot {
	return f.snapshot
}

func (f *fakeWindowController) OpenWindow(_ context.Context, params wire.OpenWindowParams) error {
	f.openWindowCalls++
	f.capturedParams = params
	return f.openWindowErr
}

func (f *fakeWindowController) RevokeWindow(_ context.Context) error {
	f.revokeCalls++
	return f.revokeErr
}

// ---- helper ----

func newAdmComm() *wire.AdministratorCommissioning {
	return wire.NewAdministratorCommissioning()
}

// ---- Tests ----

func TestAdmComm_ClusterID(t *testing.T) {
	t.Parallel()
	ac := newAdmComm()
	if got := ac.MatterClusterID(); got != admCommClusterID {
		t.Errorf("MatterClusterID() = 0x%04X, want 0x%04X", got, admCommClusterID)
	}
}

func TestAdmComm_Read_ClusterRevision_IsOne(t *testing.T) {
	t.Parallel()
	ac := newAdmComm()
	v, ok := ac.MatterRead(admCommAttrRev)
	if !ok {
		t.Fatal("MatterRead(ClusterRevision) returned ok=false")
	}
	if rev, _ := v.(uint16); rev != 1 {
		t.Errorf("ClusterRevision = %v, want 1", v)
	}
}

func TestAdmComm_Read_FeatureMap_IsZero(t *testing.T) {
	t.Parallel()
	ac := newAdmComm()
	v, ok := ac.MatterRead(admCommAttrFeatMap)
	if !ok {
		t.Fatal("MatterRead(FeatureMap) returned ok=false")
	}
	if fm, _ := v.(uint32); fm != 0 {
		t.Errorf("FeatureMap = 0x%04X, want 0x0000", fm)
	}
}

func TestAdmComm_Read_WindowStatus_NoController_ReturnsClosed(t *testing.T) {
	t.Parallel()
	ac := newAdmComm()
	v, ok := ac.MatterRead(admCommAttrWindow)
	if !ok {
		t.Fatal("MatterRead(WindowStatus) returned ok=false")
	}
	if status, _ := v.(uint8); status != uint8(wire.WindowStatusClosed) {
		t.Errorf("WindowStatus (no controller) = %v, want %v (Closed)", v, uint8(wire.WindowStatusClosed))
	}
}

func TestAdmComm_Read_AdminFabricIndex_NoController_ReturnsNull(t *testing.T) {
	t.Parallel()
	ac := newAdmComm()
	v, ok := ac.MatterRead(admCommAttrFabric)
	if !ok {
		t.Fatal("MatterRead(AdminFabricIndex) returned ok=false")
	}
	if v != nil {
		t.Errorf("AdminFabricIndex (no controller) = %v, want nil (NULL)", v)
	}
}

func TestAdmComm_Read_AdminVendorID_NoController_ReturnsNull(t *testing.T) {
	t.Parallel()
	ac := newAdmComm()
	v, ok := ac.MatterRead(admCommAttrVendor)
	if !ok {
		t.Fatal("MatterRead(AdminVendorID) returned ok=false")
	}
	if v != nil {
		t.Errorf("AdminVendorID (no controller) = %v, want nil (NULL)", v)
	}
}

func TestAdmComm_Read_UnknownAttr_ReturnsFalse(t *testing.T) {
	t.Parallel()
	ac := newAdmComm()
	_, ok := ac.MatterRead(0xDEAD)
	if ok {
		t.Error("MatterRead(unknown) returned ok=true, want false")
	}
}

func TestAdmComm_Read_WindowStatus_WithController_Enhanced(t *testing.T) {
	t.Parallel()
	ac := newAdmComm()
	ctrl := &fakeWindowController{
		snapshot: wire.WindowStatusSnapshot{
			Status:            wire.WindowStatusEnhanced,
			AdminFabricIsNull: true,
			AdminVendorIsNull: true,
		},
	}
	ac.SetController(ctrl)

	v, ok := ac.MatterRead(admCommAttrWindow)
	if !ok {
		t.Fatal("MatterRead(WindowStatus) returned ok=false")
	}
	if status, _ := v.(uint8); status != uint8(wire.WindowStatusEnhanced) {
		t.Errorf("WindowStatus = %v, want %v (Enhanced)", v, uint8(wire.WindowStatusEnhanced))
	}
}

func TestAdmComm_Read_AdminFabricIndex_NotNull_ReturnsFabricIndex(t *testing.T) {
	t.Parallel()
	ac := newAdmComm()
	ctrl := &fakeWindowController{
		snapshot: wire.WindowStatusSnapshot{
			Status:            wire.WindowStatusEnhanced,
			AdminFabricIndex:  7,
			AdminFabricIsNull: false,
			AdminVendorIsNull: true,
		},
	}
	ac.SetController(ctrl)

	v, ok := ac.MatterRead(admCommAttrFabric)
	if !ok {
		t.Fatal("MatterRead(AdminFabricIndex) returned ok=false")
	}
	if idx, _ := v.(uint8); idx != 7 {
		t.Errorf("AdminFabricIndex = %v, want 7", v)
	}
}

func TestAdmComm_Read_AdminVendorID_NotNull_ReturnsVendorID(t *testing.T) {
	t.Parallel()
	ac := newAdmComm()
	ctrl := &fakeWindowController{
		snapshot: wire.WindowStatusSnapshot{
			Status:            wire.WindowStatusEnhanced,
			AdminFabricIsNull: true,
			AdminVendorID:     0x1234,
			AdminVendorIsNull: false,
		},
	}
	ac.SetController(ctrl)

	v, ok := ac.MatterRead(admCommAttrVendor)
	if !ok {
		t.Fatal("MatterRead(AdminVendorID) returned ok=false")
	}
	if vid, _ := v.(uint16); vid != 0x1234 {
		t.Errorf("AdminVendorID = 0x%04X, want 0x1234", vid)
	}
}

func TestAdmComm_Write_ReturnsErrorForEveryAttr(t *testing.T) {
	t.Parallel()
	ac := newAdmComm()
	for _, attrID := range []uint32{
		admCommAttrWindow, admCommAttrFabric, admCommAttrVendor,
		admCommAttrFeatMap, admCommAttrRev,
	} {
		t.Run("", func(t *testing.T) {
			t.Parallel()
			err := ac.MatterWrite(context.Background(), attrID, uint8(0), hmenum.CommandPriorityHigh)
			if err == nil {
				t.Errorf("MatterWrite(0x%04X) returned nil error; attribute is read-only", attrID)
			}
		})
	}
}

func TestAdmComm_Invoke_OpenWindow_NoController_ReturnsBusy(t *testing.T) {
	t.Parallel()
	ac := newAdmComm()
	// OpenCommissioningWindow requires a CASE session (fabricIndex > 0).
	ctx := im.WithFabricFilter(context.Background(), true, 1)
	_, err := ac.MatterInvoke(ctx, admCommCmdOpenWindow, wire.OpenWindowParams{
		CommissioningTimeoutSeconds: 300,
		Iterations:                  1000,
		Salt:                        make([]byte, 16),
		PAKEPasscodeVerifier:        make([]byte, 97),
	}, hmenum.CommandPriorityHigh)
	if !errors.Is(err, wire.ErrAdmCommBusy) {
		t.Errorf("MatterInvoke(OpenWindow, no controller): want ErrAdmCommBusy, got %v", err)
	}
}

func TestAdmComm_Invoke_OpenWindow_WithController_ForwardsParams(t *testing.T) {
	t.Parallel()
	ac := newAdmComm()
	ctrl := &fakeWindowController{}
	ac.SetController(ctrl)

	params := wire.OpenWindowParams{
		CommissioningTimeoutSeconds: 600,
		Discriminator:               0xABC,
		// PAKE params per Matter §11.19.8.1.2.
		Iterations:           1000,
		Salt:                 make([]byte, 16),
		PAKEPasscodeVerifier: make([]byte, 97),
	}
	// OpenCommissioningWindow requires a CASE session (fabricIndex > 0).
	caseCtx := im.WithFabricFilter(context.Background(), true, 1)
	_, err := ac.MatterInvoke(caseCtx, admCommCmdOpenWindow, params, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Errorf("MatterInvoke(OpenWindow) with controller: unexpected error: %v", err)
	}
	if ctrl.openWindowCalls != 1 {
		t.Errorf("OpenWindow call count = %d, want 1", ctrl.openWindowCalls)
	}
	// Compare the scalar fields forwarded by the cluster (slices are not
	// comparable with ==, but the cluster passes them through verbatim).
	got := ctrl.capturedParams
	if got.CommissioningTimeoutSeconds != params.CommissioningTimeoutSeconds ||
		got.Discriminator != params.Discriminator {
		t.Errorf("captured params = %+v, want %+v", got, params)
	}
}

func TestAdmComm_Invoke_OpenWindow_WithController_PropagatesError(t *testing.T) {
	t.Parallel()
	ac := newAdmComm()
	sentinelErr := errors.New("test controller error")
	ctrl := &fakeWindowController{openWindowErr: sentinelErr}
	ac.SetController(ctrl)

	// OpenCommissioningWindow requires a CASE session (fabricIndex > 0).
	caseCtx := im.WithFabricFilter(context.Background(), true, 1)
	_, err := ac.MatterInvoke(caseCtx, admCommCmdOpenWindow, wire.OpenWindowParams{
		CommissioningTimeoutSeconds: 300,
		Iterations:                  1000,
		Salt:                        make([]byte, 16),
		PAKEPasscodeVerifier:        make([]byte, 97),
	}, hmenum.CommandPriorityHigh)
	if !errors.Is(err, sentinelErr) {
		t.Errorf("MatterInvoke(OpenWindow) error = %v, want sentinel %v", err, sentinelErr)
	}
}

func TestAdmComm_Invoke_OpenBasicWindow_ReturnsUnsupported(t *testing.T) {
	t.Parallel()
	ac := newAdmComm()
	_, err := ac.MatterInvoke(context.Background(), admCommCmdOpenBasicWindow, nil, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Error("MatterInvoke(OpenBasicCommissioningWindow) returned nil error; feature not in FeatureMap")
	}
}

func TestAdmComm_Invoke_RevokeCommissioning_NoController_ReturnsBusy(t *testing.T) {
	t.Parallel()
	ac := newAdmComm()
	_, err := ac.MatterInvoke(context.Background(), admCommCmdRevoke, nil, hmenum.CommandPriorityHigh)
	if !errors.Is(err, wire.ErrAdmCommBusy) {
		t.Errorf("MatterInvoke(RevokeCommissioning, no controller): want ErrAdmCommBusy, got %v", err)
	}
}

func TestAdmComm_Invoke_RevokeCommissioning_WithController_ForwardsCall(t *testing.T) {
	t.Parallel()
	ac := newAdmComm()
	ctrl := &fakeWindowController{}
	ac.SetController(ctrl)

	// fakeWindowController defaults to WindowStatusClosed, so the wire
	// handler forwards to the controller for the unconditional PASE-close
	// (Matter §11.19.8.3 Step 1) and then surfaces ErrAdmCommWindowNotOpen
	// (Step 2). The Forwards-Call invariant is the call
	// count — the WindowNotOpen error is the Step-2 spec path.
	_, err := ac.MatterInvoke(context.Background(), admCommCmdRevoke, nil, hmenum.CommandPriorityHigh)
	if err != nil && !errors.Is(err, wire.ErrAdmCommWindowNotOpen) {
		t.Errorf("MatterInvoke(RevokeCommissioning) with controller: unexpected error: %v", err)
	}
	if ctrl.revokeCalls != 1 {
		t.Errorf("RevokeWindow call count = %d, want 1", ctrl.revokeCalls)
	}
}

func TestAdmComm_Invoke_UnknownCommand_ReturnsUnsupported(t *testing.T) {
	t.Parallel()
	ac := newAdmComm()
	_, err := ac.MatterInvoke(context.Background(), 0xFF, nil, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Error("MatterInvoke(unknown cmdID) returned nil error, want unsupported-command error")
	}
}

// TestAdmComm_Invoke_OpenWindow_StampsAdminFabricIndexFromContext locks the
// matter.js parity: AdministratorCommissioningServer.ts:176-180 sets
// `this.state.adminFabricIndex = adminFabric.fabricIndex` on every
// OpenCommissioningWindow invoke. openccu-loom mirrors that by reading the
// IM dispatcher's FabricFilterFromContext.
func TestAdmComm_Invoke_OpenWindow_StampsAdminFabricIndexFromContext(t *testing.T) {
	t.Parallel()
	ac := newAdmComm()
	ctrl := &fakeWindowController{}
	ac.SetController(ctrl)

	ctx := im.WithFabricFilter(context.Background(), true, 7)
	_, err := ac.MatterInvoke(ctx, admCommCmdOpenWindow, wire.OpenWindowParams{
		CommissioningTimeoutSeconds: 300,
		Iterations:                  1000,
		Salt:                        make([]byte, 16),
		PAKEPasscodeVerifier:        make([]byte, 97),
	}, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("MatterInvoke(OpenWindow): %v", err)
	}
	if got := ctrl.capturedParams.AdminFabricIndex; got != 7 {
		t.Errorf("AdminFabricIndex = %d, want 7 (from FabricFilterFromContext)", got)
	}
}

// TestAdmComm_Invoke_OpenWindow_ResolvesAdminVendorIDFromFabricStore locks
// the contract: when a Multi-Admin OpenCommissioningWindow arrives over
// CASE (fabricIndex != 0) and the daemon wired a VendorIDResolver, the
// cluster server populates AdminVendorID from the resolver before forwarding
// to the controller. Mirrors matter.js
// AdministratorCommissioningServer.ts:176-180 (`adminFabric.rootVendorId`).
func TestAdmComm_Invoke_OpenWindow_ResolvesAdminVendorIDFromFabricStore(t *testing.T) {
	t.Parallel()
	ac := newAdmComm()
	ctrl := &fakeWindowController{}
	ac.SetController(ctrl)

	var resolverCalls int
	var resolverFabric uint8
	ac.SetVendorIDResolver(func(_ context.Context, fabricIndex uint8) uint16 {
		resolverCalls++
		resolverFabric = fabricIndex
		return 0x1349 // matter.js test vendor
	})

	ctx := im.WithFabricFilter(context.Background(), true, 4)
	_, err := ac.MatterInvoke(ctx, admCommCmdOpenWindow, wire.OpenWindowParams{
		CommissioningTimeoutSeconds: 300,
		Iterations:                  1000,
		Salt:                        make([]byte, 16),
		PAKEPasscodeVerifier:        make([]byte, 97),
	}, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("MatterInvoke(OpenWindow): %v", err)
	}
	if resolverCalls != 1 {
		t.Errorf("resolver call count = %d, want 1", resolverCalls)
	}
	if resolverFabric != 4 {
		t.Errorf("resolver fabricIndex = %d, want 4", resolverFabric)
	}
	if got := ctrl.capturedParams.AdminVendorID; got != 0x1349 {
		t.Errorf("AdminVendorID = 0x%04X, want 0x1349 (resolved)", got)
	}
}

// TestAdmComm_Invoke_OpenWindow_PASEReturnsErrBusy guards that
// OpenCommissioningWindow is rejected when called over a PASE session
// (fabricIndex == 0). Multi-Admin commissioning windows may only be opened
// over a CASE session per chip AdministratorCommissioningCluster.cpp
// VerifyOrExit(session.IsSecureSession(), ...).
func TestAdmComm_Invoke_OpenWindow_PASEReturnsErrBusy(t *testing.T) {
	t.Parallel()
	ac := newAdmComm()
	ctrl := &fakeWindowController{}
	ac.SetController(ctrl)

	var resolverCalls int
	ac.SetVendorIDResolver(func(_ context.Context, _ uint8) uint16 {
		resolverCalls++
		return 0xBEEF
	})

	// No WithFabricFilter wrapping → FabricFilterFromContext returns fabricIndex == 0 (PASE).
	// The cluster must reject the command with ErrAdmCommBusy.
	_, err := ac.MatterInvoke(context.Background(), admCommCmdOpenWindow, wire.OpenWindowParams{
		CommissioningTimeoutSeconds: 300,
		Iterations:                  1000,
		Salt:                        make([]byte, 16),
		PAKEPasscodeVerifier:        make([]byte, 97),
	}, hmenum.CommandPriorityHigh)
	if !errors.Is(err, wire.ErrAdmCommBusy) {
		t.Errorf("MatterInvoke(OpenWindow, PASE): want ErrAdmCommBusy, got %v", err)
	}
	// Resolver must not be called when the request was rejected before vendor lookup.
	if resolverCalls != 0 {
		t.Errorf("resolver call count = %d, want 0 (rejected before resolver reached)", resolverCalls)
	}
}

// TestAdmComm_Invoke_OpenWindow_CallerProvidedVendorIDWins guards the
// explicit-caller path: when the controller-side caller pre-populates
// AdminVendorID (test scenario, or alternative wiring), the resolver does
// not overwrite it. This keeps the resolver strictly opt-in.
func TestAdmComm_Invoke_OpenWindow_CallerProvidedVendorIDWins(t *testing.T) {
	t.Parallel()
	ac := newAdmComm()
	ctrl := &fakeWindowController{}
	ac.SetController(ctrl)

	ac.SetVendorIDResolver(func(_ context.Context, _ uint8) uint16 {
		return 0x1349
	})

	ctx := im.WithFabricFilter(context.Background(), true, 4)
	_, err := ac.MatterInvoke(ctx, admCommCmdOpenWindow, wire.OpenWindowParams{
		CommissioningTimeoutSeconds: 300,
		Iterations:                  1000,
		Salt:                        make([]byte, 16),
		PAKEPasscodeVerifier:        make([]byte, 97),
		AdminVendorID:               0xABCD,
	}, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("MatterInvoke(OpenWindow): %v", err)
	}
	if got := ctrl.capturedParams.AdminVendorID; got != 0xABCD {
		t.Errorf("AdminVendorID = 0x%04X, want 0xABCD (caller-provided wins)", got)
	}
}

// TestAdmComm_PakeParameterError_ClusterStatus locks the matter.js element
// definition: administrator-commissioning.element.ts:61 assigns id 0x3 to
// PakeParameterError. The wire ClusterStatus byte must be 0x03.
func TestAdmComm_PakeParameterError_ClusterStatus(t *testing.T) {
	t.Parallel()
	ac := newAdmComm()
	ctrl := &fakeWindowController{
		openWindowErr: wire.ErrAdmCommPakeParameter,
	}
	ac.SetController(ctrl)

	// OpenCommissioningWindow requires a CASE session (fabricIndex > 0).
	caseCtx := im.WithFabricFilter(context.Background(), true, 1)
	_, err := ac.MatterInvoke(caseCtx, admCommCmdOpenWindow, wire.OpenWindowParams{
		CommissioningTimeoutSeconds: 300,
		Iterations:                  1000,
		Salt:                        make([]byte, 16),
		PAKEPasscodeVerifier:        make([]byte, 97),
	}, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Fatal("expected error from PAKE controller, got nil")
	}
	type clusterStatusErr interface {
		MatterClusterStatus() uint8
	}
	cse, ok := err.(clusterStatusErr)
	if !ok {
		t.Fatalf("error %T does not implement MatterClusterStatus(); want 0x03", err)
	}
	if got := cse.MatterClusterStatus(); got != 0x03 {
		t.Errorf("MatterClusterStatus() = 0x%02X, want 0x03 (PakeParameterError)", got)
	}
}

func TestAdmComm_Reportable_ContainsRequiredAttributes(t *testing.T) {
	t.Parallel()
	ac := newAdmComm()
	reportable := ac.MatterReportable()

	want := []uint32{admCommAttrWindow, admCommAttrFabric, admCommAttrVendor}
	for _, id := range want {
		found := slices.Contains(reportable, id)
		if !found {
			t.Errorf("MatterReportable() = %v; missing attr 0x%04X", reportable, id)
		}
	}
}

// TestAdmComm_FabricCounter_SetsIsUncommissioned verifies that when a
// FabricCounter returning 0 is wired, OpenCommissioningWindow sets
// IsUncommissioned=true on the params, enabling the 48-h timeout window.
func TestAdmComm_FabricCounter_SetsIsUncommissioned(t *testing.T) {
	t.Parallel()

	ctrl := &fakeWindowController{}
	ac := wire.NewAdministratorCommissioning()
	ac.SetController(ctrl)
	// Wire a FabricCounter that returns 0 (no fabrics yet — uncommissioned).
	ac.SetFabricCounter(func(_ context.Context) (int, error) { return 0, nil })

	params := wire.OpenWindowParams{
		CommissioningTimeoutSeconds: 180,
		PAKEPasscodeVerifier:        make([]byte, 97),
		Iterations:                  1000,
		Salt:                        make([]byte, 16),
	}
	// OpenCommissioningWindow requires a CASE session (fabricIndex > 0).
	caseCtx := im.WithFabricFilter(context.Background(), true, 1)
	_, err := ac.MatterInvoke(caseCtx, 0x00, params, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("MatterInvoke OpenCommissioningWindow: %v", err)
	}
	if !ctrl.capturedParams.IsUncommissioned {
		t.Error("IsUncommissioned = false but FabricCount == 0; expected true")
	}
}

// TestAdmComm_FabricCounter_CommissionedBridgeUses900sLimit verifies that
// when FabricCounter returns > 0, IsUncommissioned is not set, so the
// standard 900-s cap applies.
func TestAdmComm_FabricCounter_CommissionedBridgeUses900sLimit(t *testing.T) {
	t.Parallel()

	ctrl := &fakeWindowController{}
	ac := wire.NewAdministratorCommissioning()
	ac.SetController(ctrl)
	// Wire a FabricCounter that returns 1 (bridge already commissioned).
	ac.SetFabricCounter(func(_ context.Context) (int, error) { return 1, nil })

	params := wire.OpenWindowParams{
		CommissioningTimeoutSeconds: 180,
		PAKEPasscodeVerifier:        make([]byte, 97),
		Iterations:                  1000,
		Salt:                        make([]byte, 16),
	}
	// OpenCommissioningWindow requires a CASE session (fabricIndex > 0).
	caseCtx := im.WithFabricFilter(context.Background(), true, 1)
	_, err := ac.MatterInvoke(caseCtx, 0x00, params, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("MatterInvoke OpenCommissioningWindow: %v", err)
	}
	if ctrl.capturedParams.IsUncommissioned {
		t.Error("IsUncommissioned = true but FabricCount == 1; expected false")
	}
}

func TestAdmComm_MatterAttributes_NonEmpty(t *testing.T) {
	t.Parallel()
	ctrl := &fakeWindowController{}
	a := wire.NewAdministratorCommissioning()
	a.SetController(ctrl)
	attrs := a.MatterAttributes()
	if len(attrs) == 0 {
		t.Error("MatterAttributes: want non-empty")
	}
}

func TestAdmComm_MatterAcceptedCommands_NonEmpty(t *testing.T) {
	t.Parallel()
	a := wire.NewAdministratorCommissioning()
	cmds := a.MatterAcceptedCommands()
	if len(cmds) == 0 {
		t.Error("MatterAcceptedCommands: want non-empty")
	}
}

func TestAdmComm_MatterGeneratedCommands_IsNilOrEmpty(t *testing.T) {
	t.Parallel()
	a := wire.NewAdministratorCommissioning()
	cmds := a.MatterGeneratedCommands()
	// AdministratorCommissioning has no generated response commands.
	if len(cmds) != 0 {
		t.Errorf("MatterGeneratedCommands: want empty, got %v", cmds)
	}
}

// TestAdmComm_TypedErrors_Interface exercises the Error() and MatterStatusCode()
// methods on the three typed error sentinels exposed by the package.
func TestAdmComm_TypedErrors_Interface(t *testing.T) {
	t.Parallel()
	// ErrAdmCommBusy
	if wire.ErrAdmCommBusy == nil {
		t.Fatal("ErrAdmCommBusy is nil")
	}
	if wire.ErrAdmCommBusy.Error() == "" {
		t.Error("ErrAdmCommBusy.Error() is empty")
	}
	if wire.ErrAdmCommBusy.MatterStatusCode() == 0 {
		t.Error("ErrAdmCommBusy.MatterStatusCode() is 0 — want non-zero status code")
	}

	// ErrAdmCommPakeParameter
	if wire.ErrAdmCommPakeParameter == nil {
		t.Fatal("ErrAdmCommPakeParameter is nil")
	}
	if wire.ErrAdmCommPakeParameter.Error() == "" {
		t.Error("ErrAdmCommPakeParameter.Error() is empty")
	}
	if wire.ErrAdmCommPakeParameter.MatterStatusCode() == 0 {
		t.Error("ErrAdmCommPakeParameter.MatterStatusCode() is 0")
	}

	// ErrAdmCommWindowNotOpen
	if wire.ErrAdmCommWindowNotOpen == nil {
		t.Fatal("ErrAdmCommWindowNotOpen is nil")
	}
	if wire.ErrAdmCommWindowNotOpen.Error() == "" {
		t.Error("ErrAdmCommWindowNotOpen.Error() is empty")
	}
	if wire.ErrAdmCommWindowNotOpen.MatterStatusCode() == 0 {
		t.Error("ErrAdmCommWindowNotOpen.MatterStatusCode() is 0")
	}
}
