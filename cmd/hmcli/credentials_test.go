// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"bytes"
	"context"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ─── resolveCredentials ─────────────────────────────────────────────────────

// notATerminal is a stdin stub that is never an interactive terminal, so the
// prompt path is skipped and resolveCredentials never blocks on a read.
var notATerminal = strings.NewReader("")

func TestResolveCredentialsFlagWins(t *testing.T) {
	t.Setenv(envToken, "env-token")
	t.Setenv(envPassword, "env-pass")
	var stderr bytes.Buffer
	token, user, password := resolveCredentials("flag-token", "alice", "flag-pass", notATerminal, &stderr)
	if token != "flag-token" {
		t.Errorf("token = %q, want flag-token (flag must win over env)", token)
	}
	if user != "alice" {
		t.Errorf("user = %q, want alice", user)
	}
	if password != "flag-pass" {
		t.Errorf("password = %q, want flag-pass (flag must win over env)", password)
	}
}

func TestResolveCredentialsTokenFromEnv(t *testing.T) {
	t.Setenv(envToken, "secret-from-env")
	var stderr bytes.Buffer
	token, _, _ := resolveCredentials("", "", "", notATerminal, &stderr)
	if token != "secret-from-env" {
		t.Errorf("token = %q, want secret-from-env", token)
	}
	if strings.Contains(stderr.String(), "secret-from-env") {
		t.Error("resolveCredentials leaked the token to stderr")
	}
}

func TestResolveCredentialsPasswordFromEnv(t *testing.T) {
	t.Setenv(envPassword, "pw-from-env")
	var stderr bytes.Buffer
	_, user, password := resolveCredentials("", "bob", "", notATerminal, &stderr)
	if user != "bob" {
		t.Errorf("user = %q, want bob", user)
	}
	if password != "pw-from-env" {
		t.Errorf("password = %q, want pw-from-env", password)
	}
}

func TestResolveCredentialsNoPromptWhenNotTerminal(t *testing.T) {
	// A basic-auth user with no password and no env, over a non-terminal reader:
	// must return an empty password without prompting or blocking on a read.
	t.Setenv(envToken, "")
	t.Setenv(envPassword, "")
	var stderr bytes.Buffer
	done := make(chan struct{})
	go func() {
		token, user, password := resolveCredentials("", "bob", "", notATerminal, &stderr)
		if token != "" || user != "bob" || password != "" {
			t.Errorf("expected empty token/password, got token=%q user=%q password=%q", token, user, password)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("resolveCredentials blocked on a non-terminal reader")
	}
	if stderr.Len() != 0 {
		t.Errorf("expected no prompt output over a non-terminal reader, got %q", stderr.String())
	}
}

func TestPromptLineReadsSingleLine(t *testing.T) {
	t.Parallel()
	var stderr bytes.Buffer
	got := promptLine(strings.NewReader("s3cr3t\nignored second line\n"), &stderr, "Password: ")
	if got != "s3cr3t" {
		t.Errorf("promptLine = %q, want s3cr3t", got)
	}
	if !strings.Contains(stderr.String(), "Password: ") {
		t.Errorf("prompt %q should be written to stderr", stderr.String())
	}
	// CRLF line endings are trimmed too.
	if got := promptLine(strings.NewReader("windows\r\n"), &stderr, "> "); got != "windows" {
		t.Errorf("promptLine(CRLF) = %q, want windows", got)
	}
}

// ─── warnIfPlaintextCredentials ─────────────────────────────────────────────

func TestWarnIfPlaintextCredentials(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		host       string
		token      string
		user       string
		wantWarn   bool
		wantSubstr string
	}{
		{"http non-loopback with token warns", "http://192.168.1.50:8119", "tok", "", true, "192.168.1.50"},
		{"http remote hostname with token warns", "http://ccu.example.com", "tok", "", true, "ccu.example.com"},
		{"http basic-auth user warns", "http://ccu.example.com", "", "alice", true, "ccu.example.com"},
		{"https non-loopback stays silent", "https://ccu.example.com", "tok", "", false, ""},
		{"http localhost stays silent", "http://localhost:8119", "tok", "", false, ""},
		{"http 127.0.0.1 stays silent", "http://127.0.0.1:8119", "tok", "", false, ""},
		{"http ::1 stays silent", "http://[::1]:8119", "tok", "", false, ""},
		{"no credentials stays silent", "http://ccu.example.com", "", "", false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var stderr bytes.Buffer
			warnIfPlaintextCredentials(tc.host, tc.token, tc.user, &stderr)
			got := stderr.String()
			if tc.wantWarn {
				if !strings.Contains(got, "warning") {
					t.Errorf("expected a warning for %s, got %q", tc.host, got)
				}
				if tc.wantSubstr != "" && !strings.Contains(got, tc.wantSubstr) {
					t.Errorf("warning %q should mention %q", got, tc.wantSubstr)
				}
			} else if got != "" {
				t.Errorf("expected no warning for %s, got %q", tc.host, got)
			}
			// A warning must never echo the token value.
			if tc.token != "" && strings.Contains(got, tc.token) {
				t.Errorf("warning leaked the token: %q", got)
			}
		})
	}
}

func TestIsLoopbackHost(t *testing.T) {
	t.Parallel()
	cases := []struct {
		host string
		want bool
	}{
		{"localhost", true},
		{"LOCALHOST", true},
		{"127.0.0.1", true},
		{"127.0.0.5", true},
		{"::1", true},
		{"", true},
		{"192.168.1.1", false},
		{"ccu.example.com", false},
		{"8.8.8.8", false},
	}
	for _, tc := range cases {
		if got := isLoopbackHost(tc.host); got != tc.want {
			t.Errorf("isLoopbackHost(%q) = %v, want %v", tc.host, got, tc.want)
		}
	}
}

// ─── buildTLSConfig / loadRootCAs ───────────────────────────────────────────

func TestBuildTLSConfigDefaults(t *testing.T) {
	t.Parallel()
	var stderr bytes.Buffer
	cfg, err := buildTLSConfig("", false, &stderr)
	if err != nil {
		t.Fatalf("buildTLSConfig: %v", err)
	}
	if cfg != nil {
		t.Errorf("expected nil TLS config for defaults, got %+v", cfg)
	}
	if stderr.Len() != 0 {
		t.Errorf("expected no warning without --insecure, got %q", stderr.String())
	}
}

func TestBuildTLSConfigInsecure(t *testing.T) {
	t.Parallel()
	var stderr bytes.Buffer
	cfg, err := buildTLSConfig("", true, &stderr)
	if err != nil {
		t.Fatalf("buildTLSConfig: %v", err)
	}
	if cfg == nil || !cfg.InsecureSkipVerify {
		t.Fatalf("expected InsecureSkipVerify=true, got %+v", cfg)
	}
	if !strings.Contains(stderr.String(), "warning") || !strings.Contains(stderr.String(), "insecure") {
		t.Errorf("expected an --insecure warning on stderr, got %q", stderr.String())
	}
}

func TestBuildTLSConfigInsecureNilStderrIsNoop(t *testing.T) {
	t.Parallel()
	// A nil stderr must not panic — callers that don't care about the warning
	// (mainly tests) can omit it.
	if _, err := buildTLSConfig("", true, nil); err != nil {
		t.Fatalf("buildTLSConfig: %v", err)
	}
}

func TestBuildTLSConfigCABundleErrors(t *testing.T) {
	t.Parallel()
	if _, err := buildTLSConfig(filepath.Join(t.TempDir(), "nope.pem"), false, nil); err == nil {
		t.Error("expected an error for a missing CA bundle")
	}
	bad := filepath.Join(t.TempDir(), "bad.pem")
	if err := os.WriteFile(bad, []byte("not a pem"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := buildTLSConfig(bad, false, nil); err == nil {
		t.Error("expected an error for a CA bundle with no valid PEM")
	}
}

// ─── redactHostUserinfo ──────────────────────────────────────────────────────

func TestRedactHostUserinfo(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		host string
		want string
	}{
		{"strips user and password", "https://user:pass@ccu.example.com/", "https://ccu.example.com/"},
		{"strips user only", "https://user@ccu.example.com:8119", "https://ccu.example.com:8119"},
		{"no userinfo unchanged", "https://ccu.example.com:8119", "https://ccu.example.com:8119"},
		{"scheme-less host unchanged", "localhost:8119", "localhost:8119"},
		{"empty unchanged", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := redactHostUserinfo(tc.host); got != tc.want {
				t.Errorf("redactHostUserinfo(%q) = %q, want %q", tc.host, got, tc.want)
			}
		})
	}
}

func TestNewDaemonClientRedactsHostUserinfoFromErrors(t *testing.T) {
	t.Parallel()
	// A non-routable address so the request fails fast and the error message
	// wraps the base URL the client built — that message must never contain
	// the password embedded in --host.
	c, err := newDaemonClient(clientConfig{baseURL: "http://user:s3cr3t@127.0.0.1:1/", timeout: time.Second})
	if err != nil {
		t.Fatalf("newDaemonClient: %v", err)
	}
	var out map[string]any
	err = c.getJSON(context.Background(), "/x", &out)
	if err == nil {
		t.Fatal("expected a connection error")
	}
	if strings.Contains(err.Error(), "s3cr3t") {
		t.Errorf("error leaked the --host password: %q", err.Error())
	}
	if strings.Contains(err.Error(), "user:") {
		t.Errorf("error leaked --host userinfo: %q", err.Error())
	}
}

// TestNewDaemonClientTrustsCustomCA is an end-to-end check: a TLS test server
// with a self-signed cert is unreachable with the default roots, reachable when
// its cert is supplied via --cacert, and reachable when TLS verification is
// skipped via --insecure.
func TestNewDaemonClientTrustsCustomCA(t *testing.T) {
	t.Parallel()
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(ts.Close)

	// Write the server's self-signed cert to a PEM file for the --cacert path.
	caPath := filepath.Join(t.TempDir(), "ca.pem")
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ts.Certificate().Raw})
	if err := os.WriteFile(caPath, pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	// Default roots: the self-signed cert is untrusted → must fail.
	def, err := newDaemonClient(clientConfig{baseURL: ts.URL, timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("newDaemonClient(default): %v", err)
	}
	var out map[string]any
	if err := def.getJSON(context.Background(), "/", &out); err == nil {
		t.Error("expected TLS verification failure with default roots, got success")
	}

	// Custom CA: cert trusted → must succeed with verification still on.
	withCA, err := newDaemonClient(clientConfig{baseURL: ts.URL, cacert: caPath, timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("newDaemonClient(cacert): %v", err)
	}
	if err := withCA.getJSON(context.Background(), "/", &out); err != nil {
		t.Errorf("expected success with custom CA, got %v", err)
	}
	if !ensureVerifyOn(t, withCA) {
		t.Error("custom-CA client must keep certificate verification on")
	}

	// Insecure: verification skipped → must succeed.
	insecureClient, err := newDaemonClient(clientConfig{baseURL: ts.URL, insecure: true, timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("newDaemonClient(insecure): %v", err)
	}
	if err := insecureClient.getJSON(context.Background(), "/", &out); err != nil {
		t.Errorf("expected success with --insecure, got %v", err)
	}
}

// ensureVerifyOn reports whether the client's transport keeps TLS verification
// enabled (InsecureSkipVerify is false).
func ensureVerifyOn(t *testing.T, c *daemonClient) bool {
	t.Helper()
	tr, ok := c.http.Transport.(*http.Transport)
	if !ok || tr.TLSClientConfig == nil {
		return true // no custom TLS config means Go's secure defaults apply
	}
	return !tr.TLSClientConfig.InsecureSkipVerify
}
