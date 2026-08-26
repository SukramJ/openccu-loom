// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// programExecuteConditionalMockServer serves ReGa.runScript for
// hubJSONRPCWriter.ExecuteProgramConditional tests. The configured result
// string is returned verbatim as the script's raw stdout so both
// well-formed and malformed CCU responses can be exercised.
type programExecuteConditionalMockServer struct {
	srv        *httptest.Server
	result     string
	lastScript string
}

func newProgramExecuteConditionalMock(t *testing.T, result string) *programExecuteConditionalMockServer {
	t.Helper()
	m := &programExecuteConditionalMockServer{result: result}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var env struct {
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if err := json.Unmarshal(body, &env); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if script, ok := env.Params["script"].(string); ok {
			m.lastScript = script
		}
		payload, _ := json.Marshal(map[string]any{"result": m.result})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(payload)
	}))
	t.Cleanup(m.srv.Close)
	return m
}

// TestHubJSONRPCWriter_ExecuteProgramConditional_ConditionMet verifies the
// happy path: the ReGa script reports executed=true and the writer forwards
// the program id into the substituted script.
func TestHubJSONRPCWriter_ExecuteProgramConditional_ConditionMet(t *testing.T) {
	t.Parallel()
	m := newProgramExecuteConditionalMock(t, `{"executed":true}`)
	w := newWriterAgainst(t, m.srv.URL)

	executed, err := w.ExecuteProgramConditional(context.Background(), "prog-42")
	if err != nil {
		t.Fatalf("ExecuteProgramConditional: %v", err)
	}
	if !executed {
		t.Fatal("executed=false, want true (condition met)")
	}
	if !strings.Contains(m.lastScript, `"prog-42"`) {
		t.Errorf("script missing program id: %s", m.lastScript)
	}
}

// TestHubJSONRPCWriter_ExecuteProgramConditional_ConditionNotMet verifies
// that executed=false decodes without error when the CCU reports the
// program's condition was not satisfied.
func TestHubJSONRPCWriter_ExecuteProgramConditional_ConditionNotMet(t *testing.T) {
	t.Parallel()
	m := newProgramExecuteConditionalMock(t, `{"executed":false}`)
	w := newWriterAgainst(t, m.srv.URL)

	executed, err := w.ExecuteProgramConditional(context.Background(), "prog-7")
	if err != nil {
		t.Fatalf("ExecuteProgramConditional: %v", err)
	}
	if executed {
		t.Fatal("executed=true, want false (condition not met)")
	}
}

// TestHubJSONRPCWriter_ExecuteProgramConditional_CCUErrorPropagates verifies
// that a malformed script result surfaces as an error classified via
// [hmerr.ErrClientException] instead of being swallowed as executed=false.
func TestHubJSONRPCWriter_ExecuteProgramConditional_CCUErrorPropagates(t *testing.T) {
	t.Parallel()
	m := newProgramExecuteConditionalMock(t, "not json")
	w := newWriterAgainst(t, m.srv.URL)

	executed, err := w.ExecuteProgramConditional(context.Background(), "prog-99")
	if err == nil {
		t.Fatal("expected error for malformed CCU script output")
	}
	if executed {
		t.Error("executed=true on error path, want false")
	}
	if !errors.Is(err, hmerr.ErrClientException) {
		t.Errorf("error should classify as ErrClientException, got %v", err)
	}
}
