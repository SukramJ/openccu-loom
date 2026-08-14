// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// ---- fake transport ----

type recorded struct {
	method string
	url    string
	header http.Header
	body   []byte
}

type fakeTransport struct {
	mu        sync.Mutex
	calls     []recorded
	responses []int // status codes to return, cycling through; default 200
}

func (f *fakeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	body, _ := io.ReadAll(req.Body)
	// Copy headers so the original is not mutated after the fact.
	hdr := req.Header.Clone()
	f.mu.Lock()
	f.calls = append(f.calls, recorded{
		method: req.Method,
		url:    req.URL.String(),
		header: hdr,
		body:   body,
	})
	idx := len(f.calls) - 1
	status := 200
	if len(f.responses) > 0 {
		if idx < len(f.responses) {
			status = f.responses[idx]
		} else {
			status = f.responses[len(f.responses)-1]
		}
	}
	f.mu.Unlock()
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(nil),
		Header:     make(http.Header),
	}, nil
}

func (f *fakeTransport) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeTransport) get(i int) recorded {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[i]
}

// ---- wait helper ----

func waitForCount(t *testing.T, ft *fakeTransport, n int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ft.count() >= n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out after %v waiting for %d request(s); got %d", timeout, n, ft.count())
}

// ---- shared helpers ----

var fixedNow = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func fixedClock() time.Time { return fixedNow }

func instantBackoff() []time.Duration {
	return []time.Duration{time.Millisecond, time.Millisecond}
}

func makeCentral(t *testing.T, name string) *central.Unit {
	t.Helper()
	u, err := central.New(central.Config{Name: name})
	if err != nil {
		t.Fatalf("central.New(%q): %v", name, err)
	}
	return u
}

func makeRegistry(t *testing.T, units ...*central.Unit) *central.Registry {
	t.Helper()
	reg := central.NewRegistry()
	for _, u := range units {
		if err := reg.Register(u); err != nil {
			t.Fatalf("reg.Register(%q): %v", u.Name(), err)
		}
	}
	return reg
}

func computeHMAC(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func datapointEvent(interfaceID, channelAddr, parameter string, newVal, oldVal hmtypes.ParamValue) hmevent.DataPointValueChangedEvent {
	return hmevent.DataPointValueChangedEvent{
		Base: hmevent.NewBase(),
		Key: hmtypes.DataPointKey{
			InterfaceID:    interfaceID,
			ChannelAddress: channelAddr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      parameter,
		},
		NewValue: newVal,
		OldValue: oldVal,
	}
}

// ---- tests ----

func TestOutboundSignsDataPointDelivery(t *testing.T) {
	t.Parallel()
	u := makeCentral(t, "ccuA")
	reg := makeRegistry(t, u)
	ft := &fakeTransport{}
	cfg := config.NorthWebhook{
		Enabled: true,
		URL:     "http://hook.test",
		Secret:  "topsecret",
	}
	o := NewOutbound(
		reg, cfg, nil,
		WithHTTPClient(&http.Client{Transport: ft}),
		WithBackoff(instantBackoff()),
		WithClock(fixedClock),
	)

	if err := o.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = o.Stop(context.Background()) })

	events.Publish(u.EventBus, datapointEvent("HmIP-RF", "ABC:1", "STATE",
		hmtypes.BoolValue(true), hmtypes.BoolValue(false)))

	waitForCount(t, ft, 1, 2*time.Second)

	if ft.count() != 1 {
		t.Fatalf("expected 1 POST, got %d", ft.count())
	}
	r := ft.get(0)

	if r.method != http.MethodPost {
		t.Errorf("method = %q, want POST", r.method)
	}
	if got := r.header.Get("X-OpenCCU-Event"); got != string(hmevent.EventTypeDataPointValueChanged) {
		t.Errorf("X-OpenCCU-Event = %q, want %q", got, hmevent.EventTypeDataPointValueChanged)
	}
	if got := r.header.Get("X-OpenCCU-Delivery"); got == "" {
		t.Error("X-OpenCCU-Delivery header absent")
	}
	if got := r.header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	wantSig := computeHMAC("topsecret", r.body)
	if got := r.header.Get("X-OpenCCU-Signature"); got != wantSig {
		t.Errorf("X-OpenCCU-Signature = %q, want %q", got, wantSig)
	}

	var env envelope
	if err := json.Unmarshal(r.body, &env); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if env.Schema != schemaVersion {
		t.Errorf("schema = %q, want %q", env.Schema, schemaVersion)
	}
	if env.Event != string(hmevent.EventTypeDataPointValueChanged) {
		t.Errorf("event = %q, want %q", env.Event, hmevent.EventTypeDataPointValueChanged)
	}
	if env.Central != "ccuA" {
		t.Errorf("central = %q, want ccuA", env.Central)
	}
	if env.Parameter != "STATE" {
		t.Errorf("parameter = %q, want STATE", env.Parameter)
	}

	var val bool
	if err := json.Unmarshal(env.Value, &val); err != nil || !val {
		t.Errorf("value = %s, want true", env.Value)
	}
	var prev bool
	if err := json.Unmarshal(env.Previous, &prev); err != nil || prev {
		t.Errorf("previous = %s, want false", env.Previous)
	}
}

func TestOutboundNoSecretOmitsSignatureHeader(t *testing.T) {
	t.Parallel()
	u := makeCentral(t, "ccuA")
	reg := makeRegistry(t, u)
	ft := &fakeTransport{}
	cfg := config.NorthWebhook{
		Enabled: true,
		URL:     "http://hook.test",
		Secret:  "",
	}
	o := NewOutbound(
		reg, cfg, nil,
		WithHTTPClient(&http.Client{Transport: ft}),
		WithBackoff(instantBackoff()),
		WithClock(fixedClock),
	)
	if err := o.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = o.Stop(context.Background()) })

	events.Publish(u.EventBus, datapointEvent("HmIP-RF", "ABC:1", "STATE",
		hmtypes.BoolValue(true), hmtypes.NoneValue()))

	waitForCount(t, ft, 1, 2*time.Second)

	r := ft.get(0)
	if got := r.header.Get("X-OpenCCU-Signature"); got != "" {
		t.Errorf("X-OpenCCU-Signature should be absent without secret, got %q", got)
	}
}

func TestOutboundEventTypeFilterDropsUnwantedTypes(t *testing.T) {
	t.Parallel()
	u := makeCentral(t, "ccuA")
	reg := makeRegistry(t, u)
	ft := &fakeTransport{}
	cfg := config.NorthWebhook{
		Enabled: true,
		URL:     "http://hook.test",
		Events:  []string{string(hmevent.EventTypeDataPointValueChanged)},
	}
	o := NewOutbound(
		reg, cfg, nil,
		WithHTTPClient(&http.Client{Transport: ft}),
		WithBackoff(instantBackoff()),
		WithClock(fixedClock),
	)
	if err := o.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = o.Stop(context.Background()) })

	// Publish a SystemStatus event first, then a datapoint.
	events.Publish(u.EventBus, hmevent.SystemStatusChangedEvent{
		Base:        hmevent.NewBase(),
		CentralName: "ccuA",
		Component:   "central",
		Healthy:     false,
		Reason:      "down",
		InterfaceID: "HmIP-RF",
	})
	events.Publish(u.EventBus, datapointEvent("HmIP-RF", "ABC:1", "STATE",
		hmtypes.BoolValue(true), hmtypes.NoneValue()))

	// Wait for the one allowed delivery.
	waitForCount(t, ft, 1, 2*time.Second)

	// Give a short window to catch any spurious extra delivery.
	time.Sleep(100 * time.Millisecond)

	if ft.count() != 1 {
		t.Fatalf("expected 1 delivery, got %d", ft.count())
	}
	r := ft.get(0)
	if got := r.header.Get("X-OpenCCU-Event"); got != string(hmevent.EventTypeDataPointValueChanged) {
		t.Errorf("X-OpenCCU-Event = %q, want datapoint.value_changed", got)
	}
}

func TestOutboundCentralFilterIsolatesPerCentral(t *testing.T) {
	t.Parallel()
	uA := makeCentral(t, "ccuA")
	uB := makeCentral(t, "ccuB")
	reg := makeRegistry(t, uA, uB)
	ft := &fakeTransport{}
	cfg := config.NorthWebhook{
		Enabled:  true,
		URL:      "http://hook.test",
		Centrals: []string{"ccuA"},
	}
	o := NewOutbound(
		reg, cfg, nil,
		WithHTTPClient(&http.Client{Transport: ft}),
		WithBackoff(instantBackoff()),
		WithClock(fixedClock),
	)
	if err := o.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = o.Stop(context.Background()) })

	// Publish identical events on both buses.
	events.Publish(uB.EventBus, datapointEvent("HmIP-RF", "XYZ:1", "STATE",
		hmtypes.BoolValue(false), hmtypes.NoneValue()))
	events.Publish(uA.EventBus, datapointEvent("HmIP-RF", "XYZ:1", "STATE",
		hmtypes.BoolValue(true), hmtypes.NoneValue()))

	// Expect exactly one delivery (ccuA only).
	waitForCount(t, ft, 1, 2*time.Second)
	time.Sleep(100 * time.Millisecond)

	if ft.count() != 1 {
		t.Fatalf("expected 1 delivery (ccuA only), got %d", ft.count())
	}

	var env envelope
	if err := json.Unmarshal(ft.get(0).body, &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if env.Central != "ccuA" {
		t.Errorf("central = %q, want ccuA", env.Central)
	}
}

func TestOutboundParameterGlobFiltersDataPoints(t *testing.T) {
	t.Parallel()
	u := makeCentral(t, "ccuA")
	reg := makeRegistry(t, u)
	ft := &fakeTransport{}
	cfg := config.NorthWebhook{
		Enabled:       true,
		URL:           "http://hook.test",
		ParameterGlob: "*TEMPERATURE*",
	}
	o := NewOutbound(
		reg, cfg, nil,
		WithHTTPClient(&http.Client{Transport: ft}),
		WithBackoff(instantBackoff()),
		WithClock(fixedClock),
	)
	if err := o.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = o.Stop(context.Background()) })

	// Dropped: no TEMPERATURE in name.
	events.Publish(u.EventBus, datapointEvent("HmIP-RF", "ABC:1", "STATE",
		hmtypes.BoolValue(true), hmtypes.NoneValue()))
	// Delivered: matches glob.
	events.Publish(u.EventBus, datapointEvent("HmIP-RF", "ABC:1", "ACTUAL_TEMPERATURE",
		hmtypes.FloatValue(21.5), hmtypes.NoneValue()))

	waitForCount(t, ft, 1, 2*time.Second)
	time.Sleep(100 * time.Millisecond)

	if ft.count() != 1 {
		t.Fatalf("expected 1 delivery, got %d", ft.count())
	}
	var env envelope
	if err := json.Unmarshal(ft.get(0).body, &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if env.Parameter != "ACTUAL_TEMPERATURE" {
		t.Errorf("parameter = %q, want ACTUAL_TEMPERATURE", env.Parameter)
	}
}

func TestOutboundRetryThenSuccess(t *testing.T) {
	t.Parallel()
	u := makeCentral(t, "ccuA")
	reg := makeRegistry(t, u)
	ft := &fakeTransport{responses: []int{500, 500, 200}}
	cfg := config.NorthWebhook{
		Enabled: true,
		URL:     "http://hook.test",
	}
	o := NewOutbound(
		reg, cfg, nil,
		WithHTTPClient(&http.Client{Transport: ft}),
		WithBackoff(instantBackoff()),
		WithClock(fixedClock),
	)
	if err := o.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = o.Stop(context.Background()) })

	events.Publish(u.EventBus, datapointEvent("HmIP-RF", "ABC:1", "STATE",
		hmtypes.BoolValue(true), hmtypes.NoneValue()))

	// Expect 3 transport calls: 2 failures + 1 success.
	waitForCount(t, ft, 3, 2*time.Second)
	time.Sleep(50 * time.Millisecond)

	if ft.count() != 3 {
		t.Fatalf("expected 3 transport calls, got %d", ft.count())
	}
	if o.Failed() != 0 {
		t.Errorf("Failed() = %d, want 0 (third attempt succeeded)", o.Failed())
	}
}

func TestOutboundExhaustedRetriesIncrementFailed(t *testing.T) {
	t.Parallel()
	u := makeCentral(t, "ccuA")
	reg := makeRegistry(t, u)
	// Always 500: 2 retries in backoff => 3 total attempts, all fail.
	ft := &fakeTransport{responses: []int{500}}
	cfg := config.NorthWebhook{
		Enabled: true,
		URL:     "http://hook.test",
	}
	o := NewOutbound(
		reg, cfg, nil,
		WithHTTPClient(&http.Client{Transport: ft}),
		WithBackoff(instantBackoff()),
		WithClock(fixedClock),
	)
	if err := o.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = o.Stop(context.Background()) })

	events.Publish(u.EventBus, datapointEvent("HmIP-RF", "ABC:1", "STATE",
		hmtypes.BoolValue(true), hmtypes.NoneValue()))

	// instantBackoff has 2 entries => 3 total attempts.
	waitForCount(t, ft, 3, 2*time.Second)
	time.Sleep(50 * time.Millisecond)

	if ft.count() != 3 {
		t.Fatalf("expected 3 transport calls, got %d", ft.count())
	}
	if o.Failed() != 1 {
		t.Errorf("Failed() = %d, want 1", o.Failed())
	}
}

func TestOutboundStopUnsubscribesAndBlocksNewDeliveries(t *testing.T) {
	t.Parallel()
	u := makeCentral(t, "ccuA")
	reg := makeRegistry(t, u)
	ft := &fakeTransport{}
	cfg := config.NorthWebhook{
		Enabled: true,
		URL:     "http://hook.test",
	}
	o := NewOutbound(
		reg, cfg, nil,
		WithHTTPClient(&http.Client{Transport: ft}),
		WithBackoff(instantBackoff()),
		WithClock(fixedClock),
	)
	if err := o.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Stop must return without hanging.
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer stopCancel()
	stopped := make(chan error, 1)
	go func() { stopped <- o.Stop(stopCtx) }()

	select {
	case err := <-stopped:
		if err != nil {
			t.Errorf("Stop returned error: %v", err)
		}
	case <-stopCtx.Done():
		t.Fatal("Stop did not return within 2s — possible deadlock")
	}

	before := ft.count()

	// Publish after Stop — should never be delivered.
	events.Publish(u.EventBus, datapointEvent("HmIP-RF", "ABC:1", "STATE",
		hmtypes.BoolValue(true), hmtypes.NoneValue()))
	time.Sleep(200 * time.Millisecond)

	if ft.count() != before {
		t.Errorf("got %d POST(s) after Stop, expected no change from %d", ft.count(), before)
	}
}

func TestOutboundDisabledIsNoop(t *testing.T) {
	t.Parallel()
	u := makeCentral(t, "ccuA")
	reg := makeRegistry(t, u)
	ft := &fakeTransport{}
	cfg := config.NorthWebhook{
		Enabled: false,
		URL:     "http://hook.test",
	}
	o := NewOutbound(
		reg, cfg, nil,
		WithHTTPClient(&http.Client{Transport: ft}),
		WithBackoff(instantBackoff()),
		WithClock(fixedClock),
	)
	if err := o.Start(context.Background()); err != nil {
		t.Fatalf("Start returned error for disabled bridge: %v", err)
	}
	t.Cleanup(func() { _ = o.Stop(context.Background()) })

	events.Publish(u.EventBus, datapointEvent("HmIP-RF", "ABC:1", "STATE",
		hmtypes.BoolValue(true), hmtypes.NoneValue()))
	time.Sleep(200 * time.Millisecond)

	if ft.count() != 0 {
		t.Errorf("expected 0 POSTs for disabled bridge, got %d", ft.count())
	}
}
