// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package backends

import (
	"context"
	"reflect"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// replayCaller answers every call with one canned reply and records the
// method plus positional arguments it was handed.
type replayCaller struct {
	reply      any
	lastMethod string
	lastArgs   []any
}

func (c *replayCaller) Call(_ context.Context, method string, args ...any) (any, error) {
	c.lastMethod = method
	c.lastArgs = args
	return c.reply, nil
}

func (c *replayCaller) CallAt(
	ctx context.Context, _ hmenum.CommandPriority, method string, args ...any,
) (any, error) {
	return c.Call(ctx, method, args...)
}

// TestCcuAndHomegearDecodeTheSameWire drives a CcuBackend and a
// HomegearBackend over identical wire replies and requires the two to
// produce identical decoded results and identical wire calls for the six
// link/description methods they share.
//
// The two backends carried byte-identical copies of these decoders. A copy
// is invisible while it agrees, so this compares the two production entry
// points rather than the helper they now both delegate to: a one-sided
// edit to either backend shows up as a mismatch here.
func TestCcuAndHomegearDecodeTheSameWire(t *testing.T) {
	t.Parallel()

	linksReply := []any{
		map[string]any{
			"SENDER":      "VCU0000001:1",
			"RECEIVER":    "VCU0000002:2",
			"NAME":        "link",
			"DESCRIPTION": "desc",
			"FLAGS":       3,
		},
		// A row with no SENDER must be dropped by both decoders.
		map[string]any{"RECEIVER": "VCU0000002:2"},
	}
	descReply := map[string]any{
		"LEVEL": map[string]any{
			"TYPE":       "FLOAT",
			"OPERATIONS": 7,
			"FLAGS":      1,
			"MIN":        0.0,
			"MAX":        1.0,
		},
	}

	cases := []struct {
		name  string
		reply any
		call  func(t *testing.T, ops Operations, caller *replayCaller) any
	}{
		{
			name:  "GetLinks",
			reply: linksReply,
			call: func(t *testing.T, ops Operations, _ *replayCaller) any {
				t.Helper()
				got, err := ops.GetLinks(context.Background(), "VCU0000001:1")
				if err != nil {
					t.Fatalf("GetLinks: %v", err)
				}
				return got
			},
		},
		{
			name:  "GetLinkPeers",
			reply: []any{"VCU0000002:2", "", 42, "VCU0000003:3"},
			call: func(t *testing.T, ops Operations, _ *replayCaller) any {
				t.Helper()
				got, err := ops.GetLinkPeers(context.Background(), "VCU0000001:1")
				if err != nil {
					t.Fatalf("GetLinkPeers: %v", err)
				}
				return got
			},
		},
		{
			name:  "GetLinkParamsetDescription",
			reply: descReply,
			call: func(t *testing.T, ops Operations, _ *replayCaller) any {
				t.Helper()
				got, err := ops.GetLinkParamsetDescription(context.Background(), "VCU0000001:1", "VCU0000002:2")
				if err != nil {
					t.Fatalf("GetLinkParamsetDescription: %v", err)
				}
				return got
			},
		},
		{
			name:  "GetLinkParamset",
			reply: map[string]any{"LEVEL": 0.5},
			call: func(t *testing.T, ops Operations, _ *replayCaller) any {
				t.Helper()
				got, err := ops.GetLinkParamset(context.Background(), "VCU0000001:1", "VCU0000002:2")
				if err != nil {
					t.Fatalf("GetLinkParamset: %v", err)
				}
				return got
			},
		},
		{
			name:  "PutLinkParamset",
			reply: nil,
			call: func(t *testing.T, ops Operations, _ *replayCaller) any {
				t.Helper()
				if err := ops.PutLinkParamset(
					context.Background(), "VCU0000001:1", "VCU0000002:2", map[string]any{"LEVEL": 1.0},
				); err != nil {
					t.Fatalf("PutLinkParamset: %v", err)
				}
				return nil
			},
		},
		{
			name:  "GetDeviceDescription",
			reply: map[string]any{"ADDRESS": "VCU0000001", "TYPE": "HmIP-PS"},
			call: func(t *testing.T, ops Operations, _ *replayCaller) any {
				t.Helper()
				got, err := ops.GetDeviceDescription(context.Background(), "VCU0000001")
				if err != nil {
					t.Fatalf("GetDeviceDescription: %v", err)
				}
				return got
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ccuCaller := &replayCaller{reply: tc.reply}
			hgCaller := &replayCaller{reply: tc.reply}
			ccuGot := tc.call(t, NewCcuBackend(ccuCaller, nil, nil), ccuCaller)
			hgGot := tc.call(t, NewHomegearBackend(hgCaller, nil), hgCaller)

			if !reflect.DeepEqual(ccuGot, hgGot) {
				t.Errorf("decoded result differs:\n ccu      = %#v\n homegear = %#v", ccuGot, hgGot)
			}
			if ccuCaller.lastMethod != hgCaller.lastMethod {
				t.Errorf("wire method differs: ccu=%q homegear=%q", ccuCaller.lastMethod, hgCaller.lastMethod)
			}
			if !reflect.DeepEqual(ccuCaller.lastArgs, hgCaller.lastArgs) {
				t.Errorf("wire args differ:\n ccu      = %#v\n homegear = %#v", ccuCaller.lastArgs, hgCaller.lastArgs)
			}
		})
	}
}

// TestLinkParamsetDescriptionUsesTheLinkParamsetKey pins the paramset key
// both backends put on the wire for a link-paramset description: the LINK
// enum member, read from hmenum rather than spelled as a literal.
func TestLinkParamsetDescriptionUsesTheLinkParamsetKey(t *testing.T) {
	t.Parallel()

	caller := &replayCaller{reply: map[string]any{}}
	if _, err := NewCcuBackend(caller, nil, nil).GetLinkParamsetDescription(
		context.Background(), "VCU0000001:1", "VCU0000002:2",
	); err != nil {
		t.Fatalf("GetLinkParamsetDescription: %v", err)
	}
	if len(caller.lastArgs) != 2 || caller.lastArgs[1] != string(hmenum.ParamsetKeyLink) {
		t.Fatalf("args=%#v, want paramset key %q", caller.lastArgs, hmenum.ParamsetKeyLink)
	}
}
