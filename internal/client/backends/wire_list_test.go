// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package backends

import (
	"context"
	"testing"
)

func TestAsWireListTreatsEmptyStringAsEmptyList(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		raw     any
		wantLen int
		wantOK  bool
	}{
		{"empty string", "", 0, true},
		{"array", []any{"a", "b"}, 2, true},
		{"empty array", []any{}, 0, true},
		{"non-empty string", "x", 0, false},
		{"struct", map[string]any{}, 0, false},
		{"int", 0, 0, false},
		{"nil", nil, 0, false},
	}
	for _, tc := range cases {
		list, ok := asWireList(tc.raw)
		if ok != tc.wantOK || len(list) != tc.wantLen {
			t.Errorf("%s: asWireList(%#v) = (%v, %v), want len %d ok %v", tc.name, tc.raw, list, ok, tc.wantLen, tc.wantOK)
		}
		if ok && list == nil {
			t.Errorf("%s: an accepted list must be non-nil", tc.name)
		}
	}
}

// TestListDecodersAcceptEmptyStringAnswer drives every production list
// decoder that routes through asWireList with the empty-string answer, a
// non-empty string and a real array.
func TestListDecodersAcceptEmptyStringAnswer(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	type decoder func(c Caller) (int, error)
	decoders := map[string]struct {
		run   decoder
		array []any
	}{
		"listDevices": {
			run: func(c Caller) (int, error) {
				out, err := listDevicesViaCaller(ctx, c, "ccu")
				return len(out), err
			},
			array: []any{map[string]any{"ADDRESS": "VCU0000001", "TYPE": "HM-X", "VERSION": 1}},
		},
		"listTeams": {
			run: func(c Caller) (int, error) {
				out, err := listStructArrayViaCaller(ctx, c, "ccu", "listTeams")
				return len(out), err
			},
			array: []any{map[string]any{"ADDRESS": "VCU0000001", "TYPE": "HM-X", "VERSION": 1}},
		},
		"getLinks": {
			run: func(c Caller) (int, error) {
				out, err := getLinksViaCaller(ctx, c, "ccu", "VCU0000001:1")
				return len(out), err
			},
			array: []any{map[string]any{"SENDER": "VCU0000001:1", "RECEIVER": "VCU0000002:1"}},
		},
		"getLinkPeers": {
			run: func(c Caller) (int, error) {
				out, err := getLinkPeersViaCaller(ctx, c, "ccu", "VCU0000001:1")
				return len(out), err
			},
			array: []any{"VCU0000002:1"},
		},
		"lite listBidcosInterfaces": {
			run: func(c Caller) (int, error) {
				out, err := (&LiteBackend{xml: c}).ListBidcosInterfaces(ctx, "")
				return len(out), err
			},
			array: []any{map[string]any{"ADDRESS": "KEQ0000001"}},
		},
	}
	for name, d := range decoders {
		if n, err := d.run(&replayCaller{reply: ""}); err != nil || n != 0 {
			t.Errorf("%s: empty-string answer = (%d, %v), want (0, nil)", name, n, err)
		}
		if _, err := d.run(&replayCaller{reply: "not a list"}); err == nil {
			t.Errorf("%s: non-empty string answer must stay a type error", name)
		}
		if n, err := d.run(&replayCaller{reply: d.array}); err != nil || n != 1 {
			t.Errorf("%s: array answer = (%d, %v), want (1, nil)", name, n, err)
		}
	}
}
