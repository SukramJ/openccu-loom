// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/testsupport/hajinja"
)

// renderJinja renders a discovery template over the payload published on
// its topic, with Home Assistant's variables (`value`, and `value_json` when
// the payload is JSON) and a stripped result — through [hajinja], whose
// semantics are pinned against jinja2 itself.
//
// It used to be a lenient pattern matcher of its own, which accepted what
// Jinja rejects: `dict()` of an undefined `hm` rendered as an empty mapping
// here and raised in Home Assistant, so a template this package declared
// green failed for real. A template Jinja cannot render now fails the
// calling test, and a construct outside the subset fails it too rather
// than rendering a placeholder.
func renderJinja(t *testing.T, template, envelope string) string {
	t.Helper()
	out, err := hajinja.RenderValue(template, envelope)
	if err != nil {
		t.Errorf("template %q over %q does not render in Jinja: %v", template, envelope, err)
		return ""
	}
	return out
}
