// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package remoteproxy

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ingressPathHeader is the browser-facing base path header injected by
// the HA Supervisor on every Ingress-proxied request. The proxy forwards
// it upstream with the instance prefix appended so the remote daemon
// keeps generating correct ingress-aware redirects, and reuses the
// forwarded value when it has to rewrite responses itself.
const ingressPathHeader = "X-Ingress-Path"

// ingressBase returns the sanitized browser-facing base path from the
// Supervisor's header. Only a plain absolute path ("/api/hassio_ingress/…")
// qualifies; anything else — protocol-relative "//host", query/fragment
// characters, backslashes, control bytes (browsers strip TAB/CR/LF, which
// would turn "/<TAB>/host" back into "//host") — is treated as absent, so
// a spoofed header can never steer a redirect off-origin.
func ingressBase(r *http.Request) string {
	p := r.Header.Get(ingressPathHeader)
	// "//host" and "/\host" are both treated as protocol-relative URLs
	// by browsers; reject each explicitly (the byte loop below catches
	// backslashes anywhere, this makes the guard analyzable).
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.HasPrefix(p, "/\\") {
		return ""
	}
	for i := range len(p) {
		if c := p[i]; c <= ' ' || c == 0x7f || c == '\\' || c == '?' || c == '#' {
			return ""
		}
	}
	return strings.TrimSuffix(p, "/")
}

// instanceProxy is the reverse proxy for one remote instance.
type instanceProxy struct {
	inst   Instance
	target *url.URL
	// prefix is the browser-facing mount point relative to the ingress
	// base: "/i/<name>" in multi-instance mode, "" in single-instance
	// (fully transparent) mode.
	prefix string
	rp     *httputil.ReverseProxy
	// client shares the proxy transport (connection pool, TLS mode) for
	// the poller's direct status probes.
	client    *http.Client
	transport *upstreamTransport
	log       *slog.Logger

	// normURL is the key a paired token is bound to.
	normURL string
	store   *pairStore
	// pairOps serialises starting and cancelling a pairing.
	pairOps sync.Mutex

	mu sync.RWMutex
	// paired is the box token a pairing left behind; zero when unpaired.
	paired pairedCredential
	// session is the latest pairing attempt, pending or finished.
	session *pairSession
}

// Credential sources of an instance, as the overview and the pairing
// routes report them.
const (
	authSourceNone    = ""
	authSourceOptions = "options"
	authSourcePaired  = "paired"
)

// authToken is the Bearer credential the proxy injects upstream. A
// non-empty token from the add-on options always wins over a paired one:
// the operator wrote it deliberately, typically a Full-access token for
// admin rights a pairing never grants.
func (p *instanceProxy) authToken() string {
	if p.inst.Token != "" {
		return p.inst.Token
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.paired.Token
}

// authSource names where authToken comes from.
func (p *instanceProxy) authSource() string {
	if p.inst.Token != "" {
		return authSourceOptions
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.paired.Token != "" {
		return authSourcePaired
	}
	return authSourceNone
}

// setPaired stores the credential a pairing yielded, then makes it the
// instance's credential and pins its certificate.
func (p *instanceProxy) setPaired(cred pairedCredential) error {
	if err := p.store.put(p.inst.Name, cred); err != nil {
		return err
	}
	p.mu.Lock()
	p.paired = cred
	p.mu.Unlock()
	p.applyPin()
	return nil
}

// forgetPaired drops the paired credential from memory and from the state
// file, and lifts its certificate pin.
func (p *instanceProxy) forgetPaired(reason string) {
	if err := p.store.drop(p.inst.Name); err != nil {
		p.log.Warn("forgetting the paired token on disk failed", "error", err)
	}
	p.mu.Lock()
	had := p.paired.Token != ""
	p.paired = pairedCredential{}
	p.mu.Unlock()
	p.applyPin()
	if had {
		p.log.Info("paired token dropped", "reason", reason)
	}
}

// applyPin pins the upstream transport to the paired certificate while
// the paired token is the credential in use, and lifts the pin otherwise.
func (p *instanceProxy) applyPin() {
	var pin []byte
	if p.inst.Token == "" {
		p.mu.RLock()
		fp := p.paired.Fingerprint
		p.mu.RUnlock()
		if b, err := hex.DecodeString(fp); err == nil && len(b) > 0 {
			pin = b
		}
	}
	p.transport.setPin(pin)
}

// upstreamTransport routes every upstream call (proxied requests and
// status probes) through the current http.Transport. A pairing swaps the
// transport when the certificate pin changes; connections of the old one
// are closed when idle.
type upstreamTransport struct {
	https    bool
	insecure bool
	cur      atomic.Pointer[http.Transport]
}

func newUpstreamTransport(target *url.URL, insecure bool) *upstreamTransport {
	t := &upstreamTransport{https: target.Scheme == "https", insecure: insecure}
	t.cur.Store(t.build(nil))
	return t
}

// RoundTrip implements http.RoundTripper.
func (t *upstreamTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return t.cur.Load().RoundTrip(r)
}

// CloseIdleConnections lets http.Client.CloseIdleConnections reach the
// current transport.
func (t *upstreamTransport) CloseIdleConnections() {
	t.cur.Load().CloseIdleConnections()
}

// setPin replaces the transport with one that accepts exactly the pinned
// certificate (nil: the operator's verification mode).
func (t *upstreamTransport) setPin(pin []byte) {
	if old := t.cur.Swap(t.build(pin)); old != nil {
		old.CloseIdleConnections()
	}
}

func (t *upstreamTransport) build(pin []byte) *http.Transport {
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout: 10 * time.Second,
		// Bound the wait for response HEADERS only: a remote that accepts
		// the TCP connection but never answers (half-dead VPN, wedged
		// daemon) must fail into the 502 page instead of hanging the
		// panel forever. Long-lived bodies (WebSocket, downloads, SSE)
		// are unaffected — the timer stops once headers arrive.
		ResponseHeaderTimeout: 60 * time.Second,
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConnsPerHost:   8,
		// HTTP/1.1 only: the SPA's WebSocket rides a Connection: Upgrade
		// handshake, which the client transport does not speak over h2.
		ForceAttemptHTTP2: false,
	}
	if !t.https {
		return transport
	}
	transport.TLSClientConfig = &tls.Config{
		MinVersion: tls.VersionTLS12,
		//nolint:gosec // G402: explicit per-instance operator opt-in for self-signed LAN certificates (ADR 0054).
		InsecureSkipVerify: t.insecure,
	}
	if pin != nil {
		// A paired box: the pin replaces chain verification, accepting
		// exactly the certificate the pairing code was bound to.
		transport.TLSClientConfig.VerifyConnection = pinVerifier(pin)
		transport.TLSClientConfig.InsecureSkipVerify = true
	}
	return transport
}

// newInstanceProxy wires the ReverseProxy for one instance. The URL has
// been validated by LoadOptions; a parse failure here is programmer
// error, hence the error return instead of a panic to keep the caller's
// startup path uniform.
func newInstanceProxy(inst Instance, prefix string, store *pairStore, log *slog.Logger) (*instanceProxy, error) {
	target, err := parseInstanceURL(inst.URL)
	if err != nil {
		return nil, fmt.Errorf("instance %q: %w", inst.Name, err)
	}
	if store == nil {
		store = &pairStore{log: log, entries: map[string]pairedCredential{}}
	}
	p := &instanceProxy{
		inst:      inst,
		target:    target,
		prefix:    prefix,
		log:       log.With("instance", inst.Name),
		normURL:   normaliseInstanceURL(inst.URL),
		store:     store,
		transport: newUpstreamTransport(target, inst.TLSInsecure),
	}
	if cred, ok := store.get(inst.Name); ok {
		p.paired = cred
	}
	p.applyPin()
	p.rp = &httputil.ReverseProxy{
		Rewrite:        p.rewrite,
		ModifyResponse: p.modifyResponse,
		ErrorHandler:   p.errorHandler,
		Transport:      p.transport,
		// Flush every write immediately so streamed responses (SSE,
		// long polls) traverse both proxy hops without buffering delay.
		FlushInterval: -1,
		ErrorLog:      slog.NewLogLogger(p.log.Handler(), slog.LevelWarn),
	}
	p.client = &http.Client{Transport: p.transport}
	return p, nil
}

// rewrite shapes the outbound request. The incoming request has already
// had the instance prefix stripped (http.StripPrefix in the router), so
// only the upstream base, forwarding headers, and the optional Bearer
// injection remain.
func (p *instanceProxy) rewrite(pr *httputil.ProxyRequest) {
	// Capture the forwarding headers a trusted upstream proxy (e.g. Traefik)
	// already set, before SetXForwarded overwrites them with this hop's own
	// view. The daemon's WebSocket same-origin check compares the browser Origin
	// against X-Forwarded-Host; across a double proxy that must remain the
	// browser-facing host, not this hop's internal upstream host, or the SPA's
	// live WebSocket 403s in a reconnect loop.
	priorHost := pr.In.Header.Get("X-Forwarded-Host")
	priorProto := pr.In.Header.Get("X-Forwarded-Proto")
	pr.SetURL(p.target)
	pr.SetXForwarded()
	if priorHost != "" {
		pr.Out.Header.Set("X-Forwarded-Host", priorHost)
	}
	if priorProto != "" {
		pr.Out.Header.Set("X-Forwarded-Proto", priorProto)
	}
	// Forward the browser-facing base with the instance prefix appended.
	// Without an Ingress hop (direct access, tests) the prefix alone is
	// still the correct base for multi-instance mounts. The inbound value
	// is sanitized so upstream never sees a spoofable redirect base.
	if base := ingressBase(pr.In) + p.prefix; base != "" {
		pr.Out.Header.Set(ingressPathHeader, base)
	} else {
		pr.Out.Header.Del(ingressPathHeader)
	}
	if token := p.authToken(); token != "" && pr.In.Header.Get("Authorization") == "" {
		pr.Out.Header.Set("Authorization", "Bearer "+token)
		// The injected token is the sole intended credential in token mode.
		// Drop any daemon session cookie the browser sends so it cannot compete
		// with (and, on the daemon, override) the token — a stale lower-role
		// session would otherwise downgrade the request. No-token mode keeps the
		// cookie: there the proxied login session IS the credential.
		stripSessionCookie(pr.Out)
	}
}

// sessionCookieName mirrors auth.SessionCookieName (the daemon session cookie).
// It is duplicated rather than imported so the lean remote-proxy binary does
// not pull in the auth package's transitive dependencies.
const sessionCookieName = "openccu_loom_session"

// stripSessionCookie removes the daemon session cookie from req's Cookie header
// while preserving every other cookie, rebuilding the header from the parsed
// cookies. A token-mode request must never forward a competing session.
func stripSessionCookie(req *http.Request) {
	cookies := req.Cookies()
	hasSession := false
	for _, c := range cookies {
		if c.Name == sessionCookieName {
			hasSession = true
			break
		}
	}
	if !hasSession {
		return
	}
	req.Header.Del("Cookie")
	for _, c := range cookies {
		if c.Name == sessionCookieName {
			continue
		}
		req.AddCookie(c)
	}
}

// modifyResponse folds upstream responses back under the browser-facing
// base: absolute-path redirects gain the base prefix and cookie paths
// are scoped onto it so two instances cannot clobber each other's
// session on the shared HA origin.
func (p *instanceProxy) modifyResponse(resp *http.Response) error {
	if p.isAuthWall(resp) {
		p.serveAuthWall(resp)
		return nil
	}
	// resp.Request is the outbound request, whose header already holds
	// ingress base + instance prefix — exactly the browser-facing base.
	// With no base AND no upstream base path there is nothing to map.
	base := resp.Request.Header.Get(ingressPathHeader)
	if base == "" && p.target.Path == "" {
		return nil
	}
	p.rewriteLocation(resp, base)
	rewriteCookiePaths(resp, base, p.target.Path)
	return nil
}

// rewriteLocation folds Location targets from upstream coordinates into
// browser coordinates: the upstream base path is stripped (on a segment
// boundary) and the browser-facing base is prefixed. Upstream responses
// that already honored the forwarded X-Ingress-Path pass through.
func (p *instanceProxy) rewriteLocation(resp *http.Response, base string) {
	loc := resp.Header.Get("Location")
	if loc == "" {
		return
	}
	// url.Parse sets Host for both absolute ("https://host/…") and
	// protocol-relative ("//host/…") targets. Fold a redirect back at the
	// upstream host into the ingress origin; leave foreign hosts (OIDC issuers
	// etc.) untouched. Routing protocol-relative URLs here keeps them out of the
	// absolute-path branch below, where they would otherwise be reinterpreted.
	if u, err := url.Parse(loc); err == nil && u.Host != "" {
		if u.Host != p.target.Host {
			return
		}
		// Keep the escaped path form and the fragment intact — a decoded
		// %3F would silently become a real query separator.
		rest := stripPathPrefix(u.EscapedPath(), p.target.Path)
		if u.RawQuery != "" {
			rest += "?" + u.RawQuery
		}
		if u.Fragment != "" {
			rest += "#" + u.EscapedFragment()
		}
		p.setLocalLocation(resp, rebase(rest, base))
		return
	}
	// isLocalPath is the single complete leading-slash gate: it lets a relative
	// redirect (no leading slash — it resolves correctly as-is) and the
	// open-redirect forms "//host" / "/\host" both fall through untouched, so
	// only a genuine absolute-path reference is rebased. setLocalLocation is a
	// second gate on the rebased result.
	if !isLocalPath(loc) {
		return
	}
	p.setLocalLocation(resp, rebase(stripPathPrefix(loc, p.target.Path), base))
}

// isLocalPath reports whether s is an absolute-path reference that cannot be
// reinterpreted by a browser as an external URL: a single leading "/" that is
// not immediately followed by another "/" or a "\".
func isLocalPath(s string) bool {
	if s == "/" {
		return true
	}
	return len(s) > 1 && s[0] == '/' && s[1] != '/' && s[1] != '\\'
}

// setLocalLocation writes the rewritten Location header only when the computed
// target is a safe local path, so a rebase can never emit an open redirect.
func (p *instanceProxy) setLocalLocation(resp *http.Response, loc string) {
	if isLocalPath(loc) {
		resp.Header.Set("Location", loc)
	}
}

// stripPathPrefix removes the upstream base path from an absolute-path
// reference, but only on a path-segment boundary — "/apple" must not
// lose an "/app" prefix. The result stays absolute-path shaped.
func stripPathPrefix(ref, prefix string) string {
	if prefix == "" || !strings.HasPrefix(ref, prefix) {
		return ref
	}
	rest := ref[len(prefix):]
	switch {
	case rest == "":
		return "/"
	case isLocalPath(rest):
		// Safe absolute-path remainder ("/x", "/") — the only branch that
		// forwards the tainted value, gated by the complete isLocalPath check.
		return rest
	case rest[0] == '?' || rest[0] == '#':
		return "/" + rest
	case strings.HasPrefix(rest, "//") || strings.HasPrefix(rest, `/\`):
		// Unsafe absolute-path form ("//…" / "/\…") — collapse it to the base
		// root rather than forward something a browser reads as an external URL.
		// A two-character prefix test (never a bare leading "/") so it reads as a
		// complete redirect check, not an incomplete one.
		return "/"
	default:
		return ref // not a segment boundary
	}
}

// rebase puts an absolute-path reference under the browser-facing base,
// leaving values that already carry the base untouched.
func rebase(ref, base string) string {
	if base == "" {
		return ref
	}
	if ref == base || strings.HasPrefix(ref, base+"/") || strings.HasPrefix(ref, base+"?") {
		return ref
	}
	// base is a non-empty browser-facing base path (leading "/", no trailing
	// "/"), so base + "/" + <ref without its leading slash> is always a local
	// absolute path regardless of ref's shape — no leading-slash test on ref is
	// needed, and normalising the slash this way avoids emitting "base//…".
	return base + "/" + strings.TrimPrefix(ref, "/")
}

// rewriteCookiePaths scopes every Set-Cookie path onto the browser-facing
// base, stripping the upstream base path first (symmetric to
// rewriteLocation — an unstripped upstream path would scope the session
// cookie off the browser-visible tree and break logins). A cookie line
// that fails to parse is passed through verbatim; attributes the parser
// does not model are re-appended from Unparsed.
func rewriteCookiePaths(resp *http.Response, base, targetPath string) {
	lines := resp.Header.Values("Set-Cookie")
	if len(lines) == 0 {
		return
	}
	resp.Header.Del("Set-Cookie")
	for _, line := range lines {
		c, err := http.ParseSetCookie(line)
		if err != nil {
			resp.Header.Add("Set-Cookie", line)
			continue
		}
		switch c.Path {
		case "", "/":
			c.Path = base + "/"
		default:
			c.Path = rebase(stripPathPrefix(c.Path, targetPath), base)
		}
		parts := append([]string{c.String()}, c.Unparsed...)
		resp.Header.Add("Set-Cookie", strings.Join(parts, "; "))
	}
}

// errorHandler serves a 502 when the upstream is unreachable. Client
// disconnects are logged at debug only — they are not an upstream fault.
func (p *instanceProxy) errorHandler(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, context.Canceled) {
		p.log.Debug("client canceled upstream request", "path", r.URL.Path)
		return
	}
	p.log.Warn("upstream request failed", "path", r.URL.Path, "error", err)
	serveUnreachable(w, r, p.inst.Name)
}

// isAuthWall reports whether an upstream answer is the openccu-lite box's
// login wall in front of a browser navigation: a 401/403, or the gate's
// redirect to the box login outside the instance path. Forwarded as is,
// the redirect would be rebased into the ingress panel and bounce back
// into the gate in a loop; the 401/403 would leave a bare JSON error.
func (p *instanceProxy) isAuthWall(resp *http.Response) bool {
	req := resp.Request
	if req == nil || (req.Method != http.MethodGet && req.Method != http.MethodHead) {
		return false
	}
	if !strings.Contains(req.Header.Get("Accept"), "text/html") {
		return false
	}
	if mode := req.Header.Get("Sec-Fetch-Mode"); mode != "" && mode != "navigate" {
		return false
	}
	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return true
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther,
		http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return p.isBoxLoginRedirect(resp.Header.Get("Location"))
	default:
		return false
	}
}

// isBoxLoginRedirect reports whether loc points at the box's /login page,
// which lies outside the instance's base path on the same host.
func (p *instanceProxy) isBoxLoginRedirect(loc string) bool {
	u, err := url.Parse(loc)
	if err != nil || (u.Host != "" && u.Host != p.target.Host) {
		return false
	}
	if u.Path != "/login" || p.target.Path == "" {
		return false
	}
	return !strings.HasPrefix(u.Path, p.target.Path+"/")
}

// serveAuthWall replaces the upstream answer with the page that explains
// how to get through the box's login: pairing, or a token in the options.
func (p *instanceProxy) serveAuthWall(resp *http.Response) {
	// The outbound X-Ingress-Path is ingress base + instance prefix; the
	// pairing route lives at the ingress base.
	root := strings.TrimSuffix(resp.Request.Header.Get(ingressPathHeader), p.prefix)
	status := resp.StatusCode
	if status != http.StatusForbidden {
		status = http.StatusUnauthorized
	}
	var buf bytes.Buffer
	if err := authWallTmpl.Execute(&buf, p.authWallData(localeFor(resp.Request), root+"/-/pair/"+p.inst.Name)); err != nil {
		p.log.Warn("rendering the login explanation failed", "error", err)
	}
	_ = resp.Body.Close()
	resp.Body = io.NopCloser(&buf)
	resp.ContentLength = int64(buf.Len())
	resp.Header = http.Header{
		"Content-Type":   {"text/html; charset=utf-8"},
		"Cache-Control":  {"no-store"},
		"Content-Length": {strconv.Itoa(buf.Len())},
	}
	resp.StatusCode = status
	resp.Status = fmt.Sprintf("%d %s", status, http.StatusText(status))
}
