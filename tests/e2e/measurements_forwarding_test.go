// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

//go:build e2e

package e2e

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/tests/e2e/harness"
)

// The measurement forwarder is pinned here through the booted daemon: a
// simulated CCU pushes value changes, the daemon runs its real composition
// root, and the only observation point is the fake InfluxDB write endpoint.
// Nothing constructs the forwarder, the exporter or the override overlay.
//
// Every test boots the CCU not ready and flips it afterwards, the production
// order, so the forwarder is wired while the device model is still empty.

const (
	// measurementWindow is how long a "does not arrive" assertion keeps the
	// simulated CCU emitting. The matching positive assertions use the same
	// window, so a negative is never judged on a shorter wait than the
	// positive that proves a line can arrive in it.
	measurementWindow = 5 * time.Second
	// measurementFlush is the exporter flush cadence the daemon is given.
	measurementFlush = 200 * time.Millisecond
	// measurementChannel is the HmIP-BSM measuring channel (MAINTENANCE-free metering channel :7); it carries the
	// numeric VALUES parameters below.
	measurementChannelNo = 7
	paramPower           = "POWER"
	paramCurrent         = "CURRENT"
	// wireInterface is the canonical interface id the daemon tags samples
	// with: <central>-<interface>.
	wireInterface = harness.CentralName + "-HmIP-RF"
)

// fakeInflux records every write the daemon sends to its InfluxDB target.
type fakeInflux struct {
	srv *httptest.Server

	mu    sync.Mutex
	lines []string
	auth  []string
	paths []string
}

func startFakeInflux(t *testing.T) *fakeInflux {
	t.Helper()
	f := &fakeInflux{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.auth = append(f.auth, r.Header.Get("Authorization"))
		f.paths = append(f.paths, r.URL.Path)
		for _, l := range strings.Split(string(body), "\n") {
			if strings.TrimSpace(l) != "" {
				f.lines = append(f.lines, l)
			}
		}
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeInflux) snapshot() (lines, auth []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.lines), slices.Clone(f.auth)
}

// parsedLine is one line-protocol sample: tags plus the field value.
type parsedLine struct {
	central, iface, channel, parameter string
	value                              float64
}

// parseLine reads `<m>,central=..,interface_id=..,channel=..,parameter=.. value=<v> <ts>`.
func parseLine(t *testing.T, l string) parsedLine {
	t.Helper()
	head, rest, ok := strings.Cut(l, " ")
	if !ok {
		t.Fatalf("malformed line-protocol line %q", l)
	}
	var p parsedLine
	for _, kv := range strings.Split(head, ",")[1:] {
		k, v, _ := strings.Cut(kv, "=")
		switch k {
		case "central":
			p.central = v
		case "interface_id":
			p.iface = v
		case "channel":
			p.channel = v
		case "parameter":
			p.parameter = v
		}
	}
	field, _, _ := strings.Cut(rest, " ")
	val, found := strings.CutPrefix(field, "value=")
	if !found {
		t.Fatalf("line %q carries no value field", l)
	}
	f, err := strconv.ParseFloat(val, 64)
	if err != nil {
		t.Fatalf("line %q: bad value: %v", l, err)
	}
	p.value = f
	return p
}

func (f *fakeInflux) samples(t *testing.T) []parsedLine {
	t.Helper()
	lines, _ := f.snapshot()
	out := make([]parsedLine, 0, len(lines))
	for _, l := range lines {
		out = append(out, parseLine(t, l))
	}
	return out
}

// forwardingRig is a booted daemon whose CCU is ready and whose measuring
// channel is known, together with the fake target.
type forwardingRig struct {
	h       *harness.Harness
	target  *fakeInflux
	channel string
	next    float64
}

// bootForwardingRig boots the daemon with the CCU not ready, flips it ready,
// and waits until the device model holds the HmIP-BSM. m may be nil.
func bootForwardingRig(t *testing.T, m *harness.Measurements) *forwardingRig {
	t.Helper()
	rig := &forwardingRig{next: 1000}
	opts := harness.Options{StartCCUNotReady: true}
	if m != nil {
		rig.target = startFakeInflux(t)
		cp := *m
		cp.Endpoint = rig.target.srv.URL
		if cp.FlushInterval == 0 {
			cp.FlushInterval = measurementFlush
		}
		envName := "LOOM_E2E_INFLUX_TOKEN_" + strings.ToUpper(strings.NewReplacer("/", "_", "-", "_").Replace(t.Name()))
		cp.TokenEnv = envName
		opts.Measurements = &cp
		opts.Env = []string{envName + "=" + e2eInfluxToken}
	} else {
		rig.target = startFakeInflux(t)
	}
	rig.h = harness.Start(t, opts)
	if err := rig.h.REST().LoginSession(harness.AdminUser, harness.AdminPass); err != nil {
		t.Fatalf("login: %v", err)
	}
	if items := getJSONArrayOnce(t, rig.h, "/api/v1/devices", "items"); len(items) != 0 {
		t.Fatalf("%d devices before the CCU is ready; the not-ready boot did not hold", len(items))
	}
	rig.h.CCU().V().SetReady(true)
	awaitCentralReady(t, rig.h, harness.CentralName)

	for _, raw := range getJSONArray(t, rig.h, "/api/v1/devices", "items") {
		dev, _ := raw.(map[string]any)
		if model, _ := dev["model"].(string); strings.EqualFold(model, "HmIP-BSM") {
			addr, _ := dev["address"].(string)
			rig.channel = fmt.Sprintf("%s:%d", addr, measurementChannelNo)
		}
	}
	if rig.channel == "" {
		t.Fatal("no HmIP-BSM in the device list after the CCU became ready")
	}
	return rig
}

const e2eInfluxToken = "e2e-write-token"

// emit pushes one new live value for each parameter and returns the value.
func (r *forwardingRig) emit(t *testing.T, params ...string) float64 {
	t.Helper()
	r.next++
	for _, p := range params {
		if err := r.h.CCU().V().SimulateDeviceEvent(r.channel, p, r.next); err != nil {
			t.Fatalf("SimulateDeviceEvent %s %s: %v", r.channel, p, err)
		}
	}
	return r.next
}

// emitFor keeps pushing fresh values for the whole window and returns the
// first value of the window, so callers can tell post-window samples apart.
func (r *forwardingRig) emitFor(t *testing.T, window time.Duration, params ...string) (first float64) {
	t.Helper()
	deadline := time.Now().Add(window)
	first = r.next + 1
	for time.Now().Before(deadline) {
		r.emit(t, params...)
		time.Sleep(300 * time.Millisecond)
	}
	time.Sleep(4 * measurementFlush) // let the last batch flush
	return first
}

// seen returns the parameters of the measuring channel that arrived with a
// value >= from.
func (r *forwardingRig) seen(t *testing.T, from float64) []string {
	t.Helper()
	var out []string
	for _, s := range r.target.samples(t) {
		if s.channel == r.channel && s.value >= from && !slices.Contains(out, s.parameter) {
			out = append(out, s.parameter)
		}
	}
	slices.Sort(out)
	return out
}

func TestE2EMeasurementReachesTheTargetThroughTheBootedDaemon(t *testing.T) {
	t.Parallel()
	rig := bootForwardingRig(t, &harness.Measurements{Enabled: true, Org: "e2e-org", Bucket: "e2e-bucket"})

	// Provenance: the daemon has booted and loaded its values from the CCU;
	// none of that is a measurement. Wait several flush intervals first.
	time.Sleep(5 * measurementFlush)
	if lines, _ := rig.target.snapshot(); len(lines) != 0 {
		t.Fatalf("%d lines reached the target before any value changed (first: %q); boot-time values must not be forwarded", len(lines), lines[0])
	}

	first := rig.emitFor(t, measurementWindow, paramPower)
	var got *parsedLine
	for _, s := range rig.target.samples(t) {
		if s.channel == rig.channel && s.parameter == paramPower && s.value >= first {
			s := s
			got = &s
			break
		}
	}
	if got == nil {
		lines, _ := rig.target.snapshot()
		t.Fatalf("no line for %s %s reached the target within %s (received %d lines)", rig.channel, paramPower, measurementWindow, len(lines))
	}
	if got.central != harness.CentralName {
		t.Errorf("central tag = %q, want %q", got.central, harness.CentralName)
	}
	if got.iface != wireInterface {
		t.Errorf("interface_id tag = %q, want %q", got.iface, wireInterface)
	}
	_, auth := rig.target.snapshot()
	for _, a := range auth {
		if a != "Token "+e2eInfluxToken {
			t.Errorf("Authorization = %q, want the token from the configured env var", a)
		}
	}
	rig.target.mu.Lock()
	for _, p := range rig.target.paths {
		if p != "/api/v2/write" {
			t.Errorf("write path = %q, want /api/v2/write", p)
		}
	}
	rig.target.mu.Unlock()

	if !slices.Contains(capabilitiesOf(t, rig.h), "measurements.v1") {
		t.Errorf("capabilities %v lack measurements.v1 while forwarding is enabled", capabilitiesOf(t, rig.h))
	}
}

func TestE2EMeasurementForwardingOffSendsNothing(t *testing.T) {
	t.Parallel()
	// Control first: the same daemon, same change, same window, enabled.
	// Without it a silent target would prove nothing about the window.
	t.Run("control enabled", func(t *testing.T) {
		t.Parallel()
		rig := bootForwardingRig(t, &harness.Measurements{Enabled: true})
		first := rig.emitFor(t, measurementWindow, paramPower)
		if got := rig.seen(t, first); !slices.Equal(got, []string{paramPower}) {
			t.Fatalf("control: arrived %v, want [%s]; the window is too short to prove silence", got, paramPower)
		}
	})
	t.Run("disabled", func(t *testing.T) {
		t.Parallel()
		// The block is present and fully targeted, but not enabled.
		rig := bootForwardingRig(t, &harness.Measurements{Enabled: false})
		first := rig.emitFor(t, measurementWindow, paramPower)
		if got := rig.seen(t, first); len(got) != 0 {
			t.Fatalf("forwarding is off but %v reached the target", got)
		}
		if lines, _ := rig.target.snapshot(); len(lines) != 0 {
			t.Fatalf("forwarding is off but %d lines reached the target", len(lines))
		}
		if slices.Contains(capabilitiesOf(t, rig.h), "measurements.v1") {
			t.Errorf("capabilities %v advertise measurements.v1 while forwarding is off", capabilitiesOf(t, rig.h))
		}
	})
}

func TestE2EMeasurementIncludeAndExcludeDecideWhatIsForwarded(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		include []string
		exclude []string
		want    []string
	}{
		{"include both, exclude one", []string{paramPower, paramCurrent}, []string{paramCurrent}, []string{paramPower}},
		{"include names only one", []string{paramCurrent}, nil, []string{paramCurrent}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rig := bootForwardingRig(t, &harness.Measurements{Enabled: true, Include: tc.include, Exclude: tc.exclude})
			first := rig.emitFor(t, measurementWindow, paramPower, paramCurrent)
			if got := rig.seen(t, first); !slices.Equal(got, tc.want) {
				t.Fatalf("parameters forwarded = %v, want %v (include=%v exclude=%v)", got, tc.want, tc.include, tc.exclude)
			}
		})
	}
}

func TestE2EMeasurementDisabledCentralIsNotForwarded(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		disabled []string
		want     []string
	}{
		{"control, central not listed", nil, []string{paramPower}},
		{"central listed", []string{harness.CentralName}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rig := bootForwardingRig(t, &harness.Measurements{Enabled: true, DisabledCentrals: tc.disabled})
			first := rig.emitFor(t, measurementWindow, paramPower)
			if got := rig.seen(t, first); !slices.Equal(got, tc.want) {
				t.Fatalf("parameters forwarded = %v, want %v (disabled_centrals=%v)", got, tc.want, tc.disabled)
			}
		})
	}
}

func TestE2EMeasurementRecordingOverrideWinsOverThePolicy(t *testing.T) {
	t.Parallel()
	rig := bootForwardingRig(t, &harness.Measurements{Enabled: true, Exclude: []string{paramPower}})

	// Policy excludes POWER; the window is judged against the override
	// phase below, which uses the same window length.
	first := rig.emitFor(t, measurementWindow, paramPower)
	if got := rig.seen(t, first); len(got) != 0 {
		t.Fatalf("excluded parameter forwarded without an override: %v", got)
	}

	rig.putRecording(t, http.StatusOK, boolPtr(true))
	first = rig.emitFor(t, measurementWindow, paramPower)
	if got := rig.seen(t, first); !slices.Equal(got, []string{paramPower}) {
		t.Fatalf("record=true override: forwarded %v, want [%s]", got, paramPower)
	}

	rig.putRecording(t, http.StatusOK, nil)
	first = rig.emitFor(t, measurementWindow, paramPower)
	if got := rig.seen(t, first); len(got) != 0 {
		t.Fatalf("override cleared (record=null) but %v is still forwarded", got)
	}
}

func TestE2EMeasurementRecordingRouteIsAbsentWhenForwardingIsOff(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		m    *harness.Measurements
		want int
	}{
		{"forwarding on", &harness.Measurements{Enabled: true}, http.StatusOK},
		{"forwarding off", nil, http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rig := bootForwardingRig(t, tc.m)
			path := fmt.Sprintf("/api/v1/measurements/recording?central=%s&interface_id=%s&channel=%s&parameter=%s",
				harness.CentralName, wireInterface, rig.channel, paramPower)
			req, err := rig.h.REST().NewRequest(http.MethodGet, path, nil)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := rig.h.REST().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != tc.want {
				body, _ := io.ReadAll(resp.Body)
				t.Fatalf("GET /measurements/recording = %d, want %d (body %s)", resp.StatusCode, tc.want, body)
			}
		})
	}
}

func boolPtr(b bool) *bool { return &b }

// putRecording PUTs the override for POWER on the measuring channel; nil
// clears it.
func (r *forwardingRig) putRecording(t *testing.T, wantStatus int, record *bool) {
	t.Helper()
	rec := "null"
	if record != nil {
		rec = strconv.FormatBool(*record)
	}
	body := fmt.Sprintf(`{"central":%q,"interface_id":%q,"channel":%q,"parameter":%q,"record":%s}`,
		harness.CentralName, wireInterface, r.channel, paramPower, rec)
	req, err := r.h.REST().NewRequest(http.MethodPut, "/api/v1/measurements/recording", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.h.REST().Do(req)
	if err != nil {
		t.Fatalf("PUT recording: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != wantStatus {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("PUT /measurements/recording = %d, want %d (body %s)", resp.StatusCode, wantStatus, b)
	}
}

func capabilitiesOf(t *testing.T, h *harness.Harness) []string {
	t.Helper()
	raw, _ := fetchInfo(t, h)["capabilities"].([]any)
	out := make([]string, 0, len(raw))
	for _, c := range raw {
		if s, ok := c.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
