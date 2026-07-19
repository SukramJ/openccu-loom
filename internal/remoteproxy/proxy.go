// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package remoteproxy

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
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
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") {
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
	client *http.Client
	log    *slog.Logger
}

// newInstanceProxy wires the ReverseProxy for one instance. The URL has
// been validated by LoadOptions; a parse failure here is programmer
// error, hence the error return instead of a panic to keep the caller's
// startup path uniform.
func newInstanceProxy(inst Instance, prefix string, log *slog.Logger) (*instanceProxy, error) {
	target, err := parseInstanceURL(inst.URL)
	if err != nil {
		return nil, fmt.Errorf("instance %q: %w", inst.Name, err)
	}
	p := &instanceProxy{
		inst:   inst,
		target: target,
		prefix: prefix,
		log:    log.With("instance", inst.Name),
	}
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
	if target.Scheme == "https" {
		transport.TLSClientConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
			//nolint:gosec // G402: explicit per-instance operator opt-in for self-signed LAN certificates (ADR 0054).
			InsecureSkipVerify: inst.TLSInsecure,
		}
	}
	p.rp = &httputil.ReverseProxy{
		Rewrite:        p.rewrite,
		ModifyResponse: p.modifyResponse,
		ErrorHandler:   p.errorHandler,
		Transport:      transport,
		// Flush every write immediately so streamed responses (SSE,
		// long polls) traverse both proxy hops without buffering delay.
		FlushInterval: -1,
		ErrorLog:      slog.NewLogLogger(p.log.Handler(), slog.LevelWarn),
	}
	p.client = &http.Client{Transport: transport}
	return p, nil
}

// rewrite shapes the outbound request. The incoming request has already
// had the instance prefix stripped (http.StripPrefix in the router), so
// only the upstream base, forwarding headers, and the optional Bearer
// injection remain.
func (p *instanceProxy) rewrite(pr *httputil.ProxyRequest) {
	pr.SetURL(p.target)
	pr.SetXForwarded()
	// Forward the browser-facing base with the instance prefix appended.
	// Without an Ingress hop (direct access, tests) the prefix alone is
	// still the correct base for multi-instance mounts. The inbound value
	// is sanitized so upstream never sees a spoofable redirect base.
	if base := ingressBase(pr.In) + p.prefix; base != "" {
		pr.Out.Header.Set(ingressPathHeader, base)
	} else {
		pr.Out.Header.Del(ingressPathHeader)
	}
	if p.inst.Token != "" && pr.In.Header.Get("Authorization") == "" {
		pr.Out.Header.Set("Authorization", "Bearer "+p.inst.Token)
	}
}

// modifyResponse folds upstream responses back under the browser-facing
// base: absolute-path redirects gain the base prefix and cookie paths
// are scoped onto it so two instances cannot clobber each other's
// session on the shared HA origin.
func (p *instanceProxy) modifyResponse(resp *http.Response) error {
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
	if u, err := url.Parse(loc); err == nil && u.IsAbs() {
		// Absolute URL pointing back at the upstream host: fold it into
		// the ingress origin. Foreign hosts (OIDC issuers etc.) pass.
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
		resp.Header.Set("Location", rebase(rest, base))
		return
	}
	if !strings.HasPrefix(loc, "/") {
		return // relative redirect: resolves correctly as-is
	}
	resp.Header.Set("Location", rebase(stripPathPrefix(loc, p.target.Path), base))
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
	case rest[0] == '/':
		return rest
	case rest[0] == '?' || rest[0] == '#':
		return "/" + rest
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
	if !strings.HasPrefix(ref, "/") {
		ref = "/" + ref
	}
	return base + ref
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
