//go:build e2e

package e2e

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/SukramJ/openccu-loom/tests/e2e/harness"
)

// TestE2ECallbackListenerHealthIsReported pins the wiring, not the helper.
//
// recordCallbackHealth on its own proves only that a tracker can hold the
// component; what matters is that a booted daemon actually records it, because
// the condition it reports — a callback listener that failed to bind, leaving
// the daemon reachable but unable to receive a single CCU event — is otherwise
// indistinguishable from a healthy daemon on every surface an operator has.
//
// Removing the recordCallbackHealth call from the composition root turns this
// red; the unit test in cmd/openccu-loom stays green, which is the point.
func TestE2ECallbackListenerHealthIsReported(t *testing.T) {
	t.Parallel()
	h := harness.Start(t, harness.Options{})
	if err := h.REST().LoginSession(harness.AdminUser, harness.AdminPass); err != nil {
		t.Fatalf("login: %v", err)
	}

	req, err := h.REST().NewRequest(http.MethodGet, "/api/v1/health", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := h.REST().Do(req)
	if err != nil {
		t.Fatalf("GET /api/v1/health: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var body struct {
		Components []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
			Note   string `json:"note"`
		} `json:"components"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode health: %v", err)
	}

	names := make([]string, 0, len(body.Components))
	for _, c := range body.Components {
		names = append(names, c.Name)
		if c.Name != "callback.listeners" {
			continue
		}
		// This harness binds its listeners successfully, so the component
		// has to be present AND healthy. A daemon reporting the component
		// as degraded here would mean the push path is dead, which every
		// event-driven test in this package silently depends on.
		if c.Status != "healthy" {
			t.Fatalf("callback.listeners is %q (%s) — the daemon has no push path, "+
				"so no CCU event can arrive", c.Status, c.Note)
		}
		return
	}
	t.Fatalf("/api/v1/health reports no callback.listeners component; a callback "+
		"listener that fails to bind would leave the daemon reporting itself fine "+
		"while no CCU event can ever arrive. Components: %v", names)
}
