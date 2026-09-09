// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	hadiscovery "github.com/SukramJ/go-hamqtt/discovery"

	"github.com/SukramJ/openccu-loom/internal/metrics"
)

func mustJSON(t *testing.T, body map[string]any) []byte {
	t.Helper()
	buf, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return buf
}

// TestValidateDiscoveryBodyRejectsASilentlyDroppedKey is the case the whole
// guard exists for. Home Assistant's discovery schema is extra=REMOVE_EXTRA:
// an undeclared key is dropped with no error on the wire and no line in any
// log, so without this check the only symptom is a feature that does nothing.
func TestValidateDiscoveryBodyRejectsASilentlyDroppedKey(t *testing.T) {
	t.Parallel()

	err := ValidateDiscoveryBody(string(HAComponentButton), mustJSON(t, map[string]any{
		"unique_id":     "u",
		"command_topic": "gh/x/set",
		"state_topic":   "gh/x",
	}))
	if err == nil {
		t.Fatal("a state_topic on a stateless button validated")
	}
	if !errors.Is(err, hadiscovery.ErrInvalidBundle) {
		t.Errorf("err does not block the publish: %v", err)
	}
}

// TestValidateDiscoveryBodyAllowsTheParityMarker pins the one deliberate
// exception. `translation_key` is inert on the wire and is what the
// cross-stack parity tooling compares against the Python integration; the
// validator must not turn that signal into a build failure.
func TestValidateDiscoveryBodyAllowsTheParityMarker(t *testing.T) {
	t.Parallel()

	err := ValidateDiscoveryBody(string(HAComponentSensor), mustJSON(t, map[string]any{
		"unique_id":       "u",
		"state_topic":     "gh/x",
		"translation_key": "frequency",
	}))
	if err != nil {
		t.Errorf("the parity marker was reported as a defect: %v", err)
	}
}

// TestValidateDiscoveryBodyTreatsARewriteAsAdvice keeps the guard from failing
// over a spelling Home Assistant accepts. Its AMBIGUOUS_UNITS lookup is a
// `.get(unit, unit)`, so the legacy micro sign is rewritten, not refused —
// and a test that fails for something that works is one people re-run instead
// of read.
func TestValidateDiscoveryBodyTreatsARewriteAsAdvice(t *testing.T) {
	t.Parallel()

	err := ValidateDiscoveryBody(string(HAComponentSensor), mustJSON(t, map[string]any{
		"unique_id":           "u",
		"state_topic":         "gh/x",
		"unit_of_measurement": unitMicrogramsPerM3,
	}))
	if err == nil {
		t.Fatal("the legacy spelling was not reported at all")
	}
	if errors.Is(err, hadiscovery.ErrInvalidBundle) {
		t.Errorf("an accepted spelling blocks the publish: %v", err)
	}
	if !errors.Is(err, hadiscovery.ErrAdvisory) {
		t.Errorf("err does not match ErrAdvisory: %v", err)
	}
}

// TestValidateDiscoveryBodyRejectsMalformedInput covers the two ways a caller
// can hand this function something it cannot check, so neither reads as "no
// problems found".
func TestValidateDiscoveryBodyRejectsMalformedInput(t *testing.T) {
	t.Parallel()

	if err := ValidateDiscoveryBody("", []byte(`{}`)); err == nil {
		t.Error("an empty component validated")
	}
	if err := ValidateDiscoveryBody(string(HAComponentSensor), []byte(`{`)); err == nil {
		t.Error("a truncated payload validated")
	}
}

// TestBridgeCountsAnInvalidDiscoveryPayloadAndPublishesItAnyway pins both
// halves of the production wiring.
//
// Counting is the point: a key Home Assistant does not declare is dropped with
// no error on the wire and no line in any log, so mqtt_discovery_invalid is
// the only signal a running daemon gives that a builder emits one.
//
// Publishing anyway is the other half, and it is deliberate. Home Assistant
// drops the offending key and keeps the rest of the entity, so withholding the
// config would replace a partly-working entity with no entity at all.
func TestBridgeCountsAnInvalidDiscoveryPayloadAndPublishesItAnyway(t *testing.T) {
	t.Parallel()

	reg := metrics.NewRegistry()
	col := metrics.NewMqttCollector(reg)
	pub := &recordingPublisher{}
	bridge := NewBridge(BridgeConfig{
		Base:               "gh",
		CentralName:        "inv_ccu",
		HADiscoveryEnabled: true,
		Collector:          col,
	}, pub)

	// `nonsense` is declared by no platform, so Home Assistant would strip it.
	body := mustJSON(t, map[string]any{
		"unique_id":   "u1",
		"state_topic": "gh/x",
		"nonsense":    1,
	})
	if err := bridge.publishDiscovery(context.Background(), "inv_ccu",
		string(HAComponentSensor), "node", "obj", body); err != nil {
		t.Fatalf("publishDiscovery: %v", err)
	}

	if got := col.DiscoveryInvalid("inv_ccu").Value(); got != 1 {
		t.Errorf("DiscoveryInvalid = %d, want 1", got)
	}
	if recs := pub.records(); len(recs) != 1 {
		t.Fatalf("the payload was withheld: %d publishes, want 1", len(recs))
	}

	// A valid payload must not move the counter, or it measures nothing.
	valid := mustJSON(t, map[string]any{"unique_id": "u2", "state_topic": "gh/y"})
	if err := bridge.publishDiscovery(context.Background(), "inv_ccu",
		string(HAComponentSensor), "node", "obj2", valid); err != nil {
		t.Fatalf("publishDiscovery (valid): %v", err)
	}
	if got := col.DiscoveryInvalid("inv_ccu").Value(); got != 1 {
		t.Errorf("DiscoveryInvalid after a valid payload = %d, want 1", got)
	}
}
