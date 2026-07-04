// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/rpcserver"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// ctxBlockingLoader is a device.ValueLoader whose reads block until the
// context is cancelled. It signals when a read has entered (so the test knows
// the background self-reload goroutine is in-flight) and when it has returned
// (so the test can assert the goroutine drained).
type ctxBlockingLoader struct {
	entered  chan struct{}
	returned chan struct{}
	entOnce  sync.Once
	retOnce  sync.Once
}

func newCtxBlockingLoader() *ctxBlockingLoader {
	return &ctxBlockingLoader{entered: make(chan struct{}), returned: make(chan struct{})}
}

func (l *ctxBlockingLoader) block(ctx context.Context) error {
	l.entOnce.Do(func() { close(l.entered) })
	<-ctx.Done()
	l.retOnce.Do(func() { close(l.returned) })
	return ctx.Err()
}

func (l *ctxBlockingLoader) GetValue(ctx context.Context, _ string, _ hmenum.Parameter) (any, error) {
	return nil, l.block(ctx)
}

func (l *ctxBlockingLoader) GetParamset(ctx context.Context, _ string, _ hmenum.ParamsetKey) (map[string]any, error) {
	return nil, l.block(ctx)
}

// eventMethodCall is a minimal XML-RPC `event` callback: 4 positional params
// (interface_id, channel_address, parameter, value). A non-numeric value on a
// readable INTEGER data point fails inline coercion, which is what drives the
// handler's background self-reload.
const eventMethodCall = `<?xml version="1.0"?>` +
	`<methodCall><methodName>event</methodName><params>` +
	`<param><value><string>HmIP-RF</string></value></param>` +
	`<param><value><string>DRAIN0001:1</string></value></param>` +
	`<param><value><string>TESTNUM</string></value></param>` +
	`<param><value><string>not-a-number</string></value></param>` +
	`</params></methodCall>`

// TestRegisterCentralCallbacksDeregisterDrainsHandler verifies that the
// deregister closure returned by registerCentralCallbacks drains the callback
// handler's in-flight background goroutines — i.e. it calls
// CallbackHandlers.Stop() alongside the route deregistration. Without that
// call Stop() is dead code and the self-reload / device-refresh goroutines
// leak past a live RemoveCentral or shutdown.
//
// The reproducer drives a real self-reload through the registered callback
// route: a coerce-failing `event` on a readable INTEGER DP starts a background
// LoadValue that blocks in the (context-bound) value loader. deregister() must
// then block until Stop() cancels the handler context and the goroutine
// drains; on the un-wired code deregister() returns immediately with the
// goroutine still blocked.
func TestRegisterCentralCallbacksDeregisterDrainsHandler(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)

	unit, err := central.New(central.Config{Name: "drain-ccu", Logger: logger})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}

	loader := newCtxBlockingLoader()
	d := device.New(device.Config{
		InterfaceID: "HmIP-RF", Interface: hmenum.InterfaceHmIPRF,
		Address: "DRAIN0001", Model: "HmIP-STH", Name: "Drain",
	})
	unit.ModelRegistry.Put(d)
	ch := d.AddChannel("DRAIN0001:1", 1, "TEST", hmenum.ParamsetKeyValues)
	dpKey, err := hmtypes.NewDataPointKey("HmIP-RF", "DRAIN0001:1", hmenum.ParamsetKeyValues, "TESTNUM")
	if err != nil {
		t.Fatalf("NewDataPointKey: %v", err)
	}
	ch.Put(generic.NewDataPoint[int32](generic.Spec{
		Key: dpKey,
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeInteger,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	}))
	d.SetValueLoader(loader)

	// The listener is only bound (127.0.0.1:0), never Serve()'d — callbacks are
	// dispatched directly through ServeHTTP, so no Serve/Close lifecycle is needed.
	srv, err := rpcserver.NewXMLRPCServer(rpcserver.XMLRPCConfig{Addr: "127.0.0.1:0", Logger: logger})
	if err != nil {
		t.Fatalf("NewXMLRPCServer: %v", err)
	}

	deps := WireDeps{
		CallbackServer:  srv,
		CallbackPort:    8120,
		CallbackHostFor: func(*config.CentralConfig) string { return "127.0.0.1" },
	}
	cc := &config.CentralConfig{Name: "drain-ccu"}
	_, _, deregister := registerCentralCallbacks(deps, cc, unit, logger)
	if deregister == nil {
		t.Fatal("registerCentralCallbacks returned a nil deregister closure")
	}

	// Deliver the coerce-failing event through the registered route so the
	// handler kicks off its background self-reload.
	req := httptest.NewRequest(http.MethodPost, "/RPC2/drain-ccu", strings.NewReader(eventMethodCall))
	req.Header.Set("Content-Type", "text/xml")
	srv.ServeHTTP(httptest.NewRecorder(), req)

	select {
	case <-loader.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("self-reload goroutine never started; the coerce-failing event did not trigger a background reload")
	}

	deregister()

	select {
	case <-loader.returned:
		// deregister() drained the in-flight self-reload — Stop() is wired.
	case <-time.After(2 * time.Second):
		t.Fatal("deregister() returned without draining the in-flight self-reload goroutine; " +
			"CallbackHandlers.Stop() must be wired into the deregister closure")
	}
}
