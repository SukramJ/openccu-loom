// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeReassembler struct {
	calls int
	err   error
}

func (f *fakeReassembler) Reassemble(context.Context) error {
	f.calls++
	return f.err
}

type fakeFabricPurger struct {
	revoked []uint8
	err     error
	// revokeErrOn makes RevokeFabric fail for one fabric index, so the
	// partial-reset path can be exercised.
	revokeErrOn uint8
}

func (f *fakeFabricPurger) ListFabricIndexes(context.Context) ([]uint8, error) {
	return []uint8{1, 2}, f.err
}

func (f *fakeFabricPurger) RevokeFabric(_ context.Context, idx uint8) error {
	if f.revokeErrOn != 0 && idx == f.revokeErrOn {
		return errors.New("fabric not found")
	}
	f.revoked = append(f.revoked, idx)
	return nil
}

// TestForceSyncReassemblesTheTopology covers the operator action for the
// case the bridge and the CCU disagree about what exists.
//
// Endpoints are assembled from the device model, and the assembly is
// re-run when the model changes. When something goes missing anyway —
// the change arrived while the bridge was down, an exposure was edited
// outside the usual path — the operator's only remedy was restarting the
// daemon, which drops every controller session to fix a list.
func TestForceSyncReassemblesTheTopology(t *testing.T) {
	t.Parallel()

	rs := &fakeReassembler{}
	rec := httptest.NewRecorder()
	MatterForceSync(rs, nil, nil)(rec,
		httptest.NewRequest(http.MethodPost, "/api/v1/matter/force-sync", http.NoBody))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if rs.calls != 1 {
		t.Errorf("Reassemble called %d times, want 1", rs.calls)
	}
}

// TestForceSyncReportsAFailedReassembly keeps the action honest: a
// silent 204 on a failed re-assembly would tell the operator the list is
// now correct when it is not.
func TestForceSyncReportsAFailedReassembly(t *testing.T) {
	t.Parallel()

	rs := &fakeReassembler{err: errors.New("assembler refused")}
	rec := httptest.NewRecorder()
	MatterForceSync(rs, nil, nil)(rec,
		httptest.NewRequest(http.MethodPost, "/api/v1/matter/force-sync", http.NoBody))

	if rec.Code < 500 {
		t.Errorf("status = %d, want a server error when re-assembly fails", rec.Code)
	}
}

// TestFactoryResetRefusesWithoutTheWrittenConfirmation is the guard that
// matters most on this endpoint.
//
// The reset removes every fabric: each paired controller loses the
// bridge and has to commission it again. There is no undo. A POST with
// no body — a stray curl, a REST client's "replay last request", a
// mis-scripted automation — must not be able to do that, so the caller
// has to name the action in the body.
func TestFactoryResetRefusesWithoutTheWrittenConfirmation(t *testing.T) {
	t.Parallel()

	for _, body := range []string{"", "{}", `{"confirm":"yes"}`, `{"confirm":""}`} {
		p := &fakeFabricPurger{}
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/matter/factory-reset",
			strings.NewReader(body))
		MatterFactoryReset(p, nil, nil)(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("body %q: status = %d, want 400 — an unconfirmed reset must not unpair "+
				"every controller", body, rec.Code)
		}
		if len(p.revoked) != 0 {
			t.Errorf("body %q: revoked %v despite refusing the request", body, p.revoked)
		}
	}
}

// TestFactoryResetRemovesEveryFabricWhenConfirmed pins the other half:
// once the caller has said what they mean, the action has to be
// complete. A reset that leaves one fabric behind leaves the bridge
// paired to a controller the operator believes they removed.
func TestFactoryResetRemovesEveryFabricWhenConfirmed(t *testing.T) {
	t.Parallel()

	p := &fakeFabricPurger{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/matter/factory-reset",
		strings.NewReader(`{"confirm":"remove-all-fabrics"}`))
	MatterFactoryReset(p, nil, nil)(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if len(p.revoked) != 2 || p.revoked[0] != 1 || p.revoked[1] != 2 {
		t.Errorf("revoked %v, want every fabric", p.revoked)
	}
}

// TestMaintenanceActionsNeedAnEnabledBridge keeps a disabled bridge from
// answering as though it had acted.
func TestMaintenanceActionsNeedAnEnabledBridge(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	MatterForceSync(nil, nil, nil)(rec,
		httptest.NewRequest(http.MethodPost, "/api/v1/matter/force-sync", http.NoBody))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("force-sync on a disabled bridge = %d, want 503", rec.Code)
	}

	rec = httptest.NewRecorder()
	MatterFactoryReset(nil, nil, nil)(rec,
		httptest.NewRequest(http.MethodPost, "/api/v1/matter/factory-reset",
			strings.NewReader(`{"confirm":"remove-all-fabrics"}`)))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("factory-reset on a disabled bridge = %d, want 503", rec.Code)
	}
}

// TestFactoryResetAuditsThePartOfTheResetThatLanded pins the durable
// record for the unrecoverable half of a failed reset: the fabrics that
// were already removed stay removed, so the audit log has to name them
// even though the request fails.
func TestFactoryResetAuditsThePartOfTheResetThatLanded(t *testing.T) {
	t.Parallel()

	p := &fakeFabricPurger{revokeErrOn: 2}
	auditRec := &captureRecorder{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/matter/factory-reset",
		strings.NewReader(`{"confirm":"remove-all-fabrics"}`))
	MatterFactoryReset(p, nil, auditRec)(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if len(auditRec.entries) != 1 {
		t.Fatalf("audit entries = %d, want 1 for the fabric that was removed", len(auditRec.entries))
	}
	if !strings.Contains(auditRec.entries[0].Note, "removed 1 of 2") {
		t.Errorf("audit note = %q, want it to name what was removed", auditRec.entries[0].Note)
	}
	if !strings.Contains(rec.Body.String(), "removed 1 of 2") {
		t.Errorf("response = %s, want an honest partial-reset title", rec.Body.String())
	}
}
