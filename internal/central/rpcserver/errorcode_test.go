// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package rpcserver

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
)

// TestDecodeErrorCode pins the two encodings the CCU uses for the
// error_code argument, plus the undecodable case.
func TestDecodeErrorCode(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   xmlrpc.Value
		want int
	}{
		{"integer", xmlrpc.IntValue(-5), -5},
		{"stringified integer", xmlrpc.StringValue("-7"), -7},
		{"positive stringified integer", xmlrpc.StringValue("42"), 42},
		{"unparsable string", xmlrpc.StringValue("garbage"), 0},
		{"boolean", xmlrpc.BoolValue(true), 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := decodeErrorCode(tc.in); got != tc.want {
				t.Fatalf("decodeErrorCode(%v) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}
