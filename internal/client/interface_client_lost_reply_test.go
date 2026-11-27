// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/client/reliability"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// Transport errors shaped the way the XML-RPC and BIN-RPC clients wrap
// them: a reply lost after the call went out, and a dial that never
// reached the CCU.
var (
	errLostReply   = fmt.Errorf("%w: %w", hmerr.ErrReplyLost, io.ErrUnexpectedEOF)
	errRefusedDial = fmt.Errorf("%w: %w", hmerr.ErrNoConnection, syscall.ECONNREFUSED)
)

// newFlakyWireIC builds an interface client whose wire fails the first call
// with firstErr and answers every later one; calls counts what reached it.
// It is driven through a CcuBackend over a BackendCaller, the production
// path of a write (ValueWriter → Operations.SetValue → BackendCaller.CallAt
// → InterfaceClient.Call).
func newFlakyWireIC(t *testing.T, firstErr error) (backends.Operations, *InterfaceClient, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	caller := CallerFunc(func(context.Context, string, []any) (any, error) {
		if calls.Add(1) == 1 {
			return nil, firstErr
		}
		return "", nil
	})
	ic, err := New(Config{
		CentralName: "c",
		Interface:   hmenum.InterfaceHmIPRF,
		Caller:      caller,
		Retrier: reliability.NewRetrier(reliability.RetryConfig{
			MaxAttempts: 3, Initial: time.Millisecond, Max: time.Millisecond,
		}),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	bc := NewBackendCaller(ic, hmenum.CommandPriorityLow)
	return backends.NewCcuBackendForInterface(hmenum.InterfaceHmIPRF, bc, nil, nil), ic, &calls
}

func TestInterfaceClientRepeatsALostReplyOnlyForReads(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for _, tc := range []struct {
		name     string
		firstErr error
		call     func(backends.Operations, *InterfaceClient) error
		want     int32
		wantErr  error
	}{
		{
			name:     "read after a lost reply is repeated",
			firstErr: errLostReply,
			call: func(b backends.Operations, _ *InterfaceClient) error {
				_, err := b.GetValue(ctx, "0001D3C99C3C93:1", "LEVEL")
				return err
			},
			want: 2,
		},
		{
			name:     "ACTION press after a lost reply is sent once",
			firstErr: errLostReply,
			call: func(b backends.Operations, _ *InterfaceClient) error {
				return b.SetValue(ctx, "0001D3C99C3C93:1", "PRESS_SHORT", true,
					hmenum.CommandPriorityLow, hmenum.CommandRxModeUnset)
			},
			want:    1,
			wantErr: hmerr.ErrReplyLost,
		},
		{
			name:     "paramset write after a lost reply is sent once",
			firstErr: errLostReply,
			call: func(b backends.Operations, _ *InterfaceClient) error {
				return b.PutParamset(ctx, "0001D3C99C3C93:0", hmenum.ParamsetKeyMaster,
					map[string]any{"CYCLIC_INFO_MSG": true}, hmenum.CommandPriorityLow, hmenum.CommandRxModeUnset)
			},
			want:    1,
			wantErr: hmerr.ErrReplyLost,
		},
		{
			name:     "method the classifier does not know is sent once",
			firstErr: errLostReply,
			call: func(_ backends.Operations, ic *InterfaceClient) error {
				_, err := ic.Call(ctx, "installFirmware", []any{"0001D3C99C3C93"}, hmenum.CommandPriorityLow, "")
				return err
			},
			want:    1,
			wantErr: hmerr.ErrReplyLost,
		},
		{
			name:     "write whose dial was refused is retried",
			firstErr: errRefusedDial,
			call: func(b backends.Operations, _ *InterfaceClient) error {
				return b.SetValue(ctx, "0001D3C99C3C93:1", "PRESS_SHORT", true,
					hmenum.CommandPriorityLow, hmenum.CommandRxModeUnset)
			},
			want: 2,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b, ic, calls := newFlakyWireIC(t, tc.firstErr)
			err := tc.call(b, ic)
			switch {
			case tc.wantErr == nil && err != nil:
				t.Fatalf("call = %v, want success", err)
			case tc.wantErr != nil && !errors.Is(err, tc.wantErr):
				t.Fatalf("call = %v, want %v", err, tc.wantErr)
			}
			if n := calls.Load(); n != tc.want {
				t.Fatalf("wire saw %d calls, want %d", n, tc.want)
			}
		})
	}
}
