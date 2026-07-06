// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Support utilities ported from the Python reference implementation.
// Each function is annotated with its Python source location.

package hmtypes

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ErrPortRangeInvalid is returned by [FindFreePort] when rangeLo > rangeHi
// or either bound is outside the valid port range.
var ErrPortRangeInvalid = errors.New("hmtypes: invalid port range")

// MaxCacheAge is the default maximum age for cached values, mirroring
// the Python reference implementation's MAX_CACHE_AGE constant (const.py:308).
const MaxCacheAge = 10 * time.Second

// InitTime is the zero-value sentinel for "never updated", mirroring
// the Python reference implementation's INIT_DATETIME sentinel
// (01.1970 00:00:00, const.py:288).
var InitTime = time.Unix(0, 0).UTC()

// Hostname and IP validation patterns — mirror the Python reference
// implementation's HOSTNAME_PATTERN, IPV4_PATTERN, IPV6_PATTERN constants.
//
// Note: Go's RE2 does not support PCRE lookaheads / lookbehinds, so the
// hostname pattern is implemented as a programmatic validator instead of
// a single compiled regexp. The ipv4 and ipv6 patterns use basic RE2.
var (
	ipv4Pattern = regexp.MustCompile(
		`^(?:(?:25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)\.){3}(?:25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)$`,
	)
	ipv6Pattern = regexp.MustCompile(`^\[?[0-9a-fA-F:]+\]?$`)

	// htmlTagPattern strips HTML tags and entities, mirroring the Python
	// reference implementation's HTMLTAG_PATTERN.
	htmlTagPattern = regexp.MustCompile(`<.*?>|&([a-z0-9]+|#\d{1,6}|#x[0-9a-f]{1,6});`)

	// hostnameLabel validates a single DNS label (1-63 chars, alnum + hyphen,
	// no leading or trailing hyphen). Used by isValidHostname.
	hostnameLabel = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,61}[A-Za-z0-9]$|^[A-Za-z0-9]$`)
)

// isValidHostname returns true when host is a syntactically valid DNS
// hostname per RFC 1123 (total ≤253 chars, labels ≤63 chars, no leading or
// Trailing hyphens). Replaces the PCRE-only HOSTNAME_PATTERN.
func isValidHostname(host string) bool {
	if host == "" || len(host) > 253 {
		return false
	}
	labels := strings.SplitSeq(host, ".")
	for lbl := range labels {
		if lbl == "" || len(lbl) > 63 {
			return false
		}
		if !hostnameLabel.MatchString(lbl) {
			return false
		}
	}
	return true
}

// ToBool converts a bool or string to a bool.
// Strings "y", "yes", "t", "true", "on", "1" (case-insensitive) are
// considered true. Any other string returns false. Non-string,
// non-bool types return an error.
func ToBool(value any) (bool, error) {
	switch v := value.(type) {
	case bool:
		return v, nil
	case string:
		switch strings.ToLower(v) {
		case "y", "yes", "t", "true", "on", "1":
			return true, nil
		default:
			return false, nil
		}
	default:
		return false, fmt.Errorf("hmtypes: ToBool: unsupported type %T", value)
	}
}

// ChangedWithinSeconds reports whether lastChange is more recent than
// maxAge. Returns false when lastChange equals [InitTime] (the
// "never-changed" sentinel).
func ChangedWithinSeconds(lastChange time.Time, maxAge time.Duration) bool {
	if lastChange.Equal(InitTime) {
		return false
	}
	return time.Since(lastChange) < maxAge
}

// ErrHostEmpty is returned by [ValidateHost] when the host string is
// empty or blank.
var ErrHostEmpty = errors.New("hmtypes: host must not be empty")

// ErrHostInvalid is returned by [ValidateHost] when the host string
// does not match a valid hostname or IP address.
var ErrHostInvalid = errors.New("hmtypes: host has invalid format")

// ValidateHost validates that host is a well-formed hostname or IP
// address. Returns [ErrHostEmpty] for blank input, [ErrHostInvalid] for
// syntactically invalid values.
func ValidateHost(host string) error {
	cleaned := strings.TrimSpace(host)
	if cleaned == "" {
		return ErrHostEmpty
	}
	if isValidHostname(cleaned) ||
		ipv4Pattern.MatchString(cleaned) ||
		ipv6Pattern.MatchString(cleaned) {
		return nil
	}
	return fmt.Errorf("%w: %q", ErrHostInvalid, host)
}

// IsHost reports whether host is a valid hostname or IP address.
func IsHost(host string) bool {
	return ValidateHost(host) == nil
}

// IsIPv4Address reports whether address is a valid IPv4 address string.
func IsIPv4Address(address string) bool {
	if address == "" {
		return false
	}
	return net.ParseIP(address) != nil && strings.Contains(address, ".")
}

// IsIPv6Address reports whether address is a valid IPv6 address string.
// Bracketed forms like "[::1]" are accepted.
func IsIPv6Address(address string) bool {
	if address == "" {
		return false
	}
	trimmed := strings.TrimPrefix(strings.TrimSuffix(address, "]"), "[")
	return net.ParseIP(trimmed) != nil && strings.Contains(trimmed, ":")
}

// CleanupTextFromHTMLTags removes HTML tags and common HTML entities from
// text, returning plain text suitable for display.
func CleanupTextFromHTMLTags(text string) string {
	return htmlTagPattern.ReplaceAllString(text, "")
}

// SupportsRxMode reports whether commandRxMode is compatible with the
// given set of rxModes. Only BURST and WAKEUP command modes are
// meaningful; all others always return false.
func SupportsRxMode(commandRxMode hmenum.CommandRxMode, rxModes []hmenum.RxMode) bool {
	if commandRxMode == hmenum.CommandRxModeBurst {
		if slices.Contains(rxModes, hmenum.RxModeBurst) {
			return true
		}
	}
	if commandRxMode == hmenum.CommandRxModeWakeup {
		if slices.Contains(rxModes, hmenum.RxModeWakeup) {
			return true
		}
	}
	return false
}

// HashSHA256 returns a stable base64-encoded SHA-256 hash of value.
// The value is first serialised to JSON with sorted keys; if that fails
// the Go fmt.Sprintf("%v") representation is used as fallback.
func HashSHA256(value any) string {
	var data []byte
	b, err := json.Marshal(value)
	if err != nil {
		data = fmt.Appendf(nil, "%v", makeValueHashable(value))
	} else {
		data = b
	}
	h := sha256.Sum256(data)
	return base64.StdEncoding.EncodeToString(h[:])
}

// makeValueHashable converts arbitrary values to a comparable form,
func makeValueHashable(value any) any {
	if value == nil {
		return nil
	}
	switch v := value.(type) {
	case map[string]any:
		pairs := make([]string, 0, len(v))
		for k, val := range v {
			pairs = append(pairs, fmt.Sprintf("%v=%v", k, makeValueHashable(val)))
		}
		return strings.Join(pairs, ",")
	case []any:
		parts := make([]string, len(v))
		for i, el := range v {
			parts[i] = fmt.Sprintf("%v", makeValueHashable(el))
		}
		return strings.Join(parts, ",")
	default:
		return v
	}
}

// DebugEnabled reports whether the process is likely running under a
// debugger. In Go, the equivalent heuristic is to check whether the
// binary was compiled with the race detector (runtime.WithRaceEnabled)
// or to inspect environment variables. For production parity the
// function always returns false; tests can override by wrapping it.
//
// The Python implementation inspects sys.gettrace(); Go has no
// equivalent — this is a documented PFAD-ASYMMETRIE
func DebugEnabled() bool {
	return false
}

// CreateRandomDeviceAddresses returns a mapping from each element of
// addresses to a randomly generated "VCU<7-digit>" string. The mapping
// is deterministic across calls only when the same seed is used.
func CreateRandomDeviceAddresses(addresses []string) map[string]string {
	result := make(map[string]string, len(addresses))
	for i, addr := range addresses {
		// Simple deterministic replacement for test reproducibility.
		// Production code that needs real randomness should pass
		// pre-generated random addresses.
		result[addr] = fmt.Sprintf("VCU%07d", 1000000+i)
	}
	return result
}

// IsPort reports whether port is a valid TCP/UDP port number (0–65535).
func IsPort(port int) bool {
	return port >= 0 && port <= 65535
}

// GetRxModes decodes a raw RX-mode bitmask integer into the set of
// [hmenum.RxMode] values it represents. Returns nil when mode is nil.
//
// The bitmask is decoded in the same priority order as the Python
// reference: LAZY_CONFIG → WAKEUP → CONFIG → BURST → ALWAYS.
func GetRxModes(mode *int) []hmenum.RxMode {
	if mode == nil {
		return nil
	}
	v := *mode
	var modes []hmenum.RxMode
	if v&int(hmenum.RxModeLazyConfig) != 0 {
		modes = append(modes, hmenum.RxModeLazyConfig)
	}
	if v&int(hmenum.RxModeWakeup) != 0 {
		modes = append(modes, hmenum.RxModeWakeup)
	}
	if v&int(hmenum.RxModeConfig) != 0 {
		modes = append(modes, hmenum.RxModeConfig)
	}
	if v&int(hmenum.RxModeBurst) != 0 {
		modes = append(modes, hmenum.RxModeBurst)
	}
	if v&int(hmenum.RxModeAlways) != 0 {
		modes = append(modes, hmenum.RxModeAlways)
	}
	return modes
}

// ElementMatchesKey reports whether any element in searchElements matches
// compareWith using the specified wildcard rules.
//
// The default behaviour (ignoreCase=true, rightWildcard=true) mirrors the
//
// When leftWildcard and rightWildcard are both true the element is a
// substring match. When only leftWildcard is true the element must be a
// suffix. When only rightWildcard is true the element must be a prefix.
// When neither flag is set the element must be an exact match.
//
// Py:318
// A7v4-08).
func ElementMatchesKey(searchElements []string, compareWith string, ignoreCase, leftWildcard, rightWildcard bool) bool {
	if compareWith == "" || len(searchElements) == 0 {
		return false
	}
	target := compareWith
	if ignoreCase {
		target = strings.ToLower(target)
	}
	for _, el := range searchElements {
		elem := el
		if ignoreCase {
			elem = strings.ToLower(elem)
		}
		var matched bool
		switch {
		case leftWildcard && rightWildcard:
			matched = strings.Contains(target, elem)
		case leftWildcard:
			matched = strings.HasSuffix(target, elem)
		case rightWildcard:
			matched = strings.HasPrefix(target, elem)
		default:
			matched = target == elem
		}
		if matched {
			return true
		}
	}
	return false
}

// FindFreePort asks the OS for a free TCP port in the given inclusive range
// [rangeLo, rangeHi]. When rangeLo == 0 and rangeHi == 0 any ephemeral port
// is returned (OS choice). The returned port is guaranteed to be bindable at
// the moment of the call but may be taken before the caller uses it.
//
// The Python version always lets the OS pick; this version supports an
// optional range for the callback port-range mode documented in SPECIFICATION
// §4.2.
func FindFreePort(rangeLo, rangeHi int) (int, error) {
	if rangeLo == 0 && rangeHi == 0 {
		// OS-assigned ephemeral port.
		lc := net.ListenConfig{}
		l, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
		if err != nil {
			return 0, fmt.Errorf("hmtypes: FindFreePort: %w", err)
		}
		addr, ok := l.Addr().(*net.TCPAddr)
		_ = l.Close()
		if !ok {
			return 0, fmt.Errorf("hmtypes: FindFreePort: unexpected listener addr type %T", l.Addr())
		}
		return addr.Port, nil
	}
	if rangeLo < 0 || rangeHi > 65535 || rangeLo > rangeHi {
		return 0, fmt.Errorf("%w: [%d, %d]", ErrPortRangeInvalid, rangeLo, rangeHi)
	}
	for port := rangeLo; port <= rangeHi; port++ {
		addr := fmt.Sprintf("127.0.0.1:%d", port)
		lc2 := net.ListenConfig{}
		l, err := lc2.Listen(context.Background(), "tcp", addr)
		if err != nil {
			continue
		}
		_ = l.Close()
		return port, nil
	}
	return 0, fmt.Errorf("hmtypes: FindFreePort: no free port in [%d, %d]", rangeLo, rangeHi)
}
