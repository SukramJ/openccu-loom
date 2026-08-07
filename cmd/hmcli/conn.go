// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"flag"
	"io"
	"os"
	"time"
)

// defaultHost is the daemon REST base URL used when --host is not given.
const defaultHost = "http://localhost:8119"

// connFlags is the connection flag set shared by every REST-backed command
// group (devices, sysvar, program, paramset). Centralising it here means the
// off-argv credential fallback, custom-CA support, TLS opt-out, and the
// plaintext-credential warning are wired once and behave identically across all
// groups.
type connFlags struct {
	host     string
	token    string
	user     string
	password string
	cacert   string
	insecure bool
	timeout  time.Duration
	jsonOut  bool
}

// bind registers the shared connection flags on fs.
func (f *connFlags) bind(fs *flag.FlagSet) {
	fs.StringVar(&f.host, "host", defaultHost, "daemon REST base URL")
	fs.StringVar(&f.token, "token", "", "API bearer token (or set "+envToken+")")
	fs.StringVar(&f.user, "user", "", "basic-auth username")
	fs.StringVar(&f.password, "password", "", "basic-auth password (or set "+envPassword+")")
	fs.StringVar(&f.cacert, "cacert", "", "path to a PEM CA bundle to trust for TLS")
	fs.BoolVar(&f.insecure, "insecure", false, "skip TLS certificate verification (dangerous; off by default)")
	fs.DurationVar(&f.timeout, "timeout", 60*time.Second, "request timeout")
	fs.BoolVar(&f.jsonOut, "json", false, "emit raw JSON instead of a human-readable table")
}

// client resolves off-argv credentials, warns if they would cross a plaintext
// link, and builds a daemonClient with the configured TLS trust. stderr carries
// the interactive prompt and the plaintext warning; stdin is used only when it
// is an interactive terminal.
func (f *connFlags) client(stderr io.Writer) (*daemonClient, error) {
	token, user, password := resolveCredentials(f.token, f.user, f.password, os.Stdin, stderr)
	warnIfPlaintextCredentials(f.host, token, user, stderr)
	return newDaemonClient(clientConfig{
		baseURL:  f.host,
		token:    token,
		user:     user,
		password: password,
		cacert:   f.cacert,
		insecure: f.insecure,
		timeout:  f.timeout,
		stderr:   stderr,
	})
}
