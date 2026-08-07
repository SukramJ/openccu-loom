// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strings"

	"golang.org/x/term"
)

// Environment variables that supply credentials off the command line, so a
// token or password never has to appear in shell history or the process table
// (visible via `ps`). Flags still win when set; these are the fallback.
const (
	envToken    = "OPENCCU_LOOM_TOKEN"
	envPassword = "OPENCCU_LOOM_PASSWORD"
)

// resolveCredentials fills empty credential flags from the environment and, as
// a last resort, an interactive terminal prompt. Precedence is: explicit flag →
// environment variable → interactive prompt. Credentials are never logged.
//
// in is the standard-input stream (os.Stdin in production); the prompt only
// fires when in is an interactive terminal, so pipes, redirects, and test
// harnesses never block on a read.
//
// The interactive prompt is scoped to the password of an explicit basic-auth
// user (--user set, --password missing): that is an unambiguous request for a
// credential, so prompting is expected. The bearer token is deliberately NOT
// prompted for speculatively — a token has no such "clearly wanted" signal, and
// a blind prompt on every command would both annoy operators of unauthenticated
// daemons and block any process whose stdin happens to be a terminal. The
// OPENCCU_LOOM_TOKEN environment variable is the off-argv path for a token.
func resolveCredentials(token, user, password string, in io.Reader, stderr io.Writer) (rToken, rUser, rPassword string) {
	rToken, rUser, rPassword = token, user, password

	if rToken == "" {
		rToken = os.Getenv(envToken)
	}
	if rPassword == "" {
		rPassword = os.Getenv(envPassword)
	}

	// Basic-auth username without a password: prompt for the password.
	if rUser != "" && rPassword == "" && isTerminal(in) {
		rPassword = promptLine(in, stderr, "Password: ")
	}

	return rToken, rUser, rPassword
}

// isTerminal reports whether in is an interactive character device (a TTY). It
// avoids a dependency on golang.org/x/term by inspecting the file mode, which
// is sufficient to gate the prompt.
func isTerminal(in io.Reader) bool {
	f, ok := in.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// promptLine writes prompt to stderr and reads a single line from in, trimming
// the trailing newline. When in is a terminal, the line is read with echo
// suppressed via golang.org/x/term (BSD-3-Clause; already pulled into the
// module graph transitively by golang.org/x/crypto and golang.org/x/net, so
// depending on it directly adds no new module to the build, only a real use
// of one already resolved) — a plaintext password otherwise stays in the
// terminal scrollback for as long as the session's history is kept. Callers
// that hand in a non-terminal reader (tests, or a redirected/piped stdin)
// fall back to a plain buffered line read, which keeps promptLine testable
// without a pty.
func promptLine(in io.Reader, stderr io.Writer, prompt string) string {
	_, _ = fmt.Fprint(stderr, prompt)
	if f, ok := in.(*os.File); ok && isTerminal(f) {
		if line, err := term.ReadPassword(int(f.Fd())); err == nil {
			_, _ = fmt.Fprintln(stderr) // ReadPassword consumes the Enter key without echoing it.
			return strings.TrimRight(string(line), "\r\n")
		}
		// Fall through to the plain reader — e.g. the terminal was detached
		// between the isTerminal check and the read.
	}
	line, _ := bufio.NewReader(in).ReadString('\n')
	return strings.TrimRight(line, "\r\n")
}

// warnIfPlaintextCredentials prints a one-line warning to stderr when
// Authorization credentials would be transmitted over a plaintext http://
// connection to a non-loopback host, where they could be observed on the wire.
// TLS (https://) and loopback destinations are silent.
func warnIfPlaintextCredentials(host, token, user string, stderr io.Writer) {
	if token == "" && user == "" {
		return // nothing sensitive to leak
	}
	u, err := url.Parse(host)
	if err != nil {
		return
	}
	if u.Scheme != "http" {
		return // https:// (or a ws/wss caller) is encrypted
	}
	if isLoopbackHost(u.Hostname()) {
		return
	}
	_, _ = fmt.Fprintf(stderr,
		"warning: sending credentials over plaintext http:// to %s — use https:// or a loopback host to protect them\n",
		u.Host)
}

// warnInsecureTLS prints a one-line warning to stderr when --insecure disables
// TLS certificate verification. warnIfPlaintextCredentials only covers plain
// http://; an https:// connection with verification turned off is silently
// just as exposed — any certificate is accepted, so a machine-in-the-middle
// can present its own and intercept everything, including the credentials
// this CLI sends. A nil stderr is treated as a no-op sink so callers that do
// not care about the message (unit tests) do not have to supply one.
func warnInsecureTLS(stderr io.Writer) {
	if stderr == nil {
		return
	}
	_, _ = fmt.Fprintln(stderr,
		"warning: TLS certificate verification is disabled (--insecure) — the connection is not authenticated and can be intercepted; use --cacert with a trusted CA bundle instead when possible")
}

// redactHostUserinfo strips a userinfo component (user:pass@ or user@) from a
// --host value before it is stored as a client's base URL. Go's HTTP client
// never applies destination-URL userinfo as Basic auth on its own — only a
// proxy URL's userinfo is used, in net/http.Transport's CONNECT/proxy dial
// path — so a value like https://user:pass@ccu/ authenticates nothing; it is
// dead input that does nothing but resurface the password in every error
// message built from the base URL (target := baseURL + path, then wrapped
// verbatim into an *httpStatusError). Stripping it once here, before it is
// stored, keeps it out of every message and log line derived from the base
// URL afterward. A malformed or scheme-less host (e.g. a bare "host:port"
// value, which net/url would otherwise misparse as scheme "host") is
// returned unchanged so it still reaches the existing validation/error path
// unmodified.
func redactHostUserinfo(host string) string {
	u, err := url.Parse(host)
	if err != nil || u.Scheme == "" || u.Host == "" || u.User == nil {
		return host
	}
	u.User = nil
	return u.String()
}

// isLoopbackHost reports whether host names the local machine, so plaintext
// credentials to it never leave the host and need no warning.
func isLoopbackHost(host string) bool {
	if host == "" || strings.EqualFold(host, "localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// loadRootCAs reads a PEM CA bundle from path and returns a certificate pool
// containing it. An empty path returns a nil pool (use the system roots).
func loadRootCAs(path string) (*x509.CertPool, error) {
	if path == "" {
		return nil, nil
	}
	pem, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read CA bundle %s: %w", path, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("CA bundle %s contains no valid PEM certificates", path)
	}
	return pool, nil
}

// buildTLSConfig assembles the TLS client configuration from the --cacert and
// --insecure flags. It returns nil when neither is set, so callers fall back to
// Go's secure defaults (system roots, verification on).
//
// Certificate verification stays ON unless --insecure is explicitly passed;
// --cacert only adds a custom root of trust, it does not weaken verification.
// stderr receives a warning whenever insecure is set — see warnInsecureTLS —
// so the opt-out is never silent; pass nil in a context that has no writer
// to hand (the warning is then simply not printed, e.g. in unit tests).
func buildTLSConfig(cacert string, insecure bool, stderr io.Writer) (*tls.Config, error) {
	if insecure {
		warnInsecureTLS(stderr)
	}
	if cacert == "" && !insecure {
		return nil, nil
	}
	pool, err := loadRootCAs(cacert)
	if err != nil {
		return nil, err
	}
	//nolint:gosec // InsecureSkipVerify is an explicit, documented operator opt-in (--insecure); it is off by default.
	return &tls.Config{
		RootCAs:            pool,
		InsecureSkipVerify: insecure,
		MinVersion:         tls.VersionTLS12,
	}, nil
}
