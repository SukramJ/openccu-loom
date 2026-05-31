// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

// HARegistryDescriptionRules returns a copy of the generated HA-registry
// rule slice so external packages (e.g. contract tests) can inspect the
// full rule set without accessing the unexported variable directly.
func HARegistryDescriptionRules() []HARegistryDescriptionRule {
	out := make([]HARegistryDescriptionRule, len(haRegistryDescriptionRules))
	copy(out, haRegistryDescriptionRules)
	return out
}
