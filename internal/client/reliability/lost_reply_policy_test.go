// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package reliability_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"syscall"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/client/reliability"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// Every ReGa script carries an explicit verdict here, so a new script fails
// this test until someone has read its body and decided whether running it
// twice is harmless. The verdicts are the script bodies under
// internal/client/rega/scripts, not their names.
func TestEveryRegaScriptHasALostReplyVerdict(t *testing.T) {
	t.Parallel()
	repeatable := map[hmenum.RegaScript]bool{
		hmenum.RegaScriptAcknowledgeMessage:            false,
		hmenum.RegaScriptAcknowledgeAllServiceMessages: false,
		hmenum.RegaScriptAcknowledgeAllAlarmMessages:   false,
		hmenum.RegaScriptFetchAllDeviceData:            true,
		hmenum.RegaScriptGetAlarmMessages:              true,
		hmenum.RegaScriptGetBackendInfo:                true,
		hmenum.RegaScriptGetProgramDescriptions:        true,
		hmenum.RegaScriptGetSerial:                     true,
		hmenum.RegaScriptGetServiceMessages:            true,
		// Runs /bin/checkFirmwareUpdate.sh.
		hmenum.RegaScriptGetSystemUpdateInfo:           false,
		hmenum.RegaScriptGetSystemVariableDescriptions: true,
		hmenum.RegaScriptSetProgramState:               false,
		hmenum.RegaScriptExecuteProgramConditional:     false,
		hmenum.RegaScriptDeleteProgram:                 false,
		hmenum.RegaScriptSetSystemVariable:             false,
		hmenum.RegaScriptCreateSystemVariable:          false,
		hmenum.RegaScriptUpdateSystemVariable:          false,
		hmenum.RegaScriptSetDeviceRooms:                false,
		hmenum.RegaScriptSetDeviceFunctions:            false,
		hmenum.RegaScriptStartComTest:                  false,
		hmenum.RegaScriptPollComTest:                   true,
		hmenum.RegaScriptGetUserLevel:                  true,
		hmenum.RegaScriptCreateRoom:                    false,
		hmenum.RegaScriptRenameRoom:                    false,
		hmenum.RegaScriptDeleteRoom:                    false,
		hmenum.RegaScriptCreateFunction:                false,
		hmenum.RegaScriptRenameFunction:                false,
		hmenum.RegaScriptDeleteFunction:                false,
		hmenum.RegaScriptAcceptDeviceInInbox:           false,
		hmenum.RegaScriptGetInboxDevices:               true,
		hmenum.RegaScriptTriggerFirmwareUpdate:         false,
		hmenum.RegaScriptRebootCCU:                     false,
		hmenum.RegaScriptSetCCUPosition:                false,
		hmenum.RegaScriptPoweroffCCU:                   false,
		hmenum.RegaScriptCreateBackupStart:             false,
		// Removes the backup's pid file once it reports completed or failed.
		hmenum.RegaScriptCreateBackupStatus: false,
		hmenum.RegaScriptUsageBySysvar:      true,
	}
	for _, s := range hmenum.AllRegaScripts {
		want, ok := repeatable[s]
		if !ok {
			t.Errorf("%s has no lost-reply verdict: read its body, then add it here and, if it only reads, to readOnlyScripts", s)
			continue
		}
		if got := reliability.RepeatableScriptAfterLostReply(s); got != want {
			t.Errorf("RepeatableScriptAfterLostReply(%s) = %v, want %v", s, got, want)
		}
	}
	if len(repeatable) != len(hmenum.AllRegaScripts) {
		t.Errorf("verdict table has %d scripts, AllRegaScripts %d", len(repeatable), len(hmenum.AllRegaScripts))
	}
}

func TestRepeatableAfterLostReplyAllowsOnlyReads(t *testing.T) {
	t.Parallel()
	for method, want := range map[string]bool{
		"getValue":             true,
		"getParamset":          true,
		"SysVar.getAll":        true,
		"Program.get":          true,
		"setValue":             false,
		"putParamset":          false,
		"Program.execute":      false,
		"SysVar.setFloat":      false,
		"Session.login":        false,
		"Session.renew":        false,
		"ReGa.runScript":       false,
		"installFirmware":      false,
		"CCU.downloadFirmware": false,
	} {
		if got := reliability.RepeatableAfterLostReply(method); got != want {
			t.Errorf("RepeatableAfterLostReply(%q) = %v, want %v", method, got, want)
		}
	}
}

func TestDoMethodStopsAtALostReplyForAWriteOnly(t *testing.T) {
	t.Parallel()
	lost := fmt.Errorf("%w: %w", hmerr.ErrReplyLost, io.ErrUnexpectedEOF)
	refused := fmt.Errorf("%w: %w", hmerr.ErrNoConnection, syscall.ECONNREFUSED)
	for _, tc := range []struct {
		name   string
		method string
		err    error
		want   int
	}{
		{"read repeats a lost reply", "getValue", lost, 3},
		{"write stops at a lost reply", "setValue", lost, 1},
		{"write repeats a refused dial", "setValue", refused, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := reliability.NewRetrier(reliability.RetryConfig{
				MaxAttempts: 3, Initial: time.Millisecond, Max: time.Millisecond,
			})
			calls := 0
			err := r.DoMethod(context.Background(), tc.method, func(context.Context, int) error {
				calls++
				return tc.err
			})
			if !errors.Is(err, hmerr.ErrNoConnection) {
				t.Fatalf("DoMethod = %v, want the transport error back", err)
			}
			if calls != tc.want {
				t.Fatalf("fn ran %d times, want %d", calls, tc.want)
			}
		})
	}
}
