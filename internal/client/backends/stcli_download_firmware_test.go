// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package backends

import (
	"context"
	"errors"
	"testing"
)

// TestStCliDownloadFirmwareDoesNotClaimSuccess pins that the call reports what
// it can do rather than returning nil for a request the CCU never received.
//
// It used to POST `action=download_firmware` to /config/cp_maintenance.cgi.
// That CGI defines action_firmware_upload, action_firmware_update_go,
// action_firmware_update_confirm, action_createBackup and a dozen more — and
// no download_firmware (../OpenCCU-Base/www/config/cp_maintenance.cgi). The
// unknown action fell through to an HTML page under HTTP 200, and the method
// read that as success.
//
// The CCU's actual entry point is the JSON-RPC method CCU.downloadFirmware,
// which takes NO parameters: it fetches the newest CCU firmware for this
// box's own serial from eQ-3's update server
// (../OpenCCU-Base/www/api/methods/ccu/downloadFirmware.tcl). A caller-supplied
// URL has nowhere to go, which is why this reports unsupported instead of
// pretending.
func TestStCliDownloadFirmwareDoesNotClaimSuccess(t *testing.T) {
	t.Parallel()

	b := &CcuBackend{}
	b.SetDownloadFirmwareTransport("https://ccu.example", nil, func() string { return "sid-123" })

	err := b.DownloadFirmware(context.Background(), "https://example.invalid/fw.tgz")
	if err == nil {
		t.Fatal("DownloadFirmware returned nil: the CCU has no endpoint for a caller-supplied URL")
	}
	if !errors.Is(err, ErrUnsupported) {
		t.Errorf("error = %v, want ErrUnsupported", err)
	}
}
