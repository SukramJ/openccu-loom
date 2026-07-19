// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package remoteproxy implements the OpenCCU-Loom Remote ingress proxy:
// a small, self-contained reverse proxy that surfaces one or more remote
// OpenCCU-Loom instances through a single Home Assistant Ingress panel.
// See docs/adr/0054-remote-ingress-proxy-addon.md for the trust model
// and the deliberate scope limits (UI/REST/WS only, no daemon).
package remoteproxy

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
)

// Options is the add-on configuration. The Supervisor renders the
// operator's add-on options to /data/options.json; the proxy reads that
// file directly instead of round-tripping every field through env vars.
// loom:reachable:reason="constructed by LoadOptions from the add-on options file and consumed by New in cmd/openccu-loom-remote; the type heuristic does not follow constructor returns of the proxy binary"
type Options struct {
	LogLevel  string     `json:"log_level"`
	Instances []Instance `json:"instances"`
}

// Instance describes one remote OpenCCU-Loom daemon reachable from the
// proxy. Name doubles as the URL path segment (`/i/<name>/`) when more
// than one instance is configured, hence the strict slug constraint.
// loom:reachable:reason="element type of Options.Instances, decoded from the add-on options JSON and consumed by newInstanceProxy; only referenced through the Options composite"
type Instance struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	// Token is an optional API token of the remote instance. When set,
	// the proxy injects it as a Bearer credential into every upstream
	// request that carries no credential of its own, so HA admins land
	// in the UI without a second login (ADR 0054). When empty, requests
	// pass through untouched and the remote login page takes over.
	Token string `json:"token"`
	// TLSInsecure disables certificate verification for this instance's
	// https upstream — an explicit operator opt-in for self-signed LAN
	// certificates.
	TLSInsecure bool `json:"tls_insecure"`
}

// nameRE mirrors the add-on schema constraint: the name becomes a URL
// path segment and a DOM id on the overview page, so it stays a slug.
var nameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

// LoadOptions reads and validates the add-on options file.
func LoadOptions(path string) (Options, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // G304: the path is the operator-chosen options file (CLI flag), not request input.
	if err != nil {
		return Options{}, fmt.Errorf("read options: %w", err)
	}
	var opts Options
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&opts); err != nil {
		// The Supervisor may add fields the proxy does not know yet
		// (e.g. schema defaults of a newer add-on version); fall back
		// to a lenient decode so an upgrade never bricks the panel.
		opts = Options{}
		if lerr := json.Unmarshal(raw, &opts); lerr != nil {
			return Options{}, fmt.Errorf("decode options: %w", lerr)
		}
	}
	if err := opts.Validate(); err != nil {
		return Options{}, err
	}
	return opts, nil
}

// Validate checks the options for the invariants the proxy relies on.
func (o *Options) Validate() error {
	switch o.LogLevel {
	case "", "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("log_level %q: must be one of debug|info|warn|error", o.LogLevel)
	}
	if len(o.Instances) == 0 {
		return errors.New("at least one instance must be configured")
	}
	seen := make(map[string]struct{}, len(o.Instances))
	for i := range o.Instances {
		inst := &o.Instances[i]
		// Stray surrounding whitespace is a classic paste mistake; the
		// add-on schema tolerates it too, so trim instead of rejecting.
		inst.Name = strings.TrimSpace(inst.Name)
		if !nameRE.MatchString(inst.Name) {
			return fmt.Errorf("instance %d: name %q must match %s", i, inst.Name, nameRE)
		}
		if _, dup := seen[inst.Name]; dup {
			return fmt.Errorf("instance name %q is configured twice", inst.Name)
		}
		seen[inst.Name] = struct{}{}
		u, err := parseInstanceURL(inst.URL)
		if err != nil {
			return fmt.Errorf("instance %q: %w", inst.Name, err)
		}
		inst.URL = u.String()
	}
	return nil
}

// parseInstanceURL normalizes an upstream base URL: http/https only, a
// host is required, no query/fragment/userinfo, and a trailing slash on
// the path is dropped so path joining stays predictable.
func parseInstanceURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("url %q: %w", raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("url %q: scheme must be http or https", raw)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("url %q: host is missing", raw)
	}
	if u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return nil, fmt.Errorf("url %q: query, fragment and userinfo are not allowed", raw)
	}
	u.Path = strings.TrimSuffix(u.Path, "/")
	u.RawPath = strings.TrimSuffix(u.RawPath, "/")
	return u, nil
}
