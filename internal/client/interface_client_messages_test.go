// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/client/rega"
	"github.com/SukramJ/openccu-loom/internal/client/transport/jsonrpc"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// newAckServer builds a minimal fake CCU that returns the supplied JSON
// string as the ReGa.runScript result.
func newAckServer(t *testing.T, result any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		resp := struct {
			Result any `json:"result"`
		}{Result: result}
		raw, _ := json.Marshal(resp)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(raw)
	}))
}

func newRegaRunner(t *testing.T, srvURL string) *rega.Runner {
	t.Helper()
	c, err := jsonrpc.New(jsonrpc.Config{Endpoint: srvURL})
	if err != nil {
		t.Fatal(err)
	}
	r, err := rega.NewRunner(rega.Config{Client: c})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// TestInterfaceClientAcknowledgeMessageNoRunner verifies that without a
// RegaRunner and without a backend the method returns ErrUnsupported (Task #34).
func TestInterfaceClientAcknowledgeMessageNoRunner(t *testing.T) {
	c, _ := New(Config{
		CentralName: "main",
		Interface:   hmenum.InterfaceHmIPRF,
		Caller:      CallerFunc(func(context.Context, string, []any) (any, error) { return nil, nil }),
	})
	_, err := c.AcknowledgeMessage(context.Background(), "4711", nil)
	if !errors.Is(err, hmerr.ErrUnsupported) {
		t.Fatalf("expected ErrUnsupported, got %v", err)
	}
}

// TestInterfaceClientAcknowledgeMessageSuccess verifies that with a
// RegaRunner the method returns (true, nil) on a successful CCU
// acknowledgement (Task #34).
func TestInterfaceClientAcknowledgeMessageSuccess(t *testing.T) {
	srv := newAckServer(t, `{"success":true,"error":""}`)
	defer srv.Close()

	c, _ := New(Config{
		CentralName: "main",
		Interface:   hmenum.InterfaceHmIPRF,
		Caller:      CallerFunc(func(context.Context, string, []any) (any, error) { return nil, nil }),
		RegaRunner:  newRegaRunner(t, srv.URL),
	})

	ok, err := c.AcknowledgeMessage(context.Background(), "4711", nil)
	if err != nil {
		t.Fatalf("AcknowledgeMessage: %v", err)
	}
	if !ok {
		t.Fatal("expected success=true")
	}
}

// TestInterfaceClientAcknowledgeMessageCCURejectsIsError verifies that
// a CCU-level rejection (success=false + error message) surfaces as an
// error from AcknowledgeMessage (Task #34 — functional parity with
// rega.Runner.AcknowledgeMessage).
func TestInterfaceClientAcknowledgeMessageCCURejectsIsError(t *testing.T) {
	srv := newAckServer(t, `{"success":false,"error":"message not found"}`)
	defer srv.Close()

	c, _ := New(Config{
		CentralName: "main",
		Interface:   hmenum.InterfaceHmIPRF,
		Caller:      CallerFunc(func(context.Context, string, []any) (any, error) { return nil, nil }),
		RegaRunner:  newRegaRunner(t, srv.URL),
	})

	ok, err := c.AcknowledgeMessage(context.Background(), "9999", nil)
	if err == nil {
		t.Fatal("expected error from CCU rejection")
	}
	if ok {
		t.Fatal("success must be false on CCU rejection")
	}
}
