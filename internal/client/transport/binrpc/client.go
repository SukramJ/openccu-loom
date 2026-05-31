// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package binrpc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"time"

	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// DefaultDialTimeout caps TCP handshake time when the caller's ctx has
// no deadline.
const DefaultDialTimeout = 5 * time.Second

// DefaultIOTimeout caps read/write time for a single request when the
// caller's ctx has no deadline.
const DefaultIOTimeout = 15 * time.Second

// Config configures a [Client].
type Config struct {
	// Addr is the CUxD endpoint ("host:port"). SPECIFICATION §7.2 fixes
	// the default at host:8701.
	Addr string

	// Interface identifies the logical CCU interface (CUxD). Used for
	// log and error enrichment.
	Interface string

	// Dialer overrides the TCP dialer. If nil, a default is built.
	Dialer *net.Dialer

	// DialTimeout bounds the TCP handshake. Zero = [DefaultDialTimeout].
	DialTimeout time.Duration

	// IOTimeout bounds read/write on the connection. Zero =
	// [DefaultIOTimeout].
	IOTimeout time.Duration

	// Logger receives structured slog events. If nil, [slog.Default].
	Logger *slog.Logger

	// Observer receives per-request lifecycle callbacks. If nil, a
	// no-op observer is used.
	Observer interfaces.TransportObserver
}

// Client is a BIN-RPC client for CUxD. Each Call opens a fresh TCP
// connection — CUxD closes after one request/response cycle. Safe for
// concurrent use from multiple goroutines.
type Client struct {
	cfg      Config
	dialer   *net.Dialer
	logger   *slog.Logger
	observer interfaces.TransportObserver
	ioOut    time.Duration
}

// NewClient constructs a Client.
func NewClient(cfg Config) (*Client, error) {
	if cfg.Addr == "" {
		return nil, errors.New("binrpc: Config.Addr is required")
	}
	dialer := cfg.Dialer
	if dialer == nil {
		dialTO := cfg.DialTimeout
		if dialTO <= 0 {
			dialTO = DefaultDialTimeout
		}
		dialer = &net.Dialer{Timeout: dialTO}
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	observer := cfg.Observer
	if observer == nil {
		observer = interfaces.NoopObserver{}
	}
	ioTO := cfg.IOTimeout
	if ioTO <= 0 {
		ioTO = DefaultIOTimeout
	}
	return &Client{
		cfg:      cfg,
		dialer:   dialer,
		logger:   logger,
		observer: observer,
		ioOut:    ioTO,
	}, nil
}

// Call invokes method with params on the remote and returns the decoded
// result. A BIN-RPC fault is returned wrapped as [*hmerr.XMLRPCFault].
func (c *Client) Call(ctx context.Context, method string, params []xmlrpc.Value) (xmlrpc.Value, error) {
	info := interfaces.RequestInfo{
		Protocol:  "bin-rpc",
		Method:    method,
		Host:      c.cfg.Addr,
		Interface: c.cfg.Interface,
	}
	span := c.observer.OnRequestStart(ctx, info)
	start := time.Now()

	v, err := c.doCall(ctx, method, params)

	c.observer.OnRequestEnd(span, interfaces.RequestResult{
		Duration: time.Since(start),
		Err:      err,
	})
	return v, err
}

func (c *Client) doCall(ctx context.Context, method string, params []xmlrpc.Value) (xmlrpc.Value, error) {
	// Encode request up front so a marshalling error never wastes a dial.
	var frame bytes.Buffer
	if err := WriteRequest(&frame, method, params); err != nil {
		return nil, c.wrap(method, fmt.Errorf("encode request: %w", err))
	}

	conn, err := c.dialer.DialContext(ctx, "tcp", c.cfg.Addr)
	if err != nil {
		return nil, c.wrap(method, fmt.Errorf("%w: %w", hmerr.ErrNoConnection, err))
	}
	defer func() { _ = conn.Close() }()

	// Propagate a ctx deadline onto the connection. If the caller
	// passed no deadline, fall back to IOTimeout.
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(c.ioOut)
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return nil, c.wrap(method, fmt.Errorf("set deadline: %w", err))
	}

	// Cancel the connection on ctx.Done so we don't block waiting on I/O
	// after the caller has given up.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-done:
		}
	}()

	if _, err := io.Copy(conn, &frame); err != nil {
		return nil, c.wrap(method, fmt.Errorf("write request: %w", err))
	}

	resp, err := ReadResponse(io.LimitReader(conn, MaxMessageSize+8))
	if err != nil {
		if ctx.Err() != nil {
			return nil, c.wrap(method, fmt.Errorf("%w: %w", hmerr.ErrNoConnection, ctx.Err()))
		}
		return nil, c.wrap(method, fmt.Errorf("read response: %w", err))
	}
	if resp.Fault != nil {
		c.logger.Debug(
			"binrpc fault",
			slog.String("method", method),
			slog.Int("code", resp.Fault.Code),
			slog.String("message", resp.Fault.Message),
		)
		return nil, c.wrap(method, resp.Fault)
	}
	if resp.Value == nil {
		return xmlrpc.NilValue{}, nil
	}
	return resp.Value, nil
}

func (c *Client) wrap(method string, err error) error {
	return hmerr.WithContext(err, hmerr.Context{
		Protocol:  "bin-rpc",
		Method:    method,
		Host:      c.cfg.Addr,
		Interface: c.cfg.Interface,
	})
}
