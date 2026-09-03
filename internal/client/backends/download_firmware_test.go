// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package backends

import (
	"context"
	"errors"
	"testing"
)

// TestDownloadFirmwareCallsTheParameterlessCCUMethod pins the wire call.
//
// The CCU's entry point is the JSON-RPC method CCU.downloadFirmware and it
// takes no arguments: it builds the download URL from the box's own
// /VERSION and board serial and writes the image to /tmp/fup.tgz. Passing a
// caller-supplied URL is not merely unnecessary, there is nowhere to put it.
//
// The predecessor POSTed `action=download_firmware` to
// /config/cp_maintenance.cgi, an action that CGI does not define; the
// unknown action fell through to an HTML page served under HTTP 200 and the
// method read that as success.
func TestDownloadFirmwareCallsTheParameterlessCCUMethod(t *testing.T) {
	t.Parallel()

	fc := &fakeCaller{reply: true}
	b := &CcuBackend{json: fc}

	if err := b.DownloadFirmware(context.Background()); err != nil {
		t.Fatalf("DownloadFirmware: %v", err)
	}

	call, _ := fc.lastArg.Load().([]any)
	if len(call) != 2 {
		t.Fatalf("no call recorded: %v", call)
	}
	if method, _ := call[0].(string); method != "CCU.downloadFirmware" {
		t.Errorf("method = %q, want CCU.downloadFirmware", method)
	}
	if args, _ := call[1].([]any); len(args) != 0 {
		t.Errorf("args = %v, want none: the CCU method takes no parameters", args)
	}
}

// TestDownloadFirmwareTreatsFalseAsFailure is the guard the previous
// implementation lacked.
//
// CCU.downloadFirmware answers with a JSON boolean, and false is a real
// failure: the box's major version maps to no product id, or the transfer
// to /tmp/fup.tgz failed. A successful transport exchange is therefore not
// a successful download, and reporting one as the other is exactly the
// defect that let a CGI error page count as a firmware fetch.
func TestDownloadFirmwareTreatsFalseAsFailure(t *testing.T) {
	t.Parallel()

	b := &CcuBackend{json: &fakeCaller{reply: false}}

	err := b.DownloadFirmware(context.Background())
	if err == nil {
		t.Fatal("DownloadFirmware returned nil for a false result: the CCU reported no download")
	}
	if errors.Is(err, ErrUnsupported) {
		t.Errorf("error = %v, want a failure report, not ErrUnsupported", err)
	}
}

// TestDownloadFirmwareRejectsANonBooleanResult keeps the result check from
// degrading into "anything that is not literally false counts as success".
func TestDownloadFirmwareRejectsANonBooleanResult(t *testing.T) {
	t.Parallel()

	b := &CcuBackend{json: &fakeCaller{reply: "true"}}

	if err := b.DownloadFirmware(context.Background()); err == nil {
		t.Fatal("DownloadFirmware returned nil for a string result")
	}
}

// TestDownloadFirmwareWithoutJSONRPCIsUnsupported covers the backends that
// reach a CCU over XML-RPC only.
func TestDownloadFirmwareWithoutJSONRPCIsUnsupported(t *testing.T) {
	t.Parallel()

	b := &CcuBackend{}

	if err := b.DownloadFirmware(context.Background()); !errors.Is(err, ErrUnsupported) {
		t.Errorf("error = %v, want ErrUnsupported", err)
	}
}
