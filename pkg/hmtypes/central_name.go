// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmtypes

import (
	"errors"
	"fmt"
	"regexp"
)

// centralNameRE is the allowlist every central name has to satisfy.
//
// A central name is not merely a label: it is a path segment of the XML-RPC
// callback URL the daemon advertises to the CCU (`/RPC2/<central_name>`), it
// prefixes every canonical interface id (`<central>-<iface>`), and it scopes
// MQTT topics and REST paths. Restricting it to characters that survive all of
// those verbatim — no percent-encoding, no separators, no case folding — is
// what keeps the announced URL and the route the callback server matches the
// same string.
var centralNameRE = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// ValidateCentralName reports why name cannot be used as a central name, or
// nil when it can. Enforce it at every boundary that accepts a name — config
// load, the admin REST endpoints, the onboarding wizard, the persistent store
// — because the callback router rejects anything outside the allowlist at
// dispatch time, where the only symptom is a CCU whose events silently never
// arrive.
func ValidateCentralName(name string) error {
	if name == "" {
		return errors.New("central name: required")
	}
	if !centralNameRE.MatchString(name) {
		return fmt.Errorf(
			"central name %q: only letters, digits, %q and %q are allowed — the name is a path segment of the "+
				"callback URL the CCU pushes events to", name, "-", "_",
		)
	}
	return nil
}

// IsValidCentralName reports whether name satisfies [ValidateCentralName].
// Used by the callback router, which needs the predicate rather than the
// reason.
func IsValidCentralName(name string) bool {
	return centralNameRE.MatchString(name)
}
