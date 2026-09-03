// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// interface_client_paramset_dispatch_test.go — pins which backend method
// InterfaceClient.PutParamset selects for its second argument: a paramset
// key goes to PutParamset, a peer channel address to PutLinkParamset.

package client_test

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// paramsetDispatchBackend counts which of the two paramset writes the
// orchestration layer picked. It embeds orchBackend for the rest of the
// backends.Operations surface.
type paramsetDispatchBackend struct {
	*orchBackend
	plain int
	link  int
}

func (b *paramsetDispatchBackend) PutParamset(
	context.Context, string, hmenum.ParamsetKey, map[string]any,
	hmenum.CommandPriority, hmenum.CommandRxMode,
) error {
	b.plain++
	return nil
}

func (b *paramsetDispatchBackend) PutLinkParamset(
	context.Context, string, string, map[string]any,
) error {
	b.link++
	return nil
}

// TestPutParamsetDispatchesOnChannelAddressGrammar drives both branches
// through the production PutParamset entry point.
func TestPutParamsetDispatchesOnChannelAddressGrammar(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		second    string
		wantPlain int
		wantLink  int
	}{
		{name: "paramset key", second: string(hmenum.ParamsetKeyMaster), wantPlain: 1, wantLink: 0},
		{name: "peer channel address", second: "MEQ0123456:1", wantPlain: 0, wantLink: 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			nop := client.CallerFunc(func(_ context.Context, _ string, _ []any) (any, error) {
				return nil, nil
			})
			ic, err := client.New(client.Config{
				CentralName: "test",
				Interface:   hmenum.InterfaceHmIPRF,
				Caller:      nop,
			})
			if err != nil {
				t.Fatalf("client.New: %v", err)
			}
			defer ic.Close()

			b := &paramsetDispatchBackend{orchBackend: &orchBackend{}}
			if err := ic.PutParamset(
				context.Background(), b,
				"ABC0123456:2", tc.second,
				map[string]any{"K": "V"},
				hmenum.CommandPriorityHigh, hmenum.CommandRxModeUnset, false,
			); err != nil {
				t.Fatalf("PutParamset: %v", err)
			}

			if b.plain != tc.wantPlain || b.link != tc.wantLink {
				t.Errorf(
					"second arg %q dispatched to PutParamset=%d PutLinkParamset=%d, want %d/%d",
					tc.second, b.plain, b.link, tc.wantPlain, tc.wantLink,
				)
			}
		})
	}
}
