// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package config

import (
	"fmt"
	"math"
	"net"
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/SukramJ/openccu-loom/internal/i18n"
)

// validateFieldRanges range-checks the config leaves whose out-of-range
// values are otherwise absorbed silently by a defaulting helper at the
// point of use.
//
// A silent fallback is worse than a rejection here because the operator
// gets no signal at all: the SPA answers 200, the schema promises the
// field takes effect, and the daemon then runs on a value the operator
// never chose. Every check below names the field and the accepted range
// so the SPA can render the reason verbatim in its save toast.
func validateFieldRanges(c *Config) error {
	if err := validateLocale(c.Locale); err != nil {
		return err
	}
	if err := validateCallbackPortRange(c.Callback.PortRange); err != nil {
		return err
	}
	if err := validateWebhook(&c.North.Webhook); err != nil {
		return err
	}
	if err := validateMCP(&c.North.MCP); err != nil {
		return err
	}
	if err := validateMatter(&c.North.Matter); err != nil {
		return err
	}
	if err := validateRESTLimits(&c.North.REST); err != nil {
		return err
	}
	if err := validateFiniteFloats(c); err != nil {
		return err
	}
	return validateNonNegativeDurations(c)
}

// validateLocale rejects a locale the daemon has no catalogue for. An
// unknown tag is not harmless: every lookup falls through to English, so
// a typo produces a daemon that silently ignores the operator's language
// choice on every north-bound surface at once.
func validateLocale(locale string) error {
	if locale == "" {
		// applyDefaults fills "en"; an empty value is "unset", not wrong.
		return nil
	}
	available := i18n.AvailableLocales()
	if slices.Contains(available, locale) {
		return nil
	}
	return fmt.Errorf("config: locale %q has no translation catalogue (available: %s)",
		locale, strings.Join(available, ", "))
}

// validateCallbackPortRange parses callback.port_range so a malformed
// range is refused at the save that introduces it. It used to be parsed
// only at bind time inside the daemon's callback bring-up, which meant a
// nonsense value was accepted by the config surface and only surfaced as
// a boot failure much later.
func validateCallbackPortRange(raw string) error {
	if raw == "" {
		return nil
	}
	if _, _, err := ParsePortRange(raw); err != nil {
		return fmt.Errorf("config: callback.port_range: %w", err)
	}
	return nil
}

// validateWebhook checks the outbound webhook block.
//
// URL is validated whenever it is set — not only while the bridge is
// enabled — so an operator who prepares the endpoint before flipping the
// toggle learns about a typo at the save rather than at the next boot.
func validateWebhook(w *NorthWebhook) error {
	if w.URL != "" {
		u, err := url.Parse(w.URL)
		switch {
		case err != nil:
			return fmt.Errorf("config: north.webhook.url: invalid URL %q: %w", w.URL, err)
		case u.Scheme != "http" && u.Scheme != "https":
			return fmt.Errorf("config: north.webhook.url: scheme must be http or https, got %q", w.URL)
		case u.Host == "":
			return fmt.Errorf("config: north.webhook.url: missing host in %q", w.URL)
		}
	}
	if w.TimeoutMs < 0 {
		return fmt.Errorf("config: north.webhook.timeout_ms must be >= 0 (0 selects the default): %d", w.TimeoutMs)
	}
	return nil
}

// validateMCP checks the Model Context Protocol mount path. The value
// becomes an [net/http.ServeMux] pattern verbatim, which is why the rules
// live on the config type itself — see [NorthMCP.ValidateMountPath].
func validateMCP(m *NorthMCP) error {
	if m.Path == "" {
		return nil
	}
	return m.ValidateMountPath()
}

// maxMatterDiscriminator is the largest 12-bit commissioning
// discriminator (Matter §5.1.1.1). The config field is a uint16, so a
// larger value is representable and would be truncated on the wire —
// commissioners then browse an mDNS subtype nobody advertises.
const maxMatterDiscriminator = 0xFFF

// Matter PBKDF2 iteration bounds (Matter §3.10). Values outside the
// window are rejected by a certified commissioner during PASE, which
// presents to the operator as a pairing that aborts for no stated reason.
const (
	minMatterPBKDFIterations = 1000
	maxMatterPBKDFIterations = 100000
)

// validateMatter checks the bridge's bind address and the commissioning
// parameters a commissioner verifies against the spec.
func validateMatter(m *NorthMatter) error {
	if err := validateHostPort("north.matter.listen", m.Listen); err != nil {
		return err
	}
	if m.Discriminator > maxMatterDiscriminator {
		return fmt.Errorf("config: north.matter.discriminator: out of range 0-%d (12-bit): %d",
			maxMatterDiscriminator, m.Discriminator)
	}
	if it := m.Commissioning.Iterations; it != 0 &&
		(it < minMatterPBKDFIterations || it > maxMatterPBKDFIterations) {
		return fmt.Errorf("config: north.matter.commissioning.iterations: out of range %d-%d (0 selects the default): %d",
			minMatterPBKDFIterations, maxMatterPBKDFIterations, it)
	}
	return nil
}

// validateHostPort checks a "host:port" listen address. An empty value
// selects the subsystem's own default and is left alone.
func validateHostPort(field, addr string) error {
	if addr == "" {
		return nil
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("config: %s: expected \"host:port\", got %q: %w", field, addr, err)
	}
	if host != "" && net.ParseIP(host) == nil && strings.ContainsAny(host, "/ ") {
		return fmt.Errorf("config: %s: invalid host in %q", field, addr)
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		return fmt.Errorf("config: %s: non-numeric port in %q", field, addr)
	}
	if n < 0 || n > 65535 {
		return fmt.Errorf("config: %s: port out of range 0-65535 in %q", field, addr)
	}
	return nil
}

// validateRESTLimits range-checks the REST capacity knobs. All three
// treat zero as "use the default", so only a negative value is wrong —
// and a negative one is silently clamped at the point of use, which
// leaves the operator with a limiter or replay buffer they did not
// configure and no way to tell.
func validateRESTLimits(r *NorthREST) error {
	rps := r.RateLimit.RequestsPerSecond
	switch {
	case math.IsNaN(rps) || math.IsInf(rps, 0):
		return fmt.Errorf("config: north.rest.rate_limit.requests_per_second must be a finite number: %v", rps)
	case rps < 0:
		return fmt.Errorf("config: north.rest.rate_limit.requests_per_second must be >= 0 (0 selects the default): %v", rps)
	}
	if r.RateLimit.Burst < 0 {
		return fmt.Errorf("config: north.rest.rate_limit.burst must be >= 0 (0 selects the default): %d", r.RateLimit.Burst)
	}
	if r.WS.ReplayCapacity < 0 {
		return fmt.Errorf("config: north.rest.ws.replay_capacity must be >= 0 (0 disables replay): %d", r.WS.ReplayCapacity)
	}
	return nil
}

// durationType is the reflect type every duration leaf carries.
var durationType = reflect.TypeOf(time.Duration(0))

// durationsWhereNegativeIsMeaningful lists the duration leaves for which a
// negative value is a documented instruction rather than a mistake. Each
// entry is the field path with slice indices stripped.
//
// centrals[].check_connection_interval switches the per-central
// check_connection poll off: zero already means "use the default", so the
// only value left to express "do not run it at all" is a negative one.
var durationsWhereNegativeIsMeaningful = map[string]struct{}{
	"centrals.check_connection_interval": {},
}

// sliceIndex matches the "[7]" a walked slice element contributes to its
// path, so an exemption can be declared once for every element.
var sliceIndex = regexp.MustCompile(`\[\d+\]`)

// negativeIsMeaningful reports whether a negative value at path is a
// documented sentinel rather than an error.
func negativeIsMeaningful(path string) bool {
	_, ok := durationsWhereNegativeIsMeaningful[sliceIndex.ReplaceAllString(path, "")]
	return ok
}

// validateNonNegativeDurations rejects a negative value on any
// time.Duration leaf anywhere in the config.
//
// It is reflective rather than a hand-written list because the list is
// the part that rots: every duration knob treats zero as "use the
// default" and a negative one as an interval, a retention window or a
// timeout that has already elapsed — a scheduler job that fires without
// pause, a retention purge that deletes everything it sees. A new knob
// added next release is covered without anybody remembering to extend a
// table.
func validateNonNegativeDurations(c *Config) error {
	var negative []string
	walkLeaves(reflect.ValueOf(c), "", func(path string, v reflect.Value) {
		if v.Type() != durationType {
			return
		}
		d := time.Duration(v.Int())
		if d < 0 && !negativeIsMeaningful(path) {
			negative = append(negative, fmt.Sprintf("%s (%s)", path, d))
		}
	})
	if len(negative) == 0 {
		return nil
	}
	return fmt.Errorf("config: duration fields must not be negative (0 selects the default): %s",
		strings.Join(negative, ", "))
}

// validateFiniteFloats rejects NaN and ±Inf on any float leaf anywhere in
// the config.
//
// YAML spells all three (`.nan`, `.inf`, `-.inf`) and they survive parsing
// and every range check — but JSON cannot encode them. The daemon deep-copies
// the config through a JSON round-trip whenever it re-assembles the effective
// config (the DB-tier overlay does this on every boot and every section save),
// so a single non-finite leaf would make the copy fail. Rejecting the value at
// the boundary that ingests it keeps that failure impossible instead of
// discovering it several layers away from the field that caused it.
//
// The check is reflective for the same reason the duration check is: a
// hand-written list of float leaves is the part that rots.
func validateFiniteFloats(c *Config) error {
	var bad []string
	walkLeaves(reflect.ValueOf(c), "", func(path string, v reflect.Value) {
		switch v.Kind() { //nolint:exhaustive // only float leaves are of interest here
		case reflect.Float32, reflect.Float64:
			if f := v.Float(); math.IsNaN(f) || math.IsInf(f, 0) {
				bad = append(bad, fmt.Sprintf("%s (%v)", path, f))
			}
		}
	})
	if len(bad) == 0 {
		return nil
	}
	return fmt.Errorf("config: numeric fields must be finite (no .nan / .inf): %s",
		strings.Join(bad, ", "))
}

// walkLeaves visits every scalar leaf under rv, building the dotted config
// path from the yaml tags so a rejection names the field the operator typed.
// Maps are left opaque: they carry credential and override tables, never
// numeric knobs.
func walkLeaves(rv reflect.Value, prefix string, visit func(path string, v reflect.Value)) {
	for rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return
		}
		rv = rv.Elem()
	}
	switch rv.Kind() {
	case reflect.Struct:
		rt := rv.Type()
		for i := range rt.NumField() {
			f := rt.Field(i)
			if !f.IsExported() {
				continue
			}
			tag, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
			if tag == "-" {
				continue
			}
			if tag == "" {
				tag = f.Name
			}
			path := tag
			if f.Anonymous {
				path = prefix
			} else if prefix != "" {
				path = prefix + "." + tag
			}
			walkLeaves(rv.Field(i), path, visit)
		}
	case reflect.Slice, reflect.Array:
		for i := range rv.Len() {
			walkLeaves(rv.Index(i), fmt.Sprintf("%s[%d]", prefix, i), visit)
		}
	default:
		visit(prefix, rv)
	}
}
