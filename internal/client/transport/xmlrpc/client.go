// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package xmlrpc

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmerr"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// DefaultTimeout applies when the caller's ctx has no deadline.
const DefaultTimeout = 30 * time.Second

// Config configures a [Client].
type Config struct {
	// URL is the full endpoint, e.g. "http://ccu:2010/".
	URL string

	// Username / Password enable HTTP Basic Auth if Username is non-empty.
	Username string
	Password string

	// InsecureSkipVerify disables TLS certificate verification. Callers
	// flip it only for self-signed CCU deployments; the daemon warns
	// loudly when set.
	InsecureSkipVerify bool

	// HTTPClient overrides the internal *http.Client. If nil, a client
	// with DefaultTimeout and the InsecureSkipVerify flag is built.
	HTTPClient *http.Client

	// ResponseLimit bounds the response body in bytes. Zero =
	// [DefaultResponseLimit].
	ResponseLimit int64

	// Interface identifies the logical CCU interface the client talks to
	// (HmIP-RF, BidCos-RF, …). Used purely for log/error enrichment.
	Interface string

	// Host is an optional override for the host shown in logs / errors.
	// Defaults to URL when empty.
	Host string

	// Logger receives structured slog events. If nil, [slog.Default] is used.
	Logger *slog.Logger

	// Observer receives per-request lifecycle callbacks. If nil, a
	// no-op observer is used.
	Observer interfaces.TransportObserver
}

// Client is an XML-RPC client for the CCU. Safe for concurrent use.
type Client struct {
	cfg        Config
	httpClient *http.Client
	logger     *slog.Logger
	observer   interfaces.TransportObserver
	host       string
	limit      int64
}

// NewClient constructs a Client. Returns an error only when cfg is invalid.
func NewClient(cfg Config) (*Client, error) {
	if cfg.URL == "" {
		return nil, errors.New("xmlrpc: Config.URL is required")
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{
			Timeout: DefaultTimeout,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					MinVersion:         tls.VersionTLS12,
					InsecureSkipVerify: cfg.InsecureSkipVerify, //nolint:gosec // explicit opt-in for CCU self-signed certs
				},
			},
		}
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	observer := cfg.Observer
	if observer == nil {
		observer = interfaces.NoopObserver{}
	}
	host := cfg.Host
	if host == "" {
		host = cfg.URL
	}
	limit := cfg.ResponseLimit
	if limit <= 0 {
		limit = DefaultResponseLimit
	}
	return &Client{
		cfg:        cfg,
		httpClient: hc,
		logger:     logger,
		observer:   observer,
		host:       host,
		limit:      limit,
	}, nil
}

// Call invokes method with params and returns the decoded response
// value. A CCU fault is returned wrapped as [*hmerr.XMLRPCFault].
func (c *Client) Call(ctx context.Context, method string, params []Value) (Value, error) {
	info := interfaces.RequestInfo{
		Protocol:  "xml-rpc",
		Method:    method,
		Host:      c.host,
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

func (c *Client) doCall(ctx context.Context, method string, params []Value) (Value, error) {
	var body bytes.Buffer
	if err := EncodeCall(&body, &MethodCall{Method: method, Params: params}); err != nil {
		return nil, c.wrap(method, fmt.Errorf("encode call: %w", err))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.URL, bytes.NewReader(body.Bytes()))
	if err != nil {
		return nil, c.wrap(method, fmt.Errorf("build request: %w", err))
	}
	req.Header.Set("Content-Type", "text/xml; charset=ISO-8859-1")
	req.Header.Set("Accept", "text/xml")
	req.Header.Set("Content-Length", strconv.Itoa(body.Len()))
	if c.cfg.Username != "" {
		req.SetBasicAuth(c.cfg.Username, c.cfg.Password)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, c.wrap(method, fmt.Errorf("%w: %w", hmerr.ErrNoConnection, err))
	}
	defer func() { _ = resp.Body.Close() }()

	if err := c.checkStatus(resp); err != nil {
		return nil, c.wrap(method, err)
	}

	limited := io.LimitReader(resp.Body, c.limit+1)
	raw, err := io.ReadAll(limited)
	if err != nil {
		return nil, c.wrap(method, fmt.Errorf("read response: %w", err))
	}
	if int64(len(raw)) > c.limit {
		return nil, c.wrap(method, fmt.Errorf("response exceeds limit of %d bytes: %w", c.limit, hmerr.ErrClientException))
	}

	mr, err := DecodeResponse(bytes.NewReader(raw))
	if err != nil {
		return nil, c.wrap(method, fmt.Errorf("decode response: %w", err))
	}
	if mr.Fault != nil {
		c.logger.Debug(
			"xmlrpc fault",
			slog.String("method", method),
			slog.Int("code", mr.Fault.Code),
			slog.String("message", mr.Fault.Message),
		)
		return nil, c.wrap(method, mr.Fault)
	}
	if len(mr.Params) == 0 {
		return NilValue{}, nil
	}
	return mr.Params[0], nil
}

func (c *Client) checkStatus(resp *http.Response) error {
	code := resp.StatusCode
	switch {
	case code >= 200 && code < 300:
		return nil
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return fmt.Errorf("http %d: %w", code, hmerr.ErrAuthFailure)
	case code >= 500:
		return fmt.Errorf("http %d: %w", code, hmerr.ErrInternalBackendException)
	default:
		return fmt.Errorf("http %d %s: %w", code, strings.ToLower(http.StatusText(code)), hmerr.ErrClientException)
	}
}

func (c *Client) wrap(method string, err error) error {
	return hmerr.WithContext(err, hmerr.Context{
		Protocol:  "xml-rpc",
		Method:    method,
		Host:      c.host,
		Interface: c.cfg.Interface,
	})
}
