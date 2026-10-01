// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/client/transport/occulited"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// liteStreamTypes are the lite-rpc stream messages a central consumes. The
// box's own state sweep is left out: it re-publishes values the value seed
// already reconciles and would appear on the bus as changes no device
// reported. hello and resync arrive regardless of the filter.
var liteStreamTypes = []string{
	string(occulited.KindEvent), string(occulited.KindInterface),
	string(occulited.KindNewDevices), string(occulited.KindDeleteDevices),
	string(occulited.KindUpdateDevice), string(occulited.KindReplaceDevice),
	string(occulited.KindReaddedDevice),
}

// liteAnnounceTimeout bounds how long an interface's announcement waits for
// the stream to report the interface up. The bring-up's activate and the
// recovery's reconnect both retry with their own backoff after it.
const liteAnnounceTimeout = 30 * time.Second

// liteStreamURL is the callback URL a lite central's interfaces announce.
// It is never sent anywhere: the lite announcer ignores it. It exists so
// the bring-up and the recovery take their announce branch, which is where
// the client learns that its events flow.
func liteStreamURL(centralName string) string { return "lite-stream://" + centralName }

// liteEventIngress feeds an openccu-lite central's callback handlers from
// the box's event stream. One ingress exists per central profile and
// outlives every bring-up generation, as the callback route does on a CCU.
type liteEventIngress struct {
	profile *liteProfile

	mu     sync.Mutex
	stream *liteStream
}

// Attach implements [EventIngress]: it builds the handlers and starts the
// stream supervisor, which reconnects on its own until detach.
func (in *liteEventIngress) Attach(cc *config.CentralConfig, unit *central.Unit, deps WireDeps, logger *slog.Logger) (
	handlers *CallbackHandlers, callbackURL, binRPCAddr string, detach func(),
) {
	if logger == nil {
		logger = slog.Default()
	}
	handlers = NewCallbackHandlers(unit, logger)
	if deps.Writer != nil {
		handlers.SetWriter(deps.Writer)
	}
	handlers.SetDelayNewDeviceCreation(cc.Behavior.DelayNewDeviceCreationEnabled())
	s := newLiteStream(in.profile, *cc, unit, deps.Writer, handlers, logger)
	in.mu.Lock()
	in.stream = s
	in.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	SafeGo("lite_stream."+cc.Name, func() {
		defer close(done)
		s.run(ctx)
	})
	detach = func() {
		cancel()
		<-done
		s.wg.Wait()
		handlers.Stop()
	}
	return handlers, liteStreamURL(cc.Name), "", detach
}

// BindGeneration implements generationAware: the reconciliation's value
// reseed runs through the current generation's pipeline.
func (in *liteEventIngress) BindGeneration(reseed func(ctx context.Context, iface hmenum.Interface) error) (unbind func()) {
	s := in.current()
	if s == nil {
		return func() {}
	}
	s.setReseed(reseed)
	return func() { s.setReseed(nil) }
}

func (in *liteEventIngress) current() *liteStream {
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.stream
}

// liteStream supervises one central's lite-rpc event stream and dispatches
// what it delivers to the callback handlers, the same calls an XML-RPC
// callback makes, so everything downstream cannot tell the two apart.
type liteStream struct {
	client   *occulited.Client
	cc       config.CentralConfig
	unit     *central.Unit
	writer   *client.ValueWriter
	handlers *CallbackHandlers
	logger   *slog.Logger
	opts     occulited.EventsOptions

	ifaces  []hmenum.Interface
	initIDs map[hmenum.Interface]string // the id the handlers take (InitInterfaceID form)
	wireIDs map[hmenum.Interface]string

	mu        sync.Mutex
	live      bool
	up        map[hmenum.Interface]bool
	changed   chan struct{} // closed and replaced on every live/up change
	reseed    func(ctx context.Context, iface hmenum.Interface) error
	helloSeen bool

	reconcileMu sync.Mutex
	wg          sync.WaitGroup
}

func newLiteStream(p *liteProfile, cc config.CentralConfig, unit *central.Unit, writer *client.ValueWriter, h *CallbackHandlers, logger *slog.Logger) *liteStream {
	s := &liteStream{
		client: p.client, cc: cc, unit: unit, writer: writer, handlers: h, logger: logger,
		initIDs: map[hmenum.Interface]string{}, wireIDs: map[hmenum.Interface]string{},
		up: map[hmenum.Interface]bool{}, changed: make(chan struct{}),
		opts: p.eventsOptions,
	}
	names := make([]string, 0, len(cc.Interfaces))
	for _, spec := range cc.Interfaces {
		iface := hmenum.Interface(strings.TrimSpace(spec.Name))
		if iface == "" {
			continue
		}
		s.ifaces = append(s.ifaces, iface)
		s.initIDs[iface] = InitInterfaceID(unit.InstanceName(), cc.Name, iface)
		s.wireIDs[iface] = WireInterfaceID(cc.Name, iface)
		names = append(names, string(iface))
	}
	s.opts.Interfaces = names
	s.opts.Types = liteStreamTypes
	return s
}

// run consumes the stream until ctx ends.
func (s *liteStream) run(ctx context.Context) {
	stream := s.client.Events(ctx, s.opts)
	for m := range stream.Messages() {
		s.handle(ctx, m)
	}
	s.setLive(false)
}

// handle dispatches one message. Every message and every heartbeat stamps
// the liveness of the interfaces the box reports up; a down interface is
// no longer stamped, so its client goes stale as on a CCU whose interface
// process died.
func (s *liteStream) handle(ctx context.Context, m occulited.Message) {
	switch m.Kind {
	case occulited.KindHello:
		s.onHello(ctx, m.Hello)
	case occulited.KindComment, occulited.KindOpen:
	case occulited.KindEvent:
		s.onEvent(ctx, m.Event)
	case occulited.KindInterface:
		s.onInterface(ctx, m.Interface)
	case occulited.KindNewDevices, occulited.KindDeleteDevices, occulited.KindUpdateDevice,
		occulited.KindReplaceDevice, occulited.KindReaddedDevice:
		s.onDevices(ctx, m.Kind, m.Devices)
	case occulited.KindResync:
		s.logger.Info("lite.stream.resync", slog.String("central", s.cc.Name), slog.String("reason", m.Resync.Reason))
		s.reconcileAll(ctx)
	case occulited.KindClosed:
		s.setLive(false)
		s.logger.Info("lite.stream.dropped", slog.String("central", s.cc.Name),
			slog.String("err", errString(m.Err)), slog.Duration("retry_in", m.RetryIn))
		return
	default:
		s.logger.Debug("lite.stream.unknown", slog.String("central", s.cc.Name), slog.String("type", m.Type))
	}
	s.stamp()
}

// onHello marks the stream live and each configured interface up or down
// from the box's view. A hello after the first one means a reconnect: what
// happened while the stream was down is reconciled, the equivalent of the
// full inventory a CCU pushes after every init.
func (s *liteStream) onHello(ctx context.Context, h *occulited.Hello) {
	if h == nil {
		return
	}
	s.mu.Lock()
	reconnect := s.helloSeen
	s.helloSeen = true
	s.live = true
	for _, iface := range s.ifaces {
		s.up[iface] = false
	}
	for _, hi := range h.Interfaces {
		iface := hmenum.Interface(hi.Name)
		if _, ok := s.initIDs[iface]; ok {
			s.up[iface] = hi.State == "up" || hi.State == "silent"
		}
	}
	s.notifyLocked()
	s.mu.Unlock()
	if reconnect {
		s.reconcileAll(ctx)
	}
}

// onEvent dispatches a value change with its XML-RPC type rebuilt from the
// parameter's description.
func (s *liteStream) onEvent(ctx context.Context, e *occulited.Event) {
	if e == nil {
		return
	}
	iface := hmenum.Interface(e.Interface)
	id, ok := s.initIDs[iface]
	if !ok {
		return
	}
	pd, known := s.unit.ParamsetReg.GetParameterData(hmtypes.ParseWireInterfaceID(s.wireIDs[iface]), e.Address, hmenum.ParamsetKeyValues, e.Key)
	if err := s.handlers.Event(ctx, id, e.Address, e.Key, typedValue(pd, known, e.Value)); err != nil {
		s.logger.Debug("lite.stream.event_failed", slog.String("address", e.Address), slog.String("key", e.Key), slog.String("err", err.Error()))
	}
}

// onInterface follows an interface process's state. A down interface
// publishes the connection loss a CCU outage publishes, so recovery,
// device availability and health react exactly as they would there.
func (s *liteStream) onInterface(ctx context.Context, c *occulited.InterfaceChange) {
	if c == nil {
		return
	}
	iface := hmenum.Interface(c.Interface)
	if _, ok := s.initIDs[iface]; !ok {
		return
	}
	switch c.State {
	case "down", "removed":
		s.setUp(iface, false)
		// The box states it outright, so the client leaves CONNECTED at
		// once — the transition a CCU's probe loop makes after repeated
		// failed pings, and the one forced device unavailability hangs off.
		s.markDisconnected(iface)
		if s.unit.EventBus != nil {
			s.unit.EventBus.Publish(hmevent.ConnectionLostEvent{
				Base:        hmevent.NewBase(),
				CentralName: s.cc.Name,
				InterfaceID: s.wireIDs[iface],
				Reason:      hmenum.FailureReasonNetwork,
			})
		}
	case "up", "added":
		s.setUp(iface, true)
	case "restarted":
		// The daemon lost its volatile state; what it knows now is
		// reconciled.
		s.setUp(iface, true)
		s.reconcile(ctx, iface)
	}
}

// onDevices dispatches the device lifecycle messages. The stream carries
// addresses only; the descriptions of a new device are read through the
// proxy before they are handed to the handlers.
func (s *liteStream) onDevices(ctx context.Context, kind occulited.Kind, d *occulited.DeviceChange) {
	if d == nil || d.IsSnapshot() {
		return
	}
	iface := hmenum.Interface(d.Interface)
	id, ok := s.initIDs[iface]
	if !ok {
		return
	}
	var err error
	switch kind {
	case occulited.KindNewDevices:
		s.wg.Add(1)
		SafeGo("lite_stream.new_devices."+s.cc.Name, func() {
			defer s.wg.Done()
			s.ingestNew(ctx, iface, d.Addresses)
		})
	case occulited.KindDeleteDevices:
		err = s.handlers.DeleteDevices(ctx, id, d.Addresses)
	case occulited.KindUpdateDevice:
		// The box drops the daemon's hint; hint 0 (a description refresh)
		// is the only one with an effect, and a harmless one otherwise.
		for _, addr := range d.Addresses {
			if uerr := s.handlers.UpdateDevice(ctx, id, addr, 0); uerr != nil {
				err = uerr
			}
		}
	case occulited.KindReplaceDevice:
		if len(d.Addresses) == 2 {
			err = s.handlers.ReplaceDevice(ctx, id, d.Addresses[0], d.Addresses[1])
		}
	case occulited.KindReaddedDevice:
		err = s.handlers.ReaddedDevice(ctx, id, d.Addresses)
	default:
		// handle routes only the device lifecycle kinds here.
	}
	if err != nil {
		s.logger.Warn("lite.stream.devices_failed", slog.String("central", s.cc.Name),
			slog.String("type", string(kind)), slog.String("err", err.Error()))
	}
}

// ingestNew reads the descriptions of the announced devices the central
// does not know yet and hands them to the handlers.
func (s *liteStream) ingestNew(ctx context.Context, iface hmenum.Interface, addresses []string) {
	b := s.backend(iface)
	if b == nil {
		return
	}
	wire := hmtypes.ParseWireInterfaceID(s.wireIDs[iface])
	var descs []hmproto.DeviceDescription
	for _, addr := range addresses {
		if strings.Contains(addr, ":") || s.unit.DeviceRegistry.Has(wire, addr) {
			continue
		}
		got, err := (&singleDeviceDescFetcher{ops: b, address: addr}).ListDevices(ctx, wire)
		if err != nil {
			s.logger.Warn("lite.stream.describe_failed", slog.String("address", addr), slog.String("err", err.Error()))
			continue
		}
		descs = append(descs, got...)
	}
	if len(descs) == 0 {
		return
	}
	if err := s.handlers.IngestDescriptions(ctx, s.initIDs[iface], descs); err != nil {
		s.logger.Warn("lite.stream.ingest_failed", slog.String("central", s.cc.Name), slog.String("err", err.Error()))
	}
}

// reconcileAll reconciles every configured interface.
func (s *liteStream) reconcileAll(ctx context.Context) {
	for _, iface := range s.ifaces {
		s.reconcile(ctx, iface)
	}
}

// reconcile brings one interface back in step after the stream may have
// missed messages: the inventory is listed again (known devices are a
// no-op downstream; devices no longer listed are deleted) and the values
// are reseeded. It runs in the background, one reconciliation at a time
// per central, and not before the bring-up registered the interface's
// backend — the bring-up's own ingest covers that window.
func (s *liteStream) reconcile(ctx context.Context, iface hmenum.Interface) {
	s.wg.Add(1)
	SafeGo("lite_stream.reconcile."+s.cc.Name, func() {
		defer s.wg.Done()
		s.reconcileMu.Lock()
		defer s.reconcileMu.Unlock()
		ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		b := s.backend(iface)
		if b == nil {
			return
		}
		descs, err := b.ListDevices(ctx)
		if err != nil {
			s.logger.Warn("lite.reconcile.list_failed", slog.String("interface", string(iface)), slog.String("err", err.Error()))
			return
		}
		id := s.initIDs[iface]
		if err := s.handlers.IngestDescriptions(ctx, id, descs); err != nil {
			s.logger.Warn("lite.reconcile.ingest_failed", slog.String("interface", string(iface)), slog.String("err", err.Error()))
		}
		listed := make(map[string]bool, len(descs))
		for i := range descs {
			listed[descs[i].Address] = true
		}
		var gone []string
		for _, addr := range s.unit.DeviceRegistry.Addresses(hmtypes.ParseWireInterfaceID(s.wireIDs[iface])) {
			if !listed[addr] {
				gone = append(gone, addr)
			}
		}
		if len(gone) > 0 {
			if err := s.handlers.DeleteDevices(ctx, id, gone); err != nil {
				s.logger.Warn("lite.reconcile.delete_failed", slog.String("interface", string(iface)), slog.String("err", err.Error()))
			}
		}
		if reseed := s.currentReseed(); reseed != nil {
			if err := reseed(ctx, iface); err != nil {
				s.logger.Warn("lite.reconcile.reseed_failed", slog.String("interface", string(iface)), slog.String("err", err.Error()))
			}
		}
	})
}

// markDisconnected moves a connected interface client to DISCONNECTED.
func (s *liteStream) markDisconnected(iface hmenum.Interface) {
	if s.unit.Clients == nil {
		return
	}
	entry, ok := s.unit.Clients.Get(s.wireIDs[iface])
	if !ok || entry == nil || entry.Client == nil || entry.Client.ClientState() != hmenum.ClientStateConnected {
		return
	}
	if err := entry.Client.TransitionTo(hmenum.ClientStateDisconnected, "interface down on the box", false, hmenum.FailureReasonNetwork); err != nil {
		s.logger.Debug("lite.stream.disconnect_refused", slog.String("interface", string(iface)), slog.String("err", err.Error()))
	}
}

// backend is the interface's registered backend, or nil before the
// bring-up registered it.
func (s *liteStream) backend(iface hmenum.Interface) backends.Operations {
	if s.writer == nil {
		return nil
	}
	b, ok := s.writer.Backend(s.cc.Name, hmtypes.ParseWireInterfaceID(s.wireIDs[iface]))
	if !ok {
		return nil
	}
	return b
}

// stamp refreshes the callback liveness of every interface the box
// reports up.
func (s *liteStream) stamp() {
	s.mu.Lock()
	var ids []string
	if s.live {
		for iface, up := range s.up {
			if up {
				ids = append(ids, s.initIDs[iface])
			}
		}
	}
	s.mu.Unlock()
	for _, id := range ids {
		s.handlers.NoteAlive(id)
	}
}

func (s *liteStream) setLive(live bool) {
	s.mu.Lock()
	if s.live != live {
		s.live = live
		s.notifyLocked()
	}
	s.mu.Unlock()
}

func (s *liteStream) setUp(iface hmenum.Interface, up bool) {
	s.mu.Lock()
	if s.up[iface] != up {
		s.up[iface] = up
		s.notifyLocked()
	}
	s.mu.Unlock()
}

// notifyLocked wakes every waiting announcer; the caller holds mu.
func (s *liteStream) notifyLocked() {
	close(s.changed)
	s.changed = make(chan struct{})
}

// snapshot reports whether the stream is live and iface up, with the
// channel that closes on the next change.
func (s *liteStream) snapshot(iface hmenum.Interface) (live, up bool, changed <-chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.live, s.up[iface], s.changed
}

func (s *liteStream) setReseed(fn func(ctx context.Context, iface hmenum.Interface) error) {
	s.mu.Lock()
	s.reseed = fn
	s.mu.Unlock()
}

func (s *liteStream) currentReseed() func(ctx context.Context, iface hmenum.Interface) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reseed
}

// liteAnnouncer replaces init/deinit for an openccu-lite interface: the box
// owns the subscription, so announcing means waiting until the event
// stream is live and reports the interface up.
type liteAnnouncer struct {
	ingress *liteEventIngress
	iface   hmenum.Interface
}

// errLiteInterfaceNotUp reports an announcement that timed out waiting for
// the box's event stream to report the interface up.
var errLiteInterfaceNotUp = errors.New("openccu-lite: the interface is not up on the box's event stream")

// Init implements [backends.Announcer]. It blocks until the stream is live
// and the interface up, ctx ends, or [liteAnnounceTimeout] passes; the
// caller's existing failure path (the bring-up's retry, the recovery's
// backoff) takes over on an error.
func (a *liteAnnouncer) Init(ctx context.Context, _, _ string) error {
	s := a.ingress.current()
	if s == nil {
		return fmt.Errorf("%w: %s (no event stream attached)", errLiteInterfaceNotUp, a.iface)
	}
	timer := time.NewTimer(liteAnnounceTimeout)
	defer timer.Stop()
	for {
		live, up, changed := s.snapshot(a.iface)
		if live && up {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%w: %s: %w", errLiteInterfaceNotUp, a.iface, ctx.Err())
		case <-timer.C:
			return fmt.Errorf("%w: %s (live=%v up=%v)", errLiteInterfaceNotUp, a.iface, live, up)
		case <-changed:
		}
	}
}

// Deinit implements [backends.Announcer]: occulited owns the daemon-side
// subscription, there is nothing to withdraw.
func (*liteAnnouncer) Deinit(context.Context, string) error { return nil }

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
