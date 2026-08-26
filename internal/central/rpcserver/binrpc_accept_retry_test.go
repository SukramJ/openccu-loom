// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Coverage for BINRPCServer.Serve's accept-loop resilience: a recoverable
// Accept failure must not end the loop, because nothing restarts it — the
// listener would stay bound while every CUxD push callback silently stops
// arriving. A failure that leaves the socket unusable must end the loop
// AND unbind it.

package rpcserver

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// scriptedConn is a net.Conn whose Read fails immediately, so handleConn
// takes its decode-error exit and closes it. The close is the signal that
// the accept loop delivered this connection to a handler.
type scriptedConn struct {
	closed    chan struct{}
	closeOnce sync.Once
}

func (c *scriptedConn) Read([]byte) (int, error)         { return 0, io.EOF }
func (c *scriptedConn) Write(b []byte) (int, error)      { return len(b), nil }
func (c *scriptedConn) LocalAddr() net.Addr              { return scriptedAddr{} }
func (c *scriptedConn) RemoteAddr() net.Addr             { return scriptedAddr{} }
func (c *scriptedConn) SetDeadline(time.Time) error      { return nil }
func (c *scriptedConn) SetReadDeadline(time.Time) error  { return nil }
func (c *scriptedConn) SetWriteDeadline(time.Time) error { return nil }

func (c *scriptedConn) Close() error {
	c.closeOnce.Do(func() { close(c.closed) })
	return nil
}

type scriptedAddr struct{}

func (scriptedAddr) Network() string { return "tcp" }
func (scriptedAddr) String() string  { return "127.0.0.1:2001" }

// scriptedStep is one programmed Accept outcome: either a connection or
// an error.
type scriptedStep struct {
	conn net.Conn
	err  error
}

// scriptedListener replays a fixed sequence of Accept outcomes and then
// blocks until closed, the way a real listener blocks for the next peer.
type scriptedListener struct {
	mu        sync.Mutex
	steps     []scriptedStep
	accepts   atomic.Int64
	closed    chan struct{}
	closeOnce sync.Once
}

func newScriptedListener(steps ...scriptedStep) *scriptedListener {
	return &scriptedListener{steps: steps, closed: make(chan struct{})}
}

func (l *scriptedListener) Accept() (net.Conn, error) {
	l.accepts.Add(1)
	l.mu.Lock()
	if len(l.steps) > 0 {
		step := l.steps[0]
		l.steps = l.steps[1:]
		l.mu.Unlock()
		return step.conn, step.err
	}
	l.mu.Unlock()
	<-l.closed
	return nil, net.ErrClosed
}

func (l *scriptedListener) Close() error {
	l.closeOnce.Do(func() { close(l.closed) })
	return nil
}

func (l *scriptedListener) Addr() net.Addr { return scriptedAddr{} }

func (l *scriptedListener) isClosed() bool {
	select {
	case <-l.closed:
		return true
	default:
		return false
	}
}

// TestBINRPCServerServe_RetriesRecoverableAcceptErrors drives the accept
// loop through the failures a busy host really produces — a peer that
// reset between SYN and accept, and a transient descriptor shortage — and
// asserts the connection queued behind them still reaches a handler. If
// the loop returns on the first of them, the BIN-RPC callback listener is
// dead for the rest of the process lifetime.
func TestBINRPCServerServe_RetriesRecoverableAcceptErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{name: "peer reset between SYN and accept", err: syscall.ECONNABORTED},
		{name: "process descriptor exhaustion", err: syscall.EMFILE},
		{name: "system descriptor exhaustion", err: syscall.ENFILE},
		{name: "interrupted syscall", err: syscall.EINTR},
		{name: "wrapped by the net package", err: &net.OpError{Op: "accept", Err: syscall.ECONNABORTED}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn := &scriptedConn{closed: make(chan struct{})}
			ln := newScriptedListener(
				scriptedStep{err: tc.err},
				scriptedStep{err: tc.err},
				scriptedStep{conn: conn},
			)
			srv := newBINRPCServerOn(ln, nil, 50*time.Millisecond, nil)

			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- srv.Serve(ctx) }()

			select {
			case <-conn.closed:
			case err := <-done:
				cancel()
				t.Fatalf("Serve returned %v before the queued connection was handled — "+
					"a recoverable accept failure must not end the accept loop", err)
			case <-time.After(5 * time.Second):
				cancel()
				t.Fatal("the connection queued behind the recoverable accept failures never reached a handler")
			}

			cancel()
			_ = ln.Close()
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("Serve after cancel = %v, want nil", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("Serve did not return after ctx cancellation")
			}
		})
	}
}

// TestBINRPCServerServe_FatalAcceptErrorUnbindsListener pins the other
// half: a failure that is not recoverable ends the loop, and the listener
// is closed on the way out so the callback port is not held by a process
// that no longer accepts on it.
func TestBINRPCServerServe_FatalAcceptErrorUnbindsListener(t *testing.T) {
	fatal := errors.New("listener is gone")
	ln := newScriptedListener(scriptedStep{err: fatal})
	srv := newBINRPCServerOn(ln, nil, 50*time.Millisecond, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()

	select {
	case err := <-done:
		if !errors.Is(err, fatal) {
			t.Fatalf("Serve = %v, want it to wrap %v", err, fatal)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return on a fatal accept error")
	}
	if !ln.isClosed() {
		t.Fatal("listener still bound after Serve gave up — the callback port would stay held with no acceptor")
	}
}

// TestNextAcceptRetryDelay locks the backoff envelope to the one
// http.Server.Serve uses, so both callback listeners behave alike under
// descriptor pressure.
func TestNextAcceptRetryDelay(t *testing.T) {
	got := nextAcceptRetryDelay(0)
	if got != 5*time.Millisecond {
		t.Fatalf("first delay = %v, want 5ms", got)
	}
	for range 20 {
		next := nextAcceptRetryDelay(got)
		if next <= got && got != time.Second {
			t.Fatalf("delay did not grow: %v → %v", got, next)
		}
		if next > time.Second {
			t.Fatalf("delay %v exceeds the 1s cap", next)
		}
		got = next
	}
	if got != time.Second {
		t.Fatalf("delay settled at %v, want the 1s cap", got)
	}
}
