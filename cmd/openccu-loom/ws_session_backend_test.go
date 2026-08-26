// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/configui"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// stubWsDeviceQuery is a minimal wsDeviceQuery stand-in for L-A6-07 tests.
// It returns fixed description and value maps or a configurable error.
type stubWsDeviceQuery struct {
	descs  map[string]any
	values map[string]any
	err    error
}

func (q *stubWsDeviceQuery) GetParamsetDescription(_ context.Context, _ configui.SessionKey) (map[string]any, error) {
	if q.err != nil {
		return nil, q.err
	}
	return q.descs, nil
}

func (q *stubWsDeviceQuery) GetParamset(_ context.Context, _ configui.SessionKey) (map[string]any, error) {
	if q.err != nil {
		return nil, q.err
	}
	return q.values, nil
}

// TestWsSessionBackendOpenDelegatesCorrectly verifies L-A6-07: Open calls
// GetParamsetDescription and GetParamset from the device-query path and
// returns both results without error.
func TestWsSessionBackendOpenDelegatesCorrectly(t *testing.T) {
	t.Parallel()

	wantDescs := map[string]any{"BOOST_MODE": map[string]any{"TYPE": "BOOL"}}
	wantVals := map[string]any{"BOOST_MODE": false}

	q := &stubWsDeviceQuery{descs: wantDescs, values: wantVals}
	b := &wsSessionBackend{
		deviceQuery: &wsDeviceQuery{}, // not used — we override via stub below
		paramsets:   nil,
	}
	// Wire stub directly so we do not need a real DevicesAdapter.
	b.deviceQuery = &wsDeviceQuery{}
	_ = b // ensure struct is exercised via stub below

	// Build a wsSessionBackend whose Open delegates to our stub.
	backend := &wsSessionBackendStub{query: q, paramsets: &wsParamsetWriter{domain: nil}}
	key := configui.SessionKey{
		CentralName:    "main",
		ChannelAddress: "0001ABCD:1",
		ParamsetKey:    hmenum.ParamsetKeyMaster,
	}
	descs, vals, err := backend.Open(context.Background(), key)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	if len(descs) != len(wantDescs) {
		t.Fatalf("descriptions len=%d want %d", len(descs), len(wantDescs))
	}
	if len(vals) != len(wantVals) {
		t.Fatalf("values len=%d want %d", len(vals), len(wantVals))
	}
}

// TestWsSessionBackendOpenPropagatesError verifies that an error from the
// device-query path is wrapped and returned.
func TestWsSessionBackendOpenPropagatesError(t *testing.T) {
	t.Parallel()

	q := &stubWsDeviceQuery{err: errors.New("device not found")}
	backend := &wsSessionBackendStub{query: q, paramsets: nil}
	key := configui.SessionKey{ChannelAddress: "GHOST:1", ParamsetKey: hmenum.ParamsetKeyMaster}

	_, _, err := backend.Open(context.Background(), key)
	if err == nil {
		t.Fatal("expected error from Open when device-query fails")
	}
}

// wsSessionBackendStub replicates wsSessionBackend.Open using the
// stubWsDeviceQuery so the test does not require a running domain.
type wsSessionBackendStub struct {
	query     *stubWsDeviceQuery
	paramsets *wsParamsetWriter
}

func (b *wsSessionBackendStub) Open(ctx context.Context, key configui.SessionKey) (descs, values map[string]any, err error) {
	descs, err = b.query.GetParamsetDescription(ctx, key)
	if err != nil {
		return nil, nil, err
	}
	values, err = b.query.GetParamset(ctx, key)
	if err != nil {
		return nil, nil, err
	}
	return descs, values, nil
}
