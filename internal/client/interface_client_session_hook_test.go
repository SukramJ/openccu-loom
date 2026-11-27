// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

// interface_client_session_hook_test.go — Config.SessionRecorderHook is
// reported to from InterfaceClient.Call: once per successful logical call,
// never for a failed one, once however many retry attempts the call took,
// once per coalesced group, and labelled with the client's transport.

package client_test

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/client/reliability"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// hookRecord is one observed SessionRecorderHook invocation.
type hookRecord struct {
	rpcType string
	method  string
	// params and response are the hook's own untyped wire values.
	params   any
	response any
}

// hookSpy collects SessionRecorderHook invocations; safe for concurrent use.
type hookSpy struct {
	mu      sync.Mutex
	records []hookRecord
}

func (s *hookSpy) hook(rpcType, method string, params, response any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, hookRecord{rpcType: rpcType, method: method, params: params, response: response})
}

func (s *hookSpy) snapshot() []hookRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]hookRecord(nil), s.records...)
}

// fastRetrier retries up to maxAttempts with no meaningful backoff.
func fastRetrier(maxAttempts int) *reliability.Retrier {
	return reliability.NewRetrier(reliability.RetryConfig{
		MaxAttempts: maxAttempts,
		Initial:     time.Microsecond,
		Max:         time.Microsecond,
		Jitter:      -1,
	})
}

func newHookClient(t *testing.T, cfg client.Config) *client.InterfaceClient {
	t.Helper()
	cfg.CentralName = "test"
	cfg.Interface = hmenum.InterfaceHmIPRF
	ic, err := client.New(cfg)
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	t.Cleanup(ic.Close)
	return ic
}

// TestSessionRecorderHookFiresOncePerSuccessfulCall pins the hook's payload:
// one invocation carrying the transport label, the method, the arguments as
// handed to Call and the decoded result.
func TestSessionRecorderHookFiresOncePerSuccessfulCall(t *testing.T) {
	t.Parallel()

	spy := &hookSpy{}
	reply := []any{map[string]any{"ADDRESS": "MEQ0123456"}}
	ic := newHookClient(t, client.Config{
		Caller: client.CallerFunc(func(context.Context, string, []any) (any, error) {
			return reply, nil
		}),
		SessionRecorderHook: spy.hook,
	})

	params := []any{"MEQ0123456:1", "LEVEL", 0.5}
	if _, err := ic.Call(context.Background(), "setValue", params, hmenum.CommandPriorityHigh, ""); err != nil {
		t.Fatalf("Call: %v", err)
	}

	got := spy.snapshot()
	if len(got) != 1 {
		t.Fatalf("hook called %d times, want 1", len(got))
	}
	want := hookRecord{rpcType: "xml-rpc", method: "setValue", params: params, response: reply}
	if !reflect.DeepEqual(got[0], want) {
		t.Errorf("hook recorded %+v, want %+v", got[0], want)
	}
}

// TestSessionRecorderHookSkipsAFailedCall pins that a call the reliability
// stack ends with an error is not recorded: the trace is a replayable record
// of what the CCU answered, and a failure has no answer to replay.
func TestSessionRecorderHookSkipsAFailedCall(t *testing.T) {
	t.Parallel()

	spy := &hookSpy{}
	ic := newHookClient(t, client.Config{
		Caller: client.CallerFunc(func(context.Context, string, []any) (any, error) {
			return nil, errors.New("transport down")
		}),
		Retrier:             fastRetrier(2),
		SessionRecorderHook: spy.hook,
	})

	if _, err := ic.Call(context.Background(), "listDevices", nil, hmenum.CommandPriorityHigh, ""); err == nil {
		t.Fatal("Call with a failing caller returned nil error")
	}
	if got := spy.snapshot(); len(got) != 0 {
		t.Errorf("hook called %d times for a failed call, want 0: %+v", len(got), got)
	}
}

// TestSessionRecorderHookFiresOnceAfterRetries pins that the hook sits
// outside the retry loop: a call that needed several wire attempts is one
// logical call and one trace entry.
func TestSessionRecorderHookFiresOnceAfterRetries(t *testing.T) {
	t.Parallel()

	spy := &hookSpy{}
	var (
		mu       sync.Mutex
		attempts int
	)
	ic := newHookClient(t, client.Config{
		Caller: client.CallerFunc(func(context.Context, string, []any) (any, error) {
			mu.Lock()
			defer mu.Unlock()
			attempts++
			if attempts < 3 {
				return nil, errors.New("transient")
			}
			return "ok", nil
		}),
		Retrier:             fastRetrier(5),
		SessionRecorderHook: spy.hook,
	})

	if _, err := ic.Call(context.Background(), "listDevices", nil, hmenum.CommandPriorityHigh, ""); err != nil {
		t.Fatalf("Call: %v", err)
	}
	mu.Lock()
	n := attempts
	mu.Unlock()
	if n != 3 {
		t.Fatalf("caller reached %d times, want 3 (two transient failures, then success)", n)
	}
	got := spy.snapshot()
	if len(got) != 1 {
		t.Fatalf("hook called %d times after %d attempts, want 1", len(got), n)
	}
	if got[0].response != "ok" {
		t.Errorf("hook response = %v, want the successful attempt's result \"ok\"", got[0].response)
	}
}

// TestSessionRecorderHookFiresOncePerCoalescedGroup pins that only the
// leader of a coalesced group reports: the followers share its single wire
// call, so recording them would invent calls the CCU never saw.
func TestSessionRecorderHookFiresOncePerCoalescedGroup(t *testing.T) {
	t.Parallel()

	const participants = 4
	spy := &hookSpy{}
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	ic := newHookClient(t, client.Config{
		Caller: client.CallerFunc(func(ctx context.Context, _ string, _ []any) (any, error) {
			once.Do(func() { close(started) })
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-release:
				return nil, nil
			}
		}),
		SessionRecorderHook: spy.hook,
	})

	const key = "setValue|0|VCU1234567:1|STATE|bool|true"
	params := []any{"VCU1234567:1", "STATE", true}
	errs := make(chan error, participants)
	call := func() {
		_, err := ic.Call(context.Background(), "setValue", params, hmenum.CommandPriorityLow, key)
		errs <- err
	}
	go call()
	<-started
	for range participants - 1 {
		go call()
	}
	deadline := time.Now().Add(5 * time.Second)
	for ic.Coalescer().Stats().Total < participants {
		if time.Now().After(deadline) {
			t.Fatalf("only %d of %d callers joined the coalesced group", ic.Coalescer().Stats().Total, participants)
		}
		time.Sleep(time.Millisecond)
	}
	close(release)
	for range participants {
		if err := <-errs; err != nil {
			t.Fatalf("coalesced Call: %v", err)
		}
	}

	if st := ic.Coalescer().Stats(); st.Executed != 1 {
		t.Fatalf("coalescer executed %d wire calls, want 1 — the group did not form", st.Executed)
	}
	if got := spy.snapshot(); len(got) != 1 {
		t.Errorf("hook called %d times for one coalesced group of %d callers, want 1", len(got), participants)
	}
}

// TestSessionRecorderHookNilIsANoOp pins that an unwired hook costs nothing
// and breaks nothing.
func TestSessionRecorderHookNilIsANoOp(t *testing.T) {
	t.Parallel()

	ic := newHookClient(t, client.Config{
		Caller: client.CallerFunc(func(context.Context, string, []any) (any, error) {
			return "ok", nil
		}),
	})
	out, err := ic.Call(context.Background(), "listDevices", nil, hmenum.CommandPriorityHigh, "")
	if err != nil {
		t.Fatalf("Call (nil hook): %v", err)
	}
	if out != "ok" {
		t.Errorf("Call (nil hook) = %v, want \"ok\"", out)
	}
}

// TestSessionRecorderHookLabelsTheTransport pins that the rpcType argument
// names the transport the client actually speaks: CUxD is driven over
// BIN-RPC, every other backend flavour over XML-RPC. A hard-coded label
// would report a CUxD trace as XML-RPC.
func TestSessionRecorderHookLabelsTheTransport(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		kind backends.Kind
		want string
	}{
		{name: "ccu", kind: backends.KindCCU, want: "xml-rpc"},
		{name: "cuxd", kind: backends.KindCUxD, want: "bin-rpc"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			spy := &hookSpy{}
			ic := newHookClient(t, client.Config{
				Caller: client.CallerFunc(func(context.Context, string, []any) (any, error) {
					return nil, nil
				}),
				BackendKind:         tc.kind,
				SessionRecorderHook: spy.hook,
			})
			if _, err := ic.Call(context.Background(), "listDevices", nil, hmenum.CommandPriorityHigh, ""); err != nil {
				t.Fatalf("Call: %v", err)
			}
			got := spy.snapshot()
			if len(got) != 1 {
				t.Fatalf("hook called %d times, want 1", len(got))
			}
			if got[0].rpcType != tc.want {
				t.Errorf("rpcType = %q, want %q for %v", got[0].rpcType, tc.want, tc.kind)
			}
		})
	}
}
