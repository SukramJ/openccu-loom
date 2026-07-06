// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package jsonrpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmerr"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// DefaultMaxConcurrent caps in-flight requests on a single JSON-RPC client.
// The CCU's embedded webserver becomes unstable under higher concurrency;
// 3 concurrent sessions is the safe operational limit observed in practice.
const DefaultMaxConcurrent = 3

// DefaultTimeout is the per-request timeout applied when the caller
// supplies a ctx without a deadline.
const DefaultTimeout = 30 * time.Second

// sessionParamKey is the parameter name every authenticated CCU
// JSON-RPC call must carry.
const sessionParamKey = "_session_id_"

// Config configures a [Client].
type Config struct {
	// Endpoint is the full URL of the JSON-RPC handler,
	// e.g. "https://ccu/api/homematic.cgi".
	Endpoint string

	// Username / Password for CCU WebUI session login. Optional — if
	// empty, [Client.Login] is a no-op and calls are sent unauthenticated
	Username string
	Password string

	// HTTPClient is the transport. If nil, a sensible default is used.
	HTTPClient *http.Client

	// MaxConcurrent bounds in-flight requests. If zero, [DefaultMaxConcurrent].
	MaxConcurrent int

	// Logger receives structured slog events. If nil, [slog.Default] is used.
	Logger *slog.Logger

	// Observer receives per-request lifecycle callbacks. If nil, a
	// [interfaces.NoopObserver] is used.
	Observer interfaces.TransportObserver

	// Host identifies the remote in error [hmerr.Context] and logs.
	// Defaults to Endpoint when empty.
	Host string

	// ResponseLimit bounds the response body in bytes. Zero selects
	// [DefaultResponseLimit]. Operators with very large installations whose
	// bulk fetch (Interface.getAllDeviceData / Device.listAllDetail) exceeds
	// the default may raise this.
	ResponseLimit int64
}

// DefaultResponseLimit bounds how many bytes we accept from a CCU JSON-RPC
// response before rejecting it, guarding against an oversized/hostile body
// exhausting daemon memory. Set far above the xmlrpc transport's 10 MiB
// because the JSON-RPC-only bulk calls (Interface.getAllDeviceData and
// Device.listAllDetail) return every current value for every device in one
// response: on a large installation (thousands of devices) that legitimately
// runs to tens of MiB, and rejecting it would break the cold-boot value-cache
// warm-up. 128 MiB comfortably covers any real CCU — which is itself
// memory-constrained and cannot emit a multi-hundred-MiB body — while still
// bounding the multi-gigabyte OOM a spoofed/hostile endpoint could stream.
// Operators with an exceptionally large fleet can raise Config.ResponseLimit.
const DefaultResponseLimit = 128 * 1024 * 1024

// loginBackoffMultiplier is the exponential factor applied after each failed
// login attempt.
const loginBackoffMultiplier = 2.0

// loginMaxFailedAttempts is the number of consecutive authentication
// failures after which the client stops retrying login automatically.
// Mirrors the LOGIN_MAX_FAILED_ATTEMPTS constant in the Python reference.
const loginMaxFailedAttempts = 10

// loginBaseBackoff is the initial backoff duration applied after the
// first failed login. Doubles on each subsequent failure up to
// loginMaxBackoff. Mirrors LOGIN_INITIAL_BACKOFF_SECONDS.
const loginBaseBackoff = 1 * time.Second

// loginMaxBackoff is the upper bound for the exponential backoff applied
// between failed login attempts. Mirrors LOGIN_MAX_BACKOFF_SECONDS.
const loginMaxBackoff = 60 * time.Second

// jsonSessionAge is the freshness window for session renewal. If the session
// was renewed within this many seconds, Renew skips the HTTP round-trip.
const jsonSessionAge = 90 * time.Second

// ccuAccessDeniedCode is the JSON-RPC error code the CCU returns (under
// HTTP 200) both for an invalid/expired session and for a privilege
// mismatch, e.g. `access denied ("ADMIN" needed 0)`.
const ccuAccessDeniedCode = 400

// Client is a CCU JSON-RPC client. Safe for concurrent use.
type Client struct {
	cfg        Config
	httpClient *http.Client
	sem        chan struct{}
	logger     *slog.Logger
	observer   interfaces.TransportObserver
	host       string
	limit      int64 // max response body bytes; from cfg.ResponseLimit or default

	mu                 sync.Mutex
	sessionID          string    // empty when logged out
	lastSessionRefresh time.Time // zero when never renewed

	// supportedMethods caches the result of CheckSupportedMethods. nil
	// means "not yet probed"; non-nil is the set of method names the CCU
	// responded with in system.listMethods.
	supportedMethods map[string]bool

	// Login rate-limiting fields.
	failedLoginAttempts int           // consecutive auth failures
	currentBackoff      time.Duration // next sleep before retrying login
}

// New constructs a Client. Returns an error only on invalid config.
func New(cfg Config) (*Client, error) {
	if cfg.Endpoint == "" {
		return nil, errors.New("jsonrpc: Endpoint is required")
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: DefaultTimeout}
	}
	maxConc := cfg.MaxConcurrent
	if maxConc <= 0 {
		maxConc = DefaultMaxConcurrent
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
		host = cfg.Endpoint
	}
	limit := cfg.ResponseLimit
	if limit <= 0 {
		limit = DefaultResponseLimit
	}
	return &Client{
		cfg:        cfg,
		httpClient: hc,
		sem:        make(chan struct{}, maxConc),
		logger:     logger,
		observer:   observer,
		host:       host,
		limit:      limit,
	}, nil
}

// envelope is the on-the-wire request shape. We include the jsonrpc
// Version field because both 1"
// a real CCU is also documented to accept it. The ID field is always
// Set to 0 so the CCU treats every request as
// a call (not a notification) and returns a response body.
type envelope struct {
	JSONRPC string         `json:"jsonrpc"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params,omitempty"`
	ID      int            `json:"id"`
}

// response is the on-the-wire reply shape.
type response struct {
	Result  json.RawMessage `json:"result"`
	Error   *wireError      `json:"error,omitempty"`
	Version string          `json:"version,omitempty"`
}

type wireError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    string `json:"data,omitempty"`
}

// Call invokes method with params and decodes the JSON result into out
// (if non-nil). Session injection, session upkeep, auth retry, and error
// mapping run here.
//
// Session upkeep is two-layered. Proactively, every Call runs
// [Client.loginOrRenew] so the session never idles into the CCU's
// inactivity timeout. Reactively, an expired session that still slips
// through (CCU reboot, ReGa restart) is detected on the reply — the CCU
// signals it as HTTP 200 + JSON-RPC error 400 "access denied", not as an
// HTTP auth status — and the client transparently re-logs in and retries
// the call once. A 400 that persists after a fresh login is a genuine
// privilege mismatch and is surfaced as [hmerr.ErrPermissionDenied].
func (c *Client) Call(ctx context.Context, method string, params map[string]any, out any) error {
	if err := c.acquire(ctx); err != nil {
		return err
	}
	defer c.release()

	if err := c.loginOrRenew(ctx); err != nil {
		return err
	}

	info := interfaces.RequestInfo{
		Protocol: "json-rpc",
		Method:   method,
		Host:     c.host,
	}
	span := c.observer.OnRequestStart(ctx, info)
	start := time.Now()

	err := c.callOnce(ctx, method, params, out, true /*allowRetry*/)

	c.observer.OnRequestEnd(span, interfaces.RequestResult{
		Duration: time.Since(start),
		Err:      err,
	})
	return err
}

// callOnce performs one round trip. If allowRetry is true and the CCU
// answered with 401/403 or an auth-coded JSON-RPC error, the session is
// invalidated, a fresh Login is attempted, and the call is retried once.
func (c *Client) callOnce(ctx context.Context, method string, params map[string]any, out any, allowRetry bool) error {
	merged := c.paramsWithSession(params)

	body, err := json.Marshal(envelope{JSONRPC: "1.1", Method: method, Params: merged})
	if err != nil {
		return c.wrap(method, fmt.Errorf("marshal request: %w", err))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.Endpoint, bytes.NewReader(body))
	if err != nil {
		return c.wrap(method, fmt.Errorf("build request: %w", err))
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return c.wrap(method, fmt.Errorf("%w: %w", hmerr.ErrNoConnection, err))
	}
	defer func() { _ = resp.Body.Close() }()

	// Bound the response read so a spoofed/malfunctioning CCU (reached over
	// plaintext HTTP, or TLS with verification disabled) cannot stream a
	// multi-gigabyte body into memory and OOM the daemon — the client Timeout
	// bounds total time, not bytes. Mirrors the xmlrpc client's LimitReader.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, c.limit+1))
	if err != nil {
		return c.wrap(method, fmt.Errorf("read response: %w", err))
	}
	if int64(len(raw)) > c.limit {
		return c.wrap(method, fmt.Errorf("response exceeds limit of %d bytes: %w", c.limit, hmerr.ErrClientException))
	}

	switch resp.StatusCode {
	case http.StatusOK:
		// fall through
	case http.StatusUnauthorized, http.StatusForbidden:
		c.invalidateSession()
		if allowRetry && c.cfg.Username != "" {
			if loginErr := c.Login(ctx); loginErr == nil {
				return c.callOnce(ctx, method, params, out, false)
			}
		}
		return c.wrap(method, hmerr.ErrAuthFailure)
	default:
		if resp.StatusCode >= 500 {
			return c.wrap(method, fmt.Errorf("http %d: %w", resp.StatusCode, hmerr.ErrInternalBackendException))
		}
		return c.wrap(method, fmt.Errorf("http %d: %w", resp.StatusCode, hmerr.ErrClientException))
	}

	var parsed response
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &parsed); err != nil {
			return c.wrap(method, fmt.Errorf("decode response: %w", err))
		}
	}

	if parsed.Error != nil {
		// The CCU reports an invalid/expired session as HTTP 200 + error
		// code 400 ("access denied") — indistinguishable on the wire from
		// a privilege mismatch. Re-login and retry once: an expired
		// session self-heals, a genuine privilege mismatch fails again
		// with 400 on the fresh session and propagates below.
		if parsed.Error.Code == ccuAccessDeniedCode && allowRetry && c.cfg.Username != "" {
			c.invalidateSession()
			if loginErr := c.Login(ctx); loginErr == nil {
				return c.callOnce(ctx, method, params, out, false)
			}
		}
		return c.wrap(method, &hmerr.JSONRPCError{
			Code:    parsed.Error.Code,
			Message: parsed.Error.Message,
			Data:    parsed.Error.Data,
		})
	}

	if out != nil && len(parsed.Result) > 0 && !bytes.Equal(parsed.Result, []byte("null")) {
		if err := json.Unmarshal(parsed.Result, out); err != nil {
			return c.wrap(method, fmt.Errorf("decode result: %w", err))
		}
	}
	return nil
}

// Login obtains a session ID via Session.login and stores it. Cheap to call
// repeatedly; callers usually do so only after 401/403.
//
// Rate-limiting: consecutive authentication failures trigger an exponential
// backoff starting at [loginBaseBackoff] = 1 s, doubling each time up to
// [loginMaxBackoff] = 60 s, across up to [loginMaxFailedAttempts] = 10
// failures. This prevents hammering a misconfigured CCU. The backoff and
// counter are reset on the first successful login.
func (c *Client) Login(ctx context.Context) error {
	if c.cfg.Username == "" {
		return nil
	}

	// Enforce backoff when too many consecutive failures have occurred.
	c.mu.Lock()
	failCount := c.failedLoginAttempts
	backoff := c.currentBackoff
	c.mu.Unlock()

	if failCount >= loginMaxFailedAttempts && backoff > 0 {
		// Apply the accumulated backoff before trying again.
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}

	params := map[string]any{
		"username": c.cfg.Username,
		"password": c.cfg.Password,
	}
	var session string
	if err := c.callOnce(ctx, "Session.login", params, &session, false); err != nil {
		// Not an auth failure per se — network/server error; reset the
		// auth-failure counter so we don't penalise a transient outage.
		return err
	}
	if session == "" {
		// Auth failure: bump the counter and double the backoff, capped at loginMaxBackoff.
		c.mu.Lock()
		c.failedLoginAttempts++
		if c.currentBackoff == 0 {
			c.currentBackoff = loginBaseBackoff
		} else {
			next := min(time.Duration(float64(c.currentBackoff)*loginBackoffMultiplier), loginMaxBackoff)
			c.currentBackoff = next
		}
		c.mu.Unlock()
		c.logger.Warn("jsonrpc login failed (wrong credentials?)",
			slog.String("host", c.host),
			slog.Int("attempt", c.failedLoginAttempts))
		return c.wrap("Session.login", hmerr.ErrAuthFailure)
	}

	// Success: store session and reset failure counters. The freshness
	// stamp lets the next Call skip an immediate Session.renew round-trip.
	c.mu.Lock()
	c.sessionID = session
	c.lastSessionRefresh = time.Now()
	c.failedLoginAttempts = 0
	c.currentBackoff = 0
	c.mu.Unlock()
	c.logger.Debug("jsonrpc session established", slog.String("host", c.host))
	return nil
}

// Logout releases the session on the CCU and clears local state.
// Safe to call when no session is active.
func (c *Client) Logout(ctx context.Context) error {
	c.mu.Lock()
	session := c.sessionID
	c.sessionID = ""
	c.mu.Unlock()
	if session == "" {
		return nil
	}
	return c.callOnce(ctx, "Session.logout", map[string]any{sessionParamKey: session}, nil, false)
}

// Renew extends the lifetime of the active session without obtaining a new
// one. When no session is active, Renew is a no-op (returns nil) — operators
// can call it unconditionally as part of a keepalive job.
//
// Freshness guard: if the session was renewed within [jsonSessionAge] (90 s),
// the HTTP round-trip is skipped and the call returns nil immediately.
//
// CCU response: a non-empty session ID confirming the renewal. The returned
// ID may match the current one (CCU reuses) or differ (CCU rotated) — both
// cases are folded into the local cache.
func (c *Client) Renew(ctx context.Context) error {
	c.mu.Lock()
	session := c.sessionID
	recentlyRefreshed := !c.lastSessionRefresh.IsZero() && time.Since(c.lastSessionRefresh) < jsonSessionAge
	c.mu.Unlock()
	if session == "" {
		return nil
	}
	// Skip the HTTP round-trip when the session was renewed recently.
	if recentlyRefreshed {
		return nil
	}
	var renewed string
	if err := c.callOnce(ctx, "Session.renew", map[string]any{sessionParamKey: session}, &renewed, false); err != nil {
		return err
	}
	if renewed == "" {
		// CCU rejected the renewal; force a fresh Login on the next
		// callOnce by invalidating the cached ID.
		c.invalidateSession()
		return c.wrap("Session.renew", hmerr.ErrAuthFailure)
	}
	c.mu.Lock()
	c.sessionID = renewed
	c.lastSessionRefresh = time.Now()
	c.mu.Unlock()
	c.logger.Debug("jsonrpc session renewed", slog.String("host", c.host))
	return nil
}

// loginOrRenew keeps the CCU session usable ahead of a call: with no
// cached session it logs in; with one it renews (subject to the
// [jsonSessionAge] freshness guard) and falls back to a fresh login when
// the CCU rejects the renewal. Mirrors the Python reference client's
// login-or-renew ladder that runs before every request. No-op in
// unauthenticated mode.
func (c *Client) loginOrRenew(ctx context.Context) error {
	if c.cfg.Username == "" {
		return nil
	}
	c.mu.Lock()
	session := c.sessionID
	c.mu.Unlock()
	if session == "" {
		return c.Login(ctx)
	}
	if err := c.Renew(ctx); err != nil {
		// The CCU dropped the session (reboot, inactivity timeout) or the
		// renewal round-trip failed — a fresh login either recovers the
		// auth plane or surfaces the real connectivity problem.
		return c.Login(ctx)
	}
	return nil
}

// SessionID returns the currently cached session ID (empty when logged out).
// Intended for tests and diagnostics.
func (c *Client) SessionID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sessionID
}

// IsActivated reports whether the client currently holds an active CCU
// JSON-RPC session (i.e. a non-empty session ID).
func (c *Client) IsActivated() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sessionID != ""
}

func (c *Client) paramsWithSession(params map[string]any) map[string]any {
	c.mu.Lock()
	session := c.sessionID
	c.mu.Unlock()
	if session == "" {
		return params
	}
	out := make(map[string]any, len(params)+1)
	maps.Copy(out, params)
	out[sessionParamKey] = session
	return out
}

func (c *Client) invalidateSession() {
	c.mu.Lock()
	c.sessionID = ""
	c.mu.Unlock()
}

// CheckSupportedMethods probes the CCU for all available JSON-RPC method
// names via system.listMethods, caches the result, and returns the set.
// Subsequent calls return the cached result without an HTTP round-trip.
//
// The caller (coordinator or backend) can pass the returned map to
// [IsMethodSupported] before invoking niche methods.
//
// When system.listMethods itself is not supported or returns an error, the
// method set is recorded as empty and an error is returned; callers should
// treat an empty set as "unknown" and attempt the actual calls
// optimistically.
func (c *Client) CheckSupportedMethods(ctx context.Context) (map[string]bool, error) {
	c.mu.Lock()
	if c.supportedMethods != nil {
		m := c.supportedMethods
		c.mu.Unlock()
		return m, nil
	}
	c.mu.Unlock()

	// system.listMethods returns either:
	// - a JSON array of objects with a "name" field (real CCU), or
	// - a JSON array of plain strings.
	// We decode into []any and handle both shapes.
	var rawResult []any
	err := c.Call(ctx, "system.listMethods", nil, &rawResult)
	supported := make(map[string]bool)
	if err == nil {
		for _, entry := range rawResult {
			switch v := entry.(type) {
			case string:
				if v != "" {
					supported[v] = true
				}
			case map[string]any:
				if name, ok := v["name"].(string); ok && name != "" {
					supported[name] = true
				}
			}
		}
	}

	c.mu.Lock()
	c.supportedMethods = supported
	c.mu.Unlock()

	if err != nil {
		return supported, fmt.Errorf("jsonrpc.CheckSupportedMethods: %w", err)
	}
	return supported, nil
}

// IsMethodSupported reports whether method is present in the cached
// supported-methods set. Returns true when [CheckSupportedMethods] has
// not yet been called (optimistic: unknown = assume supported), and true
// when the method was found. Returns false only when the set is populated
// and the method is absent.
func (c *Client) IsMethodSupported(method string) bool {
	c.mu.Lock()
	m := c.supportedMethods
	c.mu.Unlock()
	if len(m) == 0 {
		return true // not yet probed — optimistic
	}
	return m[method]
}

func (c *Client) acquire(ctx context.Context) error {
	select {
	case c.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Client) release() { <-c.sem }

func (c *Client) wrap(method string, err error) error {
	return hmerr.WithContext(err, hmerr.Context{
		Protocol: "json-rpc",
		Method:   method,
		Host:     c.host,
	})
}
