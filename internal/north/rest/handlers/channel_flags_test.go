// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/channelflags"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// fakeChannelFlagsWriter is an inline stub for ChannelFlagsWriter.
type fakeChannelFlagsWriter struct {
	mu    sync.Mutex
	calls []fakeChannelFlagsSetCall
	err   error
}

type fakeChannelFlagsSetCall struct {
	central        string
	channelAddress string
	hidden         bool
	locked         bool
	updatedBy      string
}

func (f *fakeChannelFlagsWriter) Set(
	_ context.Context, centralName, channelAddress string, hidden, locked bool, updatedBy string,
) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.calls = append(f.calls, fakeChannelFlagsSetCall{centralName, channelAddress, hidden, locked, updatedBy})
	return nil
}

func (f *fakeChannelFlagsWriter) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// newChannelFlagsTestDevice builds a device with a single channel, wired
// with a central name (as the real ingest pipeline would via
// Channel.SetCentralName) so the handler's ch.CentralName() call resolves.
func newChannelFlagsTestDevice(t *testing.T, addr string) *device.Device {
	t.Helper()
	d := newTestDevice(addr, "HmIP-PS")
	chAddr := addr + ":1"
	ch := d.AddChannel(chAddr, 1, "SWITCH", hmenum.ParamsetKeyValues)
	ch.SetCentralName("ccu-01")
	return d
}

// ---------------------------------------------------------------------------
// GetChannelFlags
// ---------------------------------------------------------------------------

func TestGetChannelFlags_HappyPath(t *testing.T) {
	t.Parallel()
	d := newChannelFlagsTestDevice(t, "0001ABCD")
	ch := d.Channel("0001ABCD:1")
	ch.SetOperatorFlags(true, false)
	idx := &stubDeviceIndex{devices: map[string]*device.Device{"0001ABCD": d}}

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "0001ABCD", "no": "1"}))
	w := httptest.NewRecorder()
	GetChannelFlags(idx).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp channelFlagsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.Hidden || resp.Locked {
		t.Errorf("resp = %+v, want hidden=true locked=false", resp)
	}
}

func TestGetChannelFlags_UnknownDevice_Returns404(t *testing.T) {
	t.Parallel()
	idx := &stubDeviceIndex{devices: map[string]*device.Device{}}

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "MISSING", "no": "1"}))
	w := httptest.NewRecorder()
	GetChannelFlags(idx).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestGetChannelFlags_UnknownChannel_Returns404(t *testing.T) {
	t.Parallel()
	d := newChannelFlagsTestDevice(t, "0001ABCD")
	idx := &stubDeviceIndex{devices: map[string]*device.Device{"0001ABCD": d}}

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "0001ABCD", "no": "9"}))
	w := httptest.NewRecorder()
	GetChannelFlags(idx).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// PutChannelFlags
// ---------------------------------------------------------------------------

func TestPutChannelFlags_HiddenOnly_PersistsAndAppliesToLiveChannel(t *testing.T) {
	t.Parallel()
	d := newChannelFlagsTestDevice(t, "0001ABCD")
	ch := d.Channel("0001ABCD:1")
	idx := &stubDeviceIndex{devices: map[string]*device.Device{"0001ABCD": d}}
	writer := &fakeChannelFlagsWriter{}
	overlay := channelflags.New()

	req := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"hidden":true}`))
	req = req.WithContext(chiContext(req, map[string]string{"addr": "0001ABCD", "no": "1"}))
	w := httptest.NewRecorder()
	PutChannelFlags(idx, writer, overlay, nil).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp channelFlagsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.Hidden || resp.Locked {
		t.Errorf("resp = %+v, want hidden=true locked=false (current)", resp)
	}

	if writer.callCount() != 1 {
		t.Fatalf("writer.Set called %d times, want 1", writer.callCount())
	}
	writer.mu.Lock()
	call := writer.calls[0]
	writer.mu.Unlock()
	if call.central != "ccu-01" || call.channelAddress != "0001ABCD:1" || !call.hidden || call.locked {
		t.Errorf("writer.Set call = %+v, want central=ccu-01 addr=0001ABCD:1 hidden=true locked=false", call)
	}

	got := overlay.Get("ccu-01", "0001ABCD:1")
	if !got.Hidden || got.Locked {
		t.Errorf("overlay = %+v, want hidden=true locked=false", got)
	}

	if !ch.IsHidden() || ch.IsLocked() {
		t.Errorf("live channel: IsHidden=%v IsLocked=%v, want hidden=true locked=false", ch.IsHidden(), ch.IsLocked())
	}
}

// TestPutChannelFlags_PartialUpdateKeepsOtherFlag verifies that a PUT with
// only one field set keeps the channel's current value for the absent one.
func TestPutChannelFlags_PartialUpdateKeepsOtherFlag(t *testing.T) {
	t.Parallel()
	d := newChannelFlagsTestDevice(t, "0001ABCD")
	ch := d.Channel("0001ABCD:1")
	ch.SetOperatorFlags(false, true) // pre-existing lock
	idx := &stubDeviceIndex{devices: map[string]*device.Device{"0001ABCD": d}}
	writer := &fakeChannelFlagsWriter{}
	overlay := channelflags.New()

	req := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"hidden":true}`))
	req = req.WithContext(chiContext(req, map[string]string{"addr": "0001ABCD", "no": "1"}))
	w := httptest.NewRecorder()
	PutChannelFlags(idx, writer, overlay, nil).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp channelFlagsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.Hidden || !resp.Locked {
		t.Errorf("resp = %+v, want hidden=true (set) locked=true (kept)", resp)
	}

	writer.mu.Lock()
	call := writer.calls[0]
	writer.mu.Unlock()
	if !call.hidden || !call.locked {
		t.Errorf("writer.Set call = %+v, want hidden=true locked=true (kept)", call)
	}

	if !ch.IsHidden() || !ch.IsLocked() {
		t.Errorf("live channel: IsHidden=%v IsLocked=%v, want both true", ch.IsHidden(), ch.IsLocked())
	}
}

// TestPutChannelFlags_LockedOnlyKeepsHidden mirrors the above from the
// other direction: only "locked" is sent, "hidden" must survive untouched.
func TestPutChannelFlags_LockedOnlyKeepsHidden(t *testing.T) {
	t.Parallel()
	d := newChannelFlagsTestDevice(t, "0001ABCD")
	ch := d.Channel("0001ABCD:1")
	ch.SetOperatorFlags(true, false) // pre-existing hidden
	idx := &stubDeviceIndex{devices: map[string]*device.Device{"0001ABCD": d}}
	writer := &fakeChannelFlagsWriter{}
	overlay := channelflags.New()

	req := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"locked":true}`))
	req = req.WithContext(chiContext(req, map[string]string{"addr": "0001ABCD", "no": "1"}))
	w := httptest.NewRecorder()
	PutChannelFlags(idx, writer, overlay, nil).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp channelFlagsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.Hidden || !resp.Locked {
		t.Errorf("resp = %+v, want hidden=true (kept) locked=true (set)", resp)
	}
	if !ch.IsHidden() || !ch.IsLocked() {
		t.Errorf("live channel: IsHidden=%v IsLocked=%v, want both true", ch.IsHidden(), ch.IsLocked())
	}
}

func TestPutChannelFlags_NilStore_Returns503(t *testing.T) {
	t.Parallel()
	d := newChannelFlagsTestDevice(t, "0001ABCD")
	idx := &stubDeviceIndex{devices: map[string]*device.Device{"0001ABCD": d}}
	overlay := channelflags.New()

	req := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"hidden":true}`))
	req = req.WithContext(chiContext(req, map[string]string{"addr": "0001ABCD", "no": "1"}))
	w := httptest.NewRecorder()
	PutChannelFlags(idx, nil, overlay, nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPutChannelFlags_NilOverlay_Returns503(t *testing.T) {
	t.Parallel()
	d := newChannelFlagsTestDevice(t, "0001ABCD")
	idx := &stubDeviceIndex{devices: map[string]*device.Device{"0001ABCD": d}}
	writer := &fakeChannelFlagsWriter{}

	req := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"hidden":true}`))
	req = req.WithContext(chiContext(req, map[string]string{"addr": "0001ABCD", "no": "1"}))
	w := httptest.NewRecorder()
	PutChannelFlags(idx, writer, nil, nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPutChannelFlags_InvalidJSON_Returns400(t *testing.T) {
	t.Parallel()
	d := newChannelFlagsTestDevice(t, "0001ABCD")
	idx := &stubDeviceIndex{devices: map[string]*device.Device{"0001ABCD": d}}
	writer := &fakeChannelFlagsWriter{}
	overlay := channelflags.New()

	req := httptest.NewRequest(http.MethodPut, "/", strings.NewReader("NOT JSON"))
	req = req.WithContext(chiContext(req, map[string]string{"addr": "0001ABCD", "no": "1"}))
	w := httptest.NewRecorder()
	PutChannelFlags(idx, writer, overlay, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
	if writer.callCount() != 0 {
		t.Errorf("writer.Set must not be called on invalid JSON, got %d calls", writer.callCount())
	}
}

func TestPutChannelFlags_UnknownDevice_Returns404(t *testing.T) {
	t.Parallel()
	idx := &stubDeviceIndex{devices: map[string]*device.Device{}}
	writer := &fakeChannelFlagsWriter{}
	overlay := channelflags.New()

	req := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"hidden":true}`))
	req = req.WithContext(chiContext(req, map[string]string{"addr": "MISSING", "no": "1"}))
	w := httptest.NewRecorder()
	PutChannelFlags(idx, writer, overlay, nil).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", w.Code, w.Body.String())
	}
	if writer.callCount() != 0 {
		t.Errorf("writer.Set must not be called when the device is unknown, got %d calls", writer.callCount())
	}
}

func TestPutChannelFlags_UnknownChannel_Returns404(t *testing.T) {
	t.Parallel()
	d := newChannelFlagsTestDevice(t, "0001ABCD")
	idx := &stubDeviceIndex{devices: map[string]*device.Device{"0001ABCD": d}}
	writer := &fakeChannelFlagsWriter{}
	overlay := channelflags.New()

	req := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"hidden":true}`))
	req = req.WithContext(chiContext(req, map[string]string{"addr": "0001ABCD", "no": "9"}))
	w := httptest.NewRecorder()
	PutChannelFlags(idx, writer, overlay, nil).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", w.Code, w.Body.String())
	}
	if writer.callCount() != 0 {
		t.Errorf("writer.Set must not be called when the channel is unknown, got %d calls", writer.callCount())
	}
}
