// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package rpcserver

import "testing"

// TestRouteFromPathRejectsTraversal is a contract test that pins the
// XML-RPC callback router to a strict allowlist on the central-name
// segment. Any path shape outside `/RPC2/[A-Za-z0-9_-]+` is rejected
// up-front so untrusted CCUs cannot misroute callbacks between
// centrals via path traversal or encoded slashes.
func TestRouteFromPathRejectsTraversal(t *testing.T) {
	cases := []struct {
		name string
		path string
		ok   bool
		want string
	}{
		// Happy paths — accepted shapes.
		{"plain", "/RPC2/ccu-01", true, "ccu-01"},
		{"underscore", "/RPC2/ccu_01", true, "ccu_01"},
		{"alnum", "/RPC2/ccuMain42", true, "ccuMain42"},

		// Reject — wrong prefix.
		{"no-prefix", "/RPC1/ccu-01", false, ""},
		{"empty-segment", "/RPC2/", false, ""},
		{"trailing-slash", "/RPC2/ccu-01/", false, ""},

		// Reject — traversal / multi-segment / encoded shapes.
		{"dotdot", "/RPC2/..", false, ""},
		{"dotdot-target", "/RPC2/../other", false, ""},
		{"multi-segment", "/RPC2/ccu1/extra", false, ""},
		// Note: net/http decodes %2F into "/" before routing, so this
		// arrives as `/RPC2/ccu1/other` and is rejected by the
		// "contains /" check. The regex is the second line of defense.
		{"encoded-slash-decoded", "/RPC2/ccu1/other", false, ""},

		// Reject — disallowed characters.
		{"space", "/RPC2/ccu 01", false, ""},
		{"dot", "/RPC2/ccu.01", false, ""},
		{"colon", "/RPC2/ccu:01", false, ""},
		{"percent", "/RPC2/ccu%01", false, ""},
		{"null-byte", "/RPC2/ccu\x00", false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := routeFromPath(tc.path)
			if ok != tc.ok {
				t.Fatalf("ok=%v want %v (path=%q got=%q)", ok, tc.ok, tc.path, got)
			}
			if got != tc.want {
				t.Fatalf("got=%q want=%q (path=%q)", got, tc.want, tc.path)
			}
		})
	}
}
