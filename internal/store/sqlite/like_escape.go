// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package sqlite

import "strings"

// escapeLikePrefix escapes LIKE wildcards in a caller-supplied prefix so an
// address containing `%` or `_` matches literally. Every statement in this
// package that binds a prefix into a LIKE declares `ESCAPE '\'` and must bind
// through this helper — the escape clause alone does nothing, it only names
// the escape character the pattern is expected to use.
//
// The rule is not "device addresses cannot contain wildcards". Nothing
// validates the address before it reaches these statements: the cache-clear
// scope checks only that the field is non-empty
// (internal/central/cachereset Scope.Validate) and the energy handler passes
// the `device` query parameter through unchecked, so the pattern is built
// from untrusted text on both the destructive and the read path. Escaping
// here is what makes a prefix delete or a device filter mean one device.
func escapeLikePrefix(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}
