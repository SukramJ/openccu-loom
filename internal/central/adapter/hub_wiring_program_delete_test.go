// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

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

	"github.com/SukramJ/openccu-loom/internal/model/hub"
)

// programDeleteMockServer serves ReGa.runScript for
// hubJSONRPCWriter.DeleteProgram tests. The configured result string is
// returned verbatim as the script's raw stdout so success, not-found, and
// malformed CCU responses can all be exercised; failHTTP short-circuits the
// handler with a 500 to simulate a transport-level CCU failure.
type programDeleteMockServer struct {
	srv        *httptest.Server
	result     string
	failHTTP   bool
	lastScript string
}

func newProgramDeleteMock(t *testing.T, result string) *programDeleteMockServer {
	t.Helper()
	m := &programDeleteMockServer{result: result}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if m.failHTTP {
			http.Error(w, "internal backend exception", http.StatusInternalServerError)
			return
		}
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

// TestHubJSONRPCWriter_DeleteProgram_Success verifies the happy path: a "1"
// script result yields a nil error and the program id reaches the
// substituted delete_program script.
func TestHubJSONRPCWriter_DeleteProgram_Success(t *testing.T) {
	t.Parallel()
	m := newProgramDeleteMock(t, "1")
	w := newWriterAgainst(t, m.srv.URL)

	if err := w.DeleteProgram(context.Background(), "prog-42"); err != nil {
		t.Fatalf("DeleteProgram: %v", err)
	}
	if !strings.Contains(m.lastScript, `"prog-42"`) {
		t.Errorf("script missing program id: %s", m.lastScript)
	}
}

// TestHubJSONRPCWriter_DeleteProgram_NotFound verifies a "0" script result
// (the id no longer resolves to a program object) maps to
// [hub.ErrProgramNotFound].
func TestHubJSONRPCWriter_DeleteProgram_NotFound(t *testing.T) {
	t.Parallel()
	m := newProgramDeleteMock(t, "0")
	w := newWriterAgainst(t, m.srv.URL)

	err := w.DeleteProgram(context.Background(), "prog-7")
	if !errors.Is(err, hub.ErrProgramNotFound) {
		t.Fatalf("err = %v, want hub.ErrProgramNotFound", err)
	}
}

// TestHubJSONRPCWriter_DeleteProgram_CCUErrorPropagates verifies that a
// transport-level CCU failure (the ReGa.runScript call itself erroring out)
// surfaces as a plain error rather than being misclassified as
// [hub.ErrProgramNotFound].
func TestHubJSONRPCWriter_DeleteProgram_CCUErrorPropagates(t *testing.T) {
	t.Parallel()
	m := newProgramDeleteMock(t, "1")
	m.failHTTP = true
	w := newWriterAgainst(t, m.srv.URL)

	err := w.DeleteProgram(context.Background(), "prog-99")
	if err == nil {
		t.Fatal("expected error for CCU transport failure")
	}
	if errors.Is(err, hub.ErrProgramNotFound) {
		t.Error("transport failure must not be misclassified as ErrProgramNotFound")
	}
}

// TestHubJSONRPCWriter_DeleteProgram_MalformedResult verifies a non-numeric
// script result (a CCU-side scripting anomaly, not the documented 0/1
// contract) surfaces as a parse error distinct from ErrProgramNotFound.
func TestHubJSONRPCWriter_DeleteProgram_MalformedResult(t *testing.T) {
	t.Parallel()
	m := newProgramDeleteMock(t, "not-a-number")
	w := newWriterAgainst(t, m.srv.URL)

	err := w.DeleteProgram(context.Background(), "prog-1")
	if err == nil {
		t.Fatal("expected a parse error for a non-numeric script result")
	}
	if errors.Is(err, hub.ErrProgramNotFound) {
		t.Error("malformed result must not be misclassified as ErrProgramNotFound")
	}
}
