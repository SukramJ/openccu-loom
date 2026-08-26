// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
)

// wsSchemaTopicCommand mirrors the subset of wsSchemaCommand
// (wsapi_schema_test.go) this test needs, plus the wire "topic" field that
// struct does not decode.
type wsSchemaTopicCommand struct {
	Name        string `json:"name"`
	Kind        string `json:"kind,omitempty"`
	Topic       string `json:"topic,omitempty"`
	Description string `json:"description"`
}

// TestWSAPISchemaHasCentralReadinessChangedBroadcast pins the WS-API
// catalogue entry for the readiness broadcast: assets/wsapi.json must carry
// a "central.readiness_changed" command whose topic follows the
// "central.{name}.readiness" convention every other per-central broadcast
// uses. Losing this entry silently breaks generated client type packages
// and documentation, since neither is compiled against the Go source.
func TestWSAPISchemaHasCentralReadinessChangedBroadcast(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "assets", "wsapi.json"))
	if err != nil {
		t.Fatalf("read wsapi.json: %v", err)
	}
	var doc struct {
		Commands []wsSchemaTopicCommand `json:"commands"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse wsapi.json: %v", err)
	}

	var found *wsSchemaTopicCommand
	for i := range doc.Commands {
		if doc.Commands[i].Name == "central.readiness_changed" {
			found = &doc.Commands[i]
			break
		}
	}
	if found == nil {
		t.Fatal(`assets/wsapi.json has no "central.readiness_changed" command entry`)
	}
	if found.Kind != "broadcast" {
		t.Errorf(`central.readiness_changed: Kind = %q, want "broadcast"`, found.Kind)
	}
	const wantTopic = "central.{name}.readiness"
	if found.Topic != wantTopic {
		t.Errorf("central.readiness_changed: Topic = %q, want %q", found.Topic, wantTopic)
	}
	if found.Description == "" {
		t.Error("central.readiness_changed: Description is empty, want a one-sentence summary")
	}
}

// TestSystemCCUEntryMarshalsReadinessObject verifies that the SystemCCUEntry
// DTO serializes its Readiness field as a nested JSON object carrying all
// four documented keys (phase, ready, interfaces_loaded, interfaces_total) —
// the REST /system/ccu response shape the SPA's generated types depend on.
func TestSystemCCUEntryMarshalsReadinessObject(t *testing.T) {
	t.Parallel()

	entry := handlers.SystemCCUEntry{
		Name: "ccu-01",
		Readiness: handlers.CentralReadiness{
			Phase:            "loading_devices",
			Ready:            false,
			InterfacesLoaded: 2,
			InterfacesTotal:  4,
		},
	}

	raw, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("json.Marshal(SystemCCUEntry): %v", err)
	}

	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal into map: %v", err)
	}

	readinessRaw, ok := decoded["readiness"]
	if !ok {
		t.Fatal(`SystemCCUEntry JSON has no top-level "readiness" key`)
	}

	var readiness map[string]json.RawMessage
	if err := json.Unmarshal(readinessRaw, &readiness); err != nil {
		t.Fatalf(`unmarshal "readiness" object: %v`, err)
	}

	for _, key := range []string{"phase", "ready", "interfaces_loaded", "interfaces_total"} {
		if _, ok := readiness[key]; !ok {
			t.Errorf(`"readiness" object missing key %q (got %v)`, key, readiness)
		}
	}
}
