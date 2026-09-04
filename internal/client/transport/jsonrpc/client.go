// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

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
	"unicode/utf8"

	"github.com/SukramJ/openccu-loom/internal/httpx"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
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

// loginMaxFailedAttempts mirrors the LOGIN_MAX_FAILED_ATTEMPTS constant in
// the Python reference (client/json_rpc.py _do_login), which applies the
// same backoff from the first failure and, past this count, only raises
// its log to error — it does not stop retrying either. Neither client
// implements a hard "give up" state; [loginMaxBackoff] bounding the retry
// rate to one attempt per 60 s is what actually protects the CCU.
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

// staleLogoutTimeout bounds the best-effort Session.logout the client issues
// for a session it is about to abandon.
const staleLogoutTimeout = 5 * time.Second

// ccuAccessDeniedCode is the JSON-RPC error code the CCU returns (under
// HTTP 200) both for an invalid/expired session and for a privilege
// mismatch, e.g. `access denied ("ADMIN" needed 0)`.
const ccuAccessDeniedCode = 400

// jsonRPCAuthRequiredCode and jsonRPCSessionExpiredCode are the standard
// JSON-RPC 2.0 codes some backends (godevccu, the reference simulator,
// among them) use for "no session presented" / "session no longer valid"
// instead of the CCU's own code 400. Without treating these the same as
// ccuAccessDeniedCode, a backend that signals expiry this way never
// re-logs-in: it keeps sending the dead session id until the client's own
// freshness window elapses, failing every call on it in the meantime.
const (
	jsonRPCAuthRequiredCode   = -32001
	jsonRPCSessionExpiredCode = -32002
)

// isStaleSessionCode reports whether code is one of the JSON-RPC error
// codes that mean "the session on this connection is no longer usable" —
// as opposed to a permanent, non-session rejection. Every code in this
// family is handled identically: re-login once and retry the call.
func isStaleSessionCode(code int) bool {
	switch code {
	case ccuAccessDeniedCode, jsonRPCAuthRequiredCode, jsonRPCSessionExpiredCode:
		return true
	default:
		return false
	}
}

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

	// sessionLoginMu serializes the login/renew decision so a cold-start
	// burst of concurrent callers does not race into multiple parallel
	// Session.login calls — each would open a separate CCU session at once
	// and trip the CCU's "too many sessions" limit. It guards only the
	// establish-a-session critical section; ordinary field access stays on
	// mu. Lock order is always sessionLoginMu → mu, never the reverse.
	sessionLoginMu sync.Mutex

	// supportedMethods caches the result of CheckSupportedMethods. nil
	// means "not yet probed"; non-nil is the set of method names the CCU
	// responded with in system.listMethods.
	supportedMethods map[string]bool

	// Login rate-limiting fields.
	failedLoginAttempts int           // consecutive auth failures
	currentBackoff      time.Duration // backoff applied after the next failure

	// nextLoginAttempt is the earliest time a new Session.login round-trip
	// may run, per the accumulated backoff. Zero means no backoff is
	// pending. Checked without sessionLoginMu held so a caller arriving
	// during an active backoff fails fast instead of queueing behind it.
	nextLoginAttempt time.Time
}

// New constructs a Client. Returns an error only on invalid config.
func New(cfg Config) (*Client, error) {
	if cfg.Endpoint == "" {
		return nil, errors.New("jsonrpc: Endpoint is required")
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = httpx.NewClient(DefaultTimeout)
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
// answered with 401/403 or an auth-coded JSON-RPC error, a fresh Login is
// attempted and the call is retried once.
//
// The session the reply is judged against is the one the request actually
// carried (read back from the merged parameters), never the client's
// current session: several callers share one client, so a concurrent
// caller may already have logged in again between send and reply.
// Treating that fresh session as the failed one would invalidate it and
// log it out on the CCU — killing a live session and sending every
// concurrent caller through its own login.
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
	// Some CCU JSON-RPC methods (e.g. Program.getAll / Sysvar names) return an
	// ISO-8859-1 body despite JSON's UTF-8 requirement; json.Unmarshal would
	// then replace each high byte with U+FFFD ("Sp�le"). Transcode only
	// when the body is not already valid UTF-8, so a proper UTF-8 response is
	// left untouched.
	if !utf8.Valid(raw) {
		raw = hmtypes.Latin1ToUTF8(raw)
	}

	switch resp.StatusCode {
	case http.StatusOK:
		// fall through
	case http.StatusUnauthorized, http.StatusForbidden:
		if allowRetry && c.cfg.Username != "" {
			if loginErr := c.reloginLocked(ctx, sessionOf(merged)); loginErr == nil {
				return c.callOnce(ctx, method, params, out, false)
			}
		} else {
			c.invalidateStaleSession(sessionOf(merged))
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
		// a privilege mismatch; some backends signal the same condition
		// with JSON-RPC codes -32001/-32002 instead. Re-login and retry
		// once for the whole family: an expired session self-heals, a
		// genuine privilege mismatch fails again with the same code on
		// the fresh session and propagates below.
		if isStaleSessionCode(parsed.Error.Code) && allowRetry && c.cfg.Username != "" {
			if loginErr := c.reloginLocked(ctx, sessionOf(merged)); loginErr == nil {
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
// Rate-limiting: every failed login sets a deadline using an exponential
// backoff, starting at [loginBaseBackoff] = 1 s and doubling each
// consecutive failure up to [loginMaxBackoff] = 60 s — from the first
// rejection, not after [loginMaxFailedAttempts] accumulate, or a
// misconfigured or exhausted-session-pool CCU gets hammered with
// back-to-back Session.login attempts before the throttle ever engages.
// A call arriving while that deadline is still in the future returns
// [hmerr.ErrAuthFailure] immediately instead of waiting it out — see the
// comment inside the function for why the wait must never block here.
// The backoff and counter are reset on the first successful login.
func (c *Client) Login(ctx context.Context) error {
	if c.cfg.Username == "" {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	// Enforce backoff from the first failure, not from the tenth — a
	// caller gating this on loginMaxFailedAttempts would let the first
	// ten rejected logins fire back-to-back with no delay at all, which
	// is exactly what keeps an exhausted CCU session pool exhausted
	// (every immediate retry claims the slot a concurrent WebUI session
	// just freed).
	//
	// The wait itself never runs here: Login is reached through
	// loginOrRenew and reloginLocked, both of which hold sessionLoginMu
	// across the call, and every other in-flight Call() blocks on that
	// same lock before its own request. Sleeping here would therefore
	// serialize the whole central behind one caller's backoff instead of
	// just delaying the next actual login attempt. Fail fast instead —
	// the deadline naturally elapses between calls, and whichever caller
	// arrives after it makes the next real attempt.
	c.mu.Lock()
	wait := time.Until(c.nextLoginAttempt)
	c.mu.Unlock()
	if wait > 0 {
		return c.wrap("Session.login", fmt.Errorf("%w: login backoff active, retry in %s", hmerr.ErrAuthFailure, wait.Round(time.Millisecond)))
	}

	// A login that displaces a live session must hand the old slot back: the
	// CCU's session pool is small and shared with its WebUI. The ladder's own
	// callers (loginOrRenew, reloginLocked) have already released and cleared
	// the session they abandoned, so this is a no-op for them — it exists to
	// catch callers that reach for Login directly.
	c.mu.Lock()
	displaced := c.sessionID
	c.mu.Unlock()
	c.logoutStale(ctx, displaced)

	params := map[string]any{
		"username": c.cfg.Username,
		"password": c.cfg.Password,
	}
	var session string
	if err := c.callOnce(ctx, "Session.login", params, &session, false); err != nil {
		// The CCU signals BOTH wrong credentials and an exhausted session
		// pool as a JSON-RPC application error ("invalid credentials or too
		// many sessions"), never as an empty result. Both must engage the
		// backoff: retrying at full speed against a CCU whose pool is full
		// is what keeps it full.
		var jerr *hmerr.JSONRPCError
		if errors.As(err, &jerr) {
			attempt := c.noteLoginFailure()
			c.logger.Warn("jsonrpc login rejected by CCU",
				slog.String("host", c.host),
				slog.Int("code", jerr.Code),
				slog.String("message", jerr.Message),
				slog.Int("attempt", attempt))
			return c.wrap("Session.login", fmt.Errorf("%w: %s", hmerr.ErrAuthFailure, jerr.Message))
		}
		// Transport/server error — not the credentials' fault; leave the
		// auth-failure counter alone so a transient outage is not penalised.
		return err
	}
	if session == "" {
		attempt := c.noteLoginFailure()
		c.logger.Warn("jsonrpc login failed (wrong credentials?)",
			slog.String("host", c.host),
			slog.Int("attempt", attempt))
		return c.wrap("Session.login", hmerr.ErrAuthFailure)
	}

	// Success: store session and reset failure counters. The freshness
	// stamp lets the next Call skip an immediate Session.renew round-trip.
	c.mu.Lock()
	c.sessionID = session
	c.lastSessionRefresh = time.Now()
	c.failedLoginAttempts = 0
	c.currentBackoff = 0
	c.nextLoginAttempt = time.Time{}
	c.mu.Unlock()
	c.logger.Debug("jsonrpc session established", slog.String("host", c.host))
	return nil
}

// noteLoginFailure records one rejected login and advances the exponential
// backoff, returning the new consecutive-failure count. Both counters reset
// on the next successful login.
func (c *Client) noteLoginFailure() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failedLoginAttempts++
	if c.currentBackoff == 0 {
		c.currentBackoff = loginBaseBackoff
	} else {
		c.currentBackoff = min(time.Duration(float64(c.currentBackoff)*loginBackoffMultiplier), loginMaxBackoff)
	}
	c.nextLoginAttempt = time.Now().Add(c.currentBackoff)
	return c.failedLoginAttempts
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
// CCU response: the JSON boolean `true`. Session.renew extends the session
// in place and does NOT mint a new ID — the handler touches the session and
// answers with a literal `true` (CCU firmware
// WebUI/www/api/methods/session/renew.tcl). Decoding the reply as anything
// else turns every successful renewal into a decode error, and the caller
// then abandons a perfectly healthy session and opens a fresh one — one
// leaked CCU session per freshness window, until the CCU's session pool is
// exhausted and no one (including the WebUI) can log in any more.
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
	var renewed bool
	if err := c.callOnce(ctx, "Session.renew", map[string]any{sessionParamKey: session}, &renewed, false); err != nil {
		return err
	}
	if !renewed {
		// CCU rejected the renewal; the session is gone. Force a fresh Login
		// on the next callOnce by invalidating the cached ID.
		c.invalidateSession()
		return c.wrap("Session.renew", hmerr.ErrAuthFailure)
	}
	// Keep the session ID: the CCU renewed the one we sent.
	c.mu.Lock()
	c.lastSessionRefresh = time.Now()
	c.mu.Unlock()
	c.logger.Debug("jsonrpc session renewed", slog.String("host", c.host))
	return nil
}

// logoutStale releases a session the client is about to abandon so the CCU
// frees the slot immediately instead of holding it until the session idles
// out. The CCU's session pool is small, and a login that finds it full fails
// with "invalid credentials or too many sessions" — which locks the operator
// out of the WebUI as well, not just this daemon.
//
// Best-effort by design: a session the CCU has already dropped (reboot, idle
// timeout) makes the logout fail, which is expected and must never block the
// fresh login that follows. The call is detached from ctx so a cancelled
// request still frees the slot.
func (c *Client) logoutStale(ctx context.Context, session string) {
	if session == "" || c.cfg.Username == "" {
		return
	}
	// Drop the cached ID first: paramsWithSession injects the *current*
	// session over any explicit one, which would otherwise send the fresh
	// session ID to a logout meant for the stale one.
	c.invalidateSession()

	logoutCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), staleLogoutTimeout)
	defer cancel()
	if err := c.callOnce(logoutCtx, "Session.logout", map[string]any{sessionParamKey: session}, nil, false); err != nil {
		c.logger.Debug("jsonrpc stale session logout failed",
			slog.String("host", c.host),
			slog.String("err", err.Error()))
		return
	}
	c.logger.Debug("jsonrpc stale session released", slog.String("host", c.host))
}

// EnsureSession guarantees the client holds a usable CCU session, renewing
// the current one or logging in when there is none. It is the session ladder
// every caller should reach for; [Client.Login] unconditionally opens a NEW
// session and is only correct when the old one is known to be unusable.
//
// Callers outside the transport need this when they authenticate against a
// CCU endpoint by session ID instead of going through [Client.Call] — the
// backup and firmware downloads (cp_security.cgi) do exactly that.
func (c *Client) EnsureSession(ctx context.Context) error {
	return c.loginOrRenew(ctx)
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
	// Fast path: a valid, recently refreshed session needs neither a
	// network round-trip nor the login lock.
	if c.hasFreshSession() {
		return nil
	}
	// Serialize so a burst of concurrent callers at cold start does not
	// race into multiple parallel Session.login calls, which would create
	// several CCU sessions at once and trip the "too many sessions" limit.
	c.sessionLoginMu.Lock()
	defer c.sessionLoginMu.Unlock()
	// Re-check under the lock: another goroutine may have logged in or
	// renewed the session while we were waiting for it.
	if c.hasFreshSession() {
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
		// auth plane or surfaces the real connectivity problem. Release the
		// old slot first so a session the CCU still holds does not linger
		// until its idle timeout.
		c.logoutStale(ctx, session)
		return c.Login(ctx)
	}
	return nil
}

// hasFreshSession reports whether a non-empty session was refreshed
// within the [jsonSessionAge] freshness window — the condition under
// which neither a login nor a renew round-trip is needed.
func (c *Client) hasFreshSession() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sessionID != "" &&
		!c.lastSessionRefresh.IsZero() &&
		time.Since(c.lastSessionRefresh) < jsonSessionAge
}

// reloginLocked performs a fresh login serialized against concurrent
// callers. staleSession is the session ID the caller saw fail; if another
// goroutine has already established a newer session under the lock, this
// is a no-op so a burst of simultaneous auth failures triggers a single
// Session.login rather than one per caller.
func (c *Client) reloginLocked(ctx context.Context, staleSession string) error {
	c.sessionLoginMu.Lock()
	defer c.sessionLoginMu.Unlock()
	c.mu.Lock()
	current := c.sessionID
	c.mu.Unlock()
	if current != "" && current != staleSession {
		// Another goroutine already replaced the stale session; reuse it.
		return nil
	}
	// Hand the slot back before taking a new one. The session is usually
	// already dead here (that is why the call failed), but a 400 raised by a
	// privilege mismatch leaves it very much alive — and abandoning it would
	// burn a slot for nothing.
	c.logoutStale(ctx, staleSession)
	return c.Login(ctx)
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

// invalidateStaleSession drops the cached session only while it is still
// the one that failed. A session another caller established in the
// meantime is left alone — it has not been shown to be unusable, and
// dropping it would send every subsequent call through a needless login.
func (c *Client) invalidateStaleSession(stale string) {
	if stale == "" {
		return
	}
	c.mu.Lock()
	if c.sessionID == stale {
		c.sessionID = ""
	}
	c.mu.Unlock()
}

// sessionOf returns the session ID a request carried, or "" when it was
// sent unauthenticated.
func sessionOf(params map[string]any) string {
	s, _ := params[sessionParamKey].(string)
	return s
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
