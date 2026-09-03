// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package rpcserver

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
)

// TestCallbackListMethodsAdvertisesReplaceDevice drives system.listMethods
// over the wire against the real callback server and asserts the answer is an
// array containing "replaceDevice".
//
// Both properties are load-bearing on the CCU side: HSSManager::PlatformInit
// scans the returned array for that exact name
// (OpenCCU-Base src/libhsscomm/HSSManager.cpp:255-267) and treats a fault or
// any non-array as "unsupported" without an error, after which a replaced
// device arrives as deleteDevices + newDevices instead of replaceDevice.
// Dispatchability is a different property and is covered elsewhere; this pins
// the advertisement.
func TestCallbackListMethodsAdvertisesReplaceDevice(t *testing.T) {
	t.Parallel()

	h := &recordingHandlers{}
	_, url := newTestXMLRPCServer(t, "main", h)
	client, _ := xmlrpc.NewClient(xmlrpc.Config{URL: url})

	res, err := client.Call(context.Background(), "system.listMethods", nil)
	if err != nil {
		t.Fatalf("system.listMethods: %v", err)
	}
	arr, err := xmlrpc.AsArray(res)
	if err != nil {
		t.Fatalf("system.listMethods did not answer an array (%T): %v", res, err)
	}
	names := make([]string, 0, len(arr))
	for _, v := range arr {
		s, err := xmlrpc.AsString(v)
		if err != nil {
			continue
		}
		names = append(names, s)
	}
	for _, n := range names {
		if n == "replaceDevice" {
			return
		}
	}
	t.Fatalf("system.listMethods does not advertise replaceDevice; advertised: %v", names)
}
