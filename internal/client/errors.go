// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package client

import "regexp"

// sensitivePatterns is the list of (compiled regex, replacement) pairs
// applied by [SanitizeErrorMessage].
var sensitivePatterns = []struct {
	re          *regexp.Regexp
	replacement string
}{
	// IPv4 addresses
	{regexp.MustCompile(`(?i)\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\b`), "<ip-redacted>"},
	// Hostnames with at least one dot (e.g. "ccu3.local", "192.168.1.1" above catches raw IPs)
	{regexp.MustCompile(`(?i)\b[a-zA-Z0-9][-a-zA-Z0-9]*(\.[a-zA-Z0-9][-a-zA-Z0-9]*)+\b`), "<host-redacted>"},
	// Session IDs
	{regexp.MustCompile(`(?i)['"]?session[_-]?id['"]?\s*[:=]\s*['"]?[\w-]+['"]?`), "session_id=<redacted>"},
	// Passwords in URL/params
	{regexp.MustCompile(`(?i)['"]?password['"]?\s*[:=]\s*['"][^'"]*['"]`), "password=<redacted>"},
	{regexp.MustCompile(`(?i)['"]?passwd['"]?\s*[:=]\s*['"][^'"]*['"]`), "passwd=<redacted>"},
	// HTTP Basic Authorization header
	{regexp.MustCompile(`(?i)Authorization\s*:\s*Basic\s+[\w+/=]+`), "Authorization: Basic <redacted>"},
}

// SanitizeErrorMessage removes or masks potentially sensitive information (IP
// addresses, hostnames, session IDs, passwords) from an error message before
// it is written to logs or surfaced in API responses.
//
// Use this wherever an error message originates from a transport layer that
// may embed the CCU's IP address.
func SanitizeErrorMessage(message string) string {
	result := message
	for _, p := range sensitivePatterns {
		result = p.re.ReplaceAllString(result, p.replacement)
	}
	return result
}
