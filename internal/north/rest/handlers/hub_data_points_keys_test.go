// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/hub"
)

// TestHubDataPointLegacyNamesComeFromTheModel pins the REST projection's
// legacy names to the keys the hub model publishes for the same aggregates.
//
// The key is the entity identity a consumer registers under. It used to be
// typed out here as a literal beside the model's own TranslationKey, so a
// rename in the model would have moved the entity on one plane and not the
// other — and nothing would have failed.
//
// system_update and daemon_connection are deliberately absent: no model
// object names them, this handler assembles both, so its literal is the only
// source rather than a copy of one.
func TestHubDataPointLegacyNamesComeFromTheModel(t *testing.T) {
	t.Parallel()
	dp := hubDataPoints("home", &hub.Hub{})

	for _, c := range []struct {
		got, want, what string
	}{
		{dp.AlarmMessages.LegacyName, (&hub.AlarmMessages{}).TranslationKey(), "alarm messages"},
		{dp.ServiceMessages.LegacyName, (&hub.ServiceMessages{}).TranslationKey(), "service messages"},
		{dp.Inbox.LegacyName, (&hub.Inbox{}).TranslationKey(), "inbox"},
	} {
		if c.want == "" {
			t.Fatalf("%s: the model returned no key — the guard lost its subject", c.what)
		}
		if c.got != c.want {
			t.Errorf("%s: REST publishes %q, the model publishes %q", c.what, c.got, c.want)
		}
	}
}
