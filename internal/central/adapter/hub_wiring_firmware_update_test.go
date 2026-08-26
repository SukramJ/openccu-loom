// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/hub"
)

// The happy path (script_available + success both true) is covered by
// TestHubJSONRPCWriter_TriggerFirmwareUpdate_Success in
// adapter_multi_unit_test.go.

// TestHubJSONRPCWriter_TriggerFirmwareUpdate_ScriptUnavailable pins the
// defect: trigger_firmware_update.fn (see
// internal/client/rega/scripts/trigger_firmware_update.fn) reports its
// outcome as a structured {success, script_available, message} object, not
// through a ReGa.runScript transport error. A CCU without
// checkFirmwareUpdate.sh (i.e. not OpenCCU) still answers the call
// successfully — success:false, script_available:false. Reading only the
// transport error, as the previous implementation did, returned nil here
// and let the caller believe the update had started.
func TestHubJSONRPCWriter_TriggerFirmwareUpdate_ScriptUnavailable(t *testing.T) {
	t.Parallel()
	m := newProgramDeleteMock(t, `{"success":false,"script_available":false,"message":"checkFirmwareUpdate.sh not available or missing required flags (only supported on OpenCCU)"}`)
	w := newWriterAgainst(t, m.srv.URL)

	err := w.TriggerFirmwareUpdate(context.Background())
	if err == nil {
		t.Fatal("expected an error when the CCU never staged the update, got nil")
	}
	if !strings.Contains(err.Error(), "checkFirmwareUpdate.sh not available") {
		t.Errorf("error must surface the CCU's decline reason, got: %v", err)
	}
}

// TestUpdateInstall_ScriptUnavailable_DoesNotReportInProgress exercises the
// defect through the actual production path: [hub.Update.Install] wired to
// a real [hubJSONRPCWriter] against a CCU that declines the firmware
// trigger. Before the fix, TriggerFirmwareUpdate swallowed the CCU-level
// decline and returned nil, so Install flipped InProgress(true) and
// reported success for an update that never started — exactly the operator-
// visible symptom described in the defect report. With the fix, Install
// must propagate the error and must NOT report an in-progress update.
func TestUpdateInstall_ScriptUnavailable_DoesNotReportInProgress(t *testing.T) {
	t.Parallel()
	m := newProgramDeleteMock(t, `{"success":false,"script_available":false,"message":"checkFirmwareUpdate.sh not available or missing required flags (only supported on OpenCCU)"}`)
	w := newWriterAgainst(t, m.srv.URL)

	u := hub.NewUpdate()
	u.SetFirmwareUpdater(w)

	err := u.Install(context.Background())
	if err == nil {
		t.Fatal("expected Install to fail when the CCU declined the firmware trigger")
	}
	if errors.Is(err, hub.ErrNoFirmwareUpdater) {
		t.Fatalf("err must be the CCU decline, not ErrNoFirmwareUpdater: %v", err)
	}
	if u.InProgress() {
		t.Error("Install must not report an in-progress update when the CCU never staged one")
	}
}
