// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package litefake

import (
	"context"
	_ "embed" // the bundled metadata fixture
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"time"

	"github.com/SukramJ/godevccu/pkg/godevccu"
)

// Defaults for [Options]. The stream figures are the ones the box
// advertises in /api/meta/v1/version.
const (
	DefaultHeartbeatInterval = 15 * time.Second
	DefaultStreamsPerSubject = 2
	DefaultStreamsTotal      = 16
	DefaultReaderQueue       = 1024
	DefaultRingEvents        = 5000
	DefaultRingAge           = 5 * time.Minute
	DefaultSerial            = "LITEFAKE000001"
	DefaultHostname          = "openccu-lite-fake"
)

// DefaultInterfaces is the interface set of the shipped openccu-lite
// template. BidCos-Wired appears on a real box only where hs485d runs.
func DefaultInterfaces() []string {
	return []string{godevccu.InterfaceBidCosRF, godevccu.InterfaceHmIPRF, godevccu.InterfaceVirtualDevices}
}

// DefaultDevices is a small godevccu fleet with at least one device per
// default interface: a BidCos-RF switch actuator, a HomeMatic IP
// dimmer and a heating group on VirtualDevices.
func DefaultDevices() []string {
	return []string{"HM-LC-Sw1-Pl", "HmIP-BSM", "HM-CC-VG-1"}
}

// DefaultStateDatapoints is the datapoint set /api/rpc/v1/state keeps
// when [Options.StateDatapoints] is nil: the names the contract lists
// explicitly. Every ERROR_* datapoint is kept in addition. The box's
// list is longer (the contract elides part of it), so a test that needs
// another datapoint names it in the option.
func DefaultStateDatapoints() []string {
	return []string{
		"STATE", "LEVEL", "LEVEL_2", "POWER", "ENERGY_COUNTER",
		"UNREACH", "STICKY_UNREACH", "LOWBAT", "CONFIG_PENDING",
		"UPDATE_PENDING", "SABOTAGE", "DUTY_CYCLE",
	}
}

// Options configures [Start]. The zero value starts the default
// interfaces with the default fleet, one wildcard token and the box's
// stream figures.
type Options struct {
	// Interfaces are the interface processes to simulate, by their
	// InterfacesList name. Nil means [DefaultInterfaces].
	Interfaces []string
	// Devices are godevccu device types to load. Nil means
	// [DefaultDevices].
	Devices []string
	// Tokens maps API token secrets to their stored scopes. Nil means
	// {[DefaultToken]: ["*"]}.
	Tokens map[string][]string
	// StartNotReady boots the fake in the state where the box's web
	// server answers but occulited does not yet.
	StartNotReady bool
	// HeartbeatInterval is the SSE heartbeat period. Zero means
	// [DefaultHeartbeatInterval]; tests usually set a few hundred ms.
	HeartbeatInterval time.Duration
	// StreamsPerSubject and StreamsTotal are the event stream limits.
	StreamsPerSubject int
	StreamsTotal      int
	// ReaderQueue is the per-stream queue depth before messages drop.
	ReaderQueue int
	// RingEvents and RingAge bound the replay ring.
	RingEvents int
	RingAge    time.Duration
	// StateDatapoints is the datapoint set /api/rpc/v1/state keeps. Nil
	// means [DefaultStateDatapoints].
	StateDatapoints []string
	// Meta is the initial metadata store, taken over without an event
	// (its revision becomes the store's). Nil starts an empty store at
	// revision 0; [DefaultMeta] is the bundled fixture.
	Meta *Document
	// MetaHeartbeatInterval is the change-stream heartbeat period; zero
	// means [DefaultMetaHeartbeatInterval].
	MetaHeartbeatInterval time.Duration
	// MetaQueue is the per-stream queue depth of the change stream and
	// MetaLog the number of events kept for ?since= replay.
	MetaQueue int
	MetaLog   int
	// Accounts are the user accounts that can log in.
	Accounts []Account
	// PairingDisabled switches client pairing off (403 pairing-off).
	PairingDisabled bool
	// PairingLifetime is how long a request stays pending, PairingKeep
	// how much longer it answers polls, PairingPollInterval the fastest
	// poll rate without wait.
	PairingLifetime     time.Duration
	PairingKeep         time.Duration
	PairingPollInterval time.Duration
	// Serial and Hostname feed the UPnP description.
	Serial   string
	Hostname string
	// Logger receives the simulator's and the fake's logs. Nil discards.
	Logger *slog.Logger
}

// Call is one API request the fake received.
type Call struct {
	At       time.Time
	Method   string
	Path     string
	RawQuery string
	Status   int
	// Subject is "token:<name>" or "session:<user>" when the request
	// carried a valid credential.
	Subject string
	// Body is the request body as the handler read it, capped at 64 KiB.
	Body []byte
	// RPCMethods lists the XML-RPC method names of a proxy request, the
	// inner calls of a system.multicall included.
	RPCMethods []string
}

// ifaceState is the fake's view of one interface process.
type ifaceState struct {
	name       string
	daemonURL  string
	down       bool
	registered bool
	events     uint64
	calls      uint64
}

// Fake is a running fake openccu-lite box.
type Fake struct {
	opts    Options
	logger  *slog.Logger
	v       *godevccu.VirtualCCU
	srv     *httptest.Server
	cb      *httptest.Server
	start   time.Time
	ring    *ring
	values  *valueStore
	meta    *metaStore
	pairing *pairingState
	system  *systemState

	mu            sync.Mutex
	ready         bool
	tokens        map[string]tokenEntry
	accounts      map[string]Account
	sessions      map[string]*session
	heartbeat     time.Duration
	metaHeartbeat time.Duration
	ifaces        map[string]*ifaceState
	calls         []Call

	done      chan struct{}
	closeOnce sync.Once
}

// Start brings up a fake box: the godevccu interface processes, the
// subscriber callback endpoint, the subscriber's init on every
// interface, and the LAN-side HTTP server. ctx bounds the start-up
// calls only; the fake runs until [Fake.Close].
func Start(ctx context.Context, opts Options) (*Fake, error) {
	opts = withDefaults(opts)
	logger := opts.Logger
	ports := make(map[string]int, len(opts.Interfaces))
	for _, name := range opts.Interfaces {
		ports[name] = godevccu.EphemeralPort
	}
	v, err := godevccu.New(godevccu.Config{
		Mode:           godevccu.BackendModeOpenCCU,
		Host:           "127.0.0.1",
		XMLRPCPort:     godevccu.EphemeralPort,
		JSONRPCPort:    godevccu.EphemeralPort,
		Devices:        opts.Devices,
		InterfacePorts: ports,
		Serial:         opts.Serial,
		Logger:         logger,
	})
	if err != nil {
		return nil, fmt.Errorf("litefake: godevccu: %w", err)
	}
	if err := v.Start(); err != nil {
		return nil, fmt.Errorf("litefake: godevccu start: %w", err)
	}

	f := &Fake{
		opts:      opts,
		logger:    logger,
		v:         v,
		start:     time.Now(),
		ring:      newRing(opts.RingEvents, opts.RingAge, opts.ReaderQueue, opts.StreamsPerSubject, opts.StreamsTotal),
		values:    newValueStore(opts.StateDatapoints),
		ready:     !opts.StartNotReady,
		heartbeat: opts.HeartbeatInterval,
		ifaces:    make(map[string]*ifaceState, len(opts.Interfaces)),
		done:      make(chan struct{}),
	}
	f.meta = newMetaStore(opts.Meta, opts.MetaLog, opts.MetaQueue)
	f.pairing = newPairingState(opts)
	f.system = newSystemState()
	f.metaHeartbeat = opts.MetaHeartbeatInterval
	f.sessions = map[string]*session{}
	f.setTokens(opts.Tokens)
	f.SetAccounts(opts.Accounts)
	for _, name := range opts.Interfaces {
		addr, ok := v.InterfaceAddr(name).(*net.TCPAddr)
		if !ok || addr == nil {
			_ = v.Stop()
			return nil, fmt.Errorf("litefake: interface %s has no listener", name)
		}
		path := "/"
		if name == godevccu.InterfaceVirtualDevices {
			// The group interface answers under /groups on a CCU, and
			// the box keeps the daemon URL's path when it forwards.
			path = "/groups"
		}
		f.ifaces[name] = &ifaceState{name: name, daemonURL: "http://" + addr.String() + path}
	}

	f.cb = httptest.NewServer(f.callbackHandler())
	f.srv = httptest.NewServer(f.routes())

	for _, name := range opts.Interfaces {
		if err := f.subscribe(ctx, name); err != nil {
			_ = f.Close()
			return nil, err
		}
	}
	return f, nil
}

// withDefaults fills every zero option.
func withDefaults(o Options) Options {
	if o.Interfaces == nil {
		o.Interfaces = DefaultInterfaces()
	}
	if o.Devices == nil {
		o.Devices = DefaultDevices()
	}
	if o.Tokens == nil {
		o.Tokens = map[string][]string{DefaultToken: {scopeAll}}
	}
	if o.HeartbeatInterval <= 0 {
		o.HeartbeatInterval = DefaultHeartbeatInterval
	}
	if o.StreamsPerSubject <= 0 {
		o.StreamsPerSubject = DefaultStreamsPerSubject
	}
	if o.StreamsTotal <= 0 {
		o.StreamsTotal = DefaultStreamsTotal
	}
	if o.ReaderQueue <= 0 {
		o.ReaderQueue = DefaultReaderQueue
	}
	if o.RingEvents <= 0 {
		o.RingEvents = DefaultRingEvents
	}
	if o.RingAge <= 0 {
		o.RingAge = DefaultRingAge
	}
	if o.StateDatapoints == nil {
		o.StateDatapoints = DefaultStateDatapoints()
	}
	if o.MetaHeartbeatInterval <= 0 {
		o.MetaHeartbeatInterval = DefaultMetaHeartbeatInterval
	}
	if o.MetaQueue <= 0 {
		o.MetaQueue = DefaultMetaQueue
	}
	if o.MetaLog <= 0 {
		o.MetaLog = DefaultMetaLog
	}
	if o.PairingLifetime <= 0 {
		o.PairingLifetime = DefaultPairingLifetime
	}
	if o.PairingKeep <= 0 {
		o.PairingKeep = DefaultPairingKeep
	}
	if o.PairingPollInterval <= 0 {
		o.PairingPollInterval = DefaultPairingPollInterval
	}
	if o.Serial == "" {
		o.Serial = DefaultSerial
	}
	if o.Hostname == "" {
		o.Hostname = DefaultHostname
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	return o
}

// Close stops every stream, both HTTP servers and the simulator. Safe to
// call more than once.
func (f *Fake) Close() error {
	var err error
	f.closeOnce.Do(func() {
		close(f.done)
		f.ring.dropStreams()
		f.meta.dropStreams()
		if f.srv != nil {
			f.srv.Close()
		}
		err = f.v.Stop()
		if f.cb != nil {
			f.cb.Close()
		}
	})
	return err
}

// URL is the box's base URL, e.g. "http://127.0.0.1:41234".
func (f *Fake) URL() string { return f.srv.URL }

// Client returns an HTTP client for the fake's server.
func (f *Fake) Client() *http.Client { return f.srv.Client() }

// V returns the godevccu instance behind the interface processes, so a
// test can make a device report a value (InterfaceRPC(name) targets one
// interface process).
func (f *Fake) V() *godevccu.VirtualCCU { return f.v }

// BootID returns the current boot id, which changes on [Fake.RestartBoot].
func (f *Fake) BootID() string {
	boot, _ := f.ring.position()
	return boot
}

// Calls returns a copy of the API requests received so far.
func (f *Fake) Calls() []Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Call(nil), f.calls...)
}

// interfaceNames returns the configured interfaces sorted by name, the
// order /api/rpc/v1/interfaces and hello list them in.
func (f *Fake) interfaceNames() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	names := make([]string, 0, len(f.ifaces))
	for n := range f.ifaces {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// ifaceSnapshot returns a copy of one interface's state.
func (f *Fake) ifaceSnapshot(name string) (ifaceState, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	st, ok := f.ifaces[name]
	if !ok {
		return ifaceState{}, false
	}
	return *st, true
}

// routes builds the LAN-side handler: request recording around the
// readiness gate around the route table.
func (f *Fake) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/rpc/v1/interfaces", f.handleInterfaces)
	mux.HandleFunc("/api/rpc/v1/xmlrpc/{iface}", f.handleXMLRPC)
	mux.HandleFunc("GET /api/rpc/v1/events", f.handleEvents)
	mux.HandleFunc("GET /api/rpc/v1/state", f.handleState)
	mux.HandleFunc("GET /api/auth/v1/state", f.handleAuthState)
	mux.HandleFunc("GET /api/system/v1/health", f.handleHealth)
	mux.HandleFunc("GET /api/meta/v1/version", f.handleMetaVersion)
	mux.HandleFunc("POST /api/auth/v1/login", f.handleLogin)
	mux.HandleFunc("POST /api/auth/v1/logout", f.handleLogout)
	f.metaRoutes(mux)
	f.pairingRoutes(mux)
	f.systemRoutes(mux)
	mux.HandleFunc("GET /upnp/basic_dev.cgi", f.handleUPnP)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "not-found", "no such endpoint")
	})
	mux.HandleFunc("/", f.handleShell)
	return f.record(f.readinessGate(lengthGate(mux)))
}

// readinessGate answers for the web server in front of occulited while
// occulited is not answering: every /api/* request gets the JSON
// "starting" error with Retry-After, everything else a bare 503.
func (f *Fake) readinessGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		ready := f.ready
		f.mu.Unlock()
		if ready {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Retry-After", "5")
		if isAPIPath(r.URL.Path) {
			writeError(w, http.StatusServiceUnavailable, "starting", "occulited is not answering yet")
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("occulited is not answering yet\n"))
	})
}

// lengthGate answers 411 for an /api/ request with a method that
// carries a body but no Content-Length, as the web server in front of
// occulited does; clients send "{}" when they have nothing to send.
func lengthGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
			if isAPIPath(r.URL.Path) && (r.ContentLength < 0 || r.Header.Get("Content-Length") == "") {
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				w.WriteHeader(http.StatusLengthRequired)
				_, _ = w.Write([]byte("411 Length Required\n"))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// isAPIPath reports whether path belongs to the box's /api/ tree.
func isAPIPath(path string) bool {
	return path == "/api" || len(path) >= 5 && path[:5] == "/api/"
}

// callRecord is the per-request slot handlers fill in for [Call].
type callRecord struct {
	subject    string
	rpcMethods []string
}

type callRecordKey struct{}

// recordSubject notes the authenticated subject of the current request.
func recordSubject(r *http.Request, subject string) {
	if rec, ok := r.Context().Value(callRecordKey{}).(*callRecord); ok {
		rec.subject = subject
	}
}

// recordRPCMethods notes the XML-RPC methods of the current request.
func recordRPCMethods(r *http.Request, methods []string) {
	if rec, ok := r.Context().Value(callRecordKey{}).(*callRecord); ok {
		rec.rpcMethods = methods
	}
}

// statusWriter captures the status code; it forwards Flush so the SSE
// handler can stream through it. It deliberately defines no Write of its
// own: a body written without WriteHeader is a 200 (see [statusWriter.code]),
// and a concrete Write here would make every response body of the daemon's
// own handlers look, to a whole-program taint analysis resolving
// ResponseWriter.Write, as if it flowed through this test helper.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

// code is the status the handler answered with; net/http sends 200 when a
// handler writes a body (or nothing) without calling WriteHeader.
func (s *statusWriter) code() int {
	if s.status == 0 {
		return http.StatusOK
	}
	return s.status
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// record appends every /api/ request to the call log once it finished.
// An event stream is logged when it ends.
func (f *Fake) record(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &callRecord{}
		sw := &statusWriter{ResponseWriter: w}
		body := &cappedBuffer{limit: 64 << 10}
		if r.Body != nil {
			r.Body = teeReadCloser{Reader: io.TeeReader(r.Body, body), Closer: r.Body}
		}
		r = r.WithContext(context.WithValue(r.Context(), callRecordKey{}, rec))
		next.ServeHTTP(sw, r)
		if !isAPIPath(r.URL.Path) {
			return
		}
		f.mu.Lock()
		f.calls = append(f.calls, Call{
			At:         time.Now(),
			Method:     r.Method,
			Path:       r.URL.Path,
			RawQuery:   r.URL.RawQuery,
			Status:     sw.code(),
			Subject:    rec.subject,
			RPCMethods: rec.rpcMethods,
			Body:       body.bytes(),
		})
		f.mu.Unlock()
	})
}

// cappedBuffer keeps the first limit bytes written to it.
type cappedBuffer struct {
	limit int
	buf   []byte
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	if room := c.limit - len(c.buf); room > 0 {
		c.buf = append(c.buf, p[:min(room, len(p))]...)
	}
	return len(p), nil
}

func (c *cappedBuffer) bytes() []byte {
	if len(c.buf) == 0 {
		return nil
	}
	return append([]byte(nil), c.buf...)
}

// teeReadCloser records a request body while the handler reads it.
type teeReadCloser struct {
	io.Reader
	io.Closer
}

// errorBody is the box's JSON error shape.
type errorBody struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// writeError writes {"error","message"} with status.
func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, errorBody{Error: code, Message: msg})
}

// writeJSON writes body as the box writes every JSON answer. T is any
// fake-owned answer type; the constraint is json.Marshal's own.
func writeJSON[T any](w http.ResponseWriter, status int, body T) {
	raw, err := json.Marshal(body)
	if err != nil {
		status = http.StatusInternalServerError
		raw = []byte(`{"error":"internal","message":"encode failed"}`)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(raw)
}

// healthMeta is the meta part of /api/system/v1/health.
type healthMeta struct {
	Revision  int  `json:"revision"`
	Recovered bool `json:"recovered"`
}

// health is the /api/system/v1/health answer.
type health struct {
	OK      bool       `json:"ok"`
	Version string     `json:"version"`
	Release string     `json:"release"`
	Base    string     `json:"base"`
	UptimeS int64      `json:"uptime_s"`
	Meta    healthMeta `json:"meta"`
}

// Identity strings the fake reports; they follow the shape of the
// box's /VERSION record.
const (
	fakeVersion = "litefake"
	fakeRelease = "1.0.0-dev"
	fakeBase    = "3.89.11.20260919"
)

// handleHealth answers the open GET /api/system/v1/health.
func (f *Fake) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, health{
		OK:      true,
		Version: fakeVersion,
		Release: fakeRelease,
		Base:    fakeBase,
		UptimeS: int64(time.Since(f.start) / time.Second),
	})
}

// metaVersion is the /api/meta/v1/version answer, the document client
// detection classifies a box by.
type metaVersion struct {
	API            string           `json:"api"`
	Version        int              `json:"version"`
	Format         int              `json:"format"`
	Revision       int              `json:"revision"`
	Implementation string           `json:"implementation"`
	Capabilities   metaCapabilities `json:"capabilities"`
}

type metaCapabilities struct {
	Pairing    bool           `json:"pairing"`
	State      bool           `json:"state"`
	History    bool           `json:"history"`
	APIs       map[string]int `json:"apis"`
	Transports []string       `json:"transports"`
	Limits     metaLimits     `json:"limits"`
	JSONDouble bool           `json:"json_double"`
}

type metaLimits struct {
	StreamsPerToken int `json:"streams_per_token"`
	StreamsTotal    int `json:"streams_total"`
	BufferSeconds   int `json:"buffer_seconds"`
	BufferEvents    int `json:"buffer_events"`
}

// handleMetaVersion answers the open GET /api/meta/v1/version. The
// capabilities describe the fake itself: it serves pairing, but no
// history and no WebSocket event transport, so it does not claim them.
// The hmip block is left out; the contract tells a client to treat its
// absence as an older box.
func (f *Fake) handleMetaVersion(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, metaVersion{
		API:            "meta",
		Version:        1,
		Format:         1,
		Implementation: "occulited " + fakeVersion,
		Revision:       f.Meta().Revision(),
		Capabilities: metaCapabilities{
			Pairing:    !f.pairingDisabled(),
			State:      true,
			APIs:       map[string]int{"meta": 1, "rpc": 1, "system": 1, "auth": 1},
			Transports: []string{"sse"},
			Limits: metaLimits{
				StreamsPerToken: f.opts.StreamsPerSubject,
				StreamsTotal:    f.opts.StreamsTotal,
				BufferSeconds:   int(f.opts.RingAge / time.Second),
				BufferEvents:    f.opts.RingEvents,
			},
			JSONDouble: true,
		},
	})
}

// errFakeClosed is returned by knobs used after Close.
var errFakeClosed = errors.New("litefake: fake is closed")

// closed reports whether Close has run.
func (f *Fake) closed() bool {
	select {
	case <-f.done:
		return true
	default:
		return false
	}
}

// metaFixture is the bundled metadata document: a room tree
// (eg/wohnzimmer, eg/kueche, og/kueche), a function enum, and named
// objects for devices and channels of [DefaultDevices].
//
//go:embed testdata/meta.json
var metaFixture []byte

// DefaultMeta returns the bundled metadata fixture as a fresh document.
func DefaultMeta() *Document {
	var doc Document
	if err := json.Unmarshal(metaFixture, &doc); err != nil {
		// The fixture is part of the package; a decode failure is a
		// defect in it, caught by the package's own tests.
		return &Document{Format: 1, Objects: map[string]Object{}, Enums: map[string]Enum{}}
	}
	return &doc
}

// pairingDisabled reports whether pairing is switched off.
func (f *Fake) pairingDisabled() bool {
	f.pairing.mu.Lock()
	defer f.pairing.mu.Unlock()
	return f.pairing.disabled
}
