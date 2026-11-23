//go:build integration

// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package integration

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/ccudata"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/model/custom/cdpkind"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/internal/store/visibility"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// paramsetBackendWriter forwards the model's writes to the simulated CCU, which
// refuses a parameter the channel does not describe — the check this guard
// rests on.
type paramsetBackendWriter struct{ b *backends.CcuBackend }

func (w paramsetBackendWriter) SetValue(ctx context.Context, _, _, addr string, p hmenum.Parameter, v any, prio hmenum.CommandPriority) error {
	return w.b.SetValue(ctx, addr, p, v, prio, "")
}

func (w paramsetBackendWriter) PutParamset(ctx context.Context, _, _, addr string, key hmenum.ParamsetKey, values map[string]any, prio hmenum.CommandPriority) error {
	return w.b.PutParamset(ctx, addr, key, values, prio, "")
}

// landedWrites records, per channel address, every parameter the simulated
// CCU accepted and the value it took.
type landedWrites struct {
	mu     sync.Mutex
	params map[string]map[string]any
}

// record is the simulated CCU's write hook. The value is wire-decoded
// XML-RPC, whose type depends on the parameter, hence any.
func (l *landedWrites) record(address, valueKey string, value any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.params == nil {
		l.params = map[string]map[string]any{}
	}
	a := strings.ToUpper(address)
	if l.params[a] == nil {
		l.params[a] = map[string]any{}
	}
	l.params[a][valueKey] = value
}

func (l *landedWrites) take(address string) map[string]any {
	l.mu.Lock()
	defer l.mu.Unlock()
	a := strings.ToUpper(address)
	got := l.params[a]
	delete(l.params, a)
	return got
}

// TestEveryLightThatDeclaresATransitionAcceptsOne walks the whole simulated
// fleet through the production ingest and holds every light to its declared
// `transition` capability, observed on the wire: a light that declares one
// puts a ramp parameter beside LEVEL for a `transition` switch-on, and the
// simulated CCU accepts it; a light that does not refuses the key and writes
// nothing. Declared and accepted are then the same set — the capability API
// consumers read is the command the services take.
func TestEveryLightThatDeclaresATransitionAcceptsOne(t *testing.T) {
	landed := &landedWrites{}
	srv := startMockCCUWithOptions(t, snapshotDevices(t), landed.record)
	xmlClient := newXMLRPCClient(t, srv.URL())
	backend := backends.NewCcuBackend(&xmlrpcBackendCaller{client: xmlClient}, nil, nil)

	c, err := central.New(central.Config{Name: "light-transition-ccu"})
	if err != nil {
		t.Fatalf("central: %v", err)
	}
	translations, err := ccudata.LoadTranslationsEmbedded()
	if err != nil {
		t.Fatalf("translations: %v", err)
	}
	pipeline := adapter.NewDevicePipeline(c).
		WithTranslations(translations, snapshotLocale()).
		WithVisibility(visibility.NewRegistry())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := pipeline.IngestFromBackend(
		ctx, "HmIP-RF", hmenum.InterfaceHmIPRF, backend, paramsetBackendWriter{b: backend}, nil, slog.New(slog.DiscardHandler),
	); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	type invoker interface {
		Invoke(ctx context.Context, name string, params map[string]any, priority hmenum.CommandPriority) error
	}
	var ramping, still int
	for _, dev := range c.ModelRegistry.List() {
		for _, ch := range dev.Channels() {
			cdp := ch.CustomDataPoint()
			if cdp == nil {
				continue
			}
			declared, isLight := cdpkind.Capabilities(cdp)["transition"]
			if !isLight {
				continue
			}
			inv, ok := cdp.(invoker)
			if !ok {
				t.Errorf("%s %s: a light without service methods", dev.Model, ch.Address)
				continue
			}
			landed.take(ch.Address)
			params := map[string]any{"state": "ON", "brightness": 255.0, "transition": 0.5}
			err := inv.Invoke(ctx, "set_level", params, hmenum.CommandPriorityHigh)
			wire := landed.take(ch.Address)
			if !declared {
				still++
				if !errors.Is(err, payload.ErrServiceInvalidParam) {
					t.Errorf("%s %s declares no transition but set_level with one returned %v", dev.Model, ch.Address, err)
				}
				if len(wire) != 0 {
					t.Errorf("%s %s declares no transition but a refused command wrote %v", dev.Model, ch.Address, wire)
				}
				continue
			}
			ramping++
			if err != nil {
				t.Errorf("%s %s declares a transition but set_level with one failed: %v", dev.Model, ch.Address, err)
				continue
			}
			// A zero ramp is no ramp: the HmIP-MP3P LED writes its ramp pair
			// on every switch-on, at 0 when the command names none.
			_, hasLevel := wire[string(hmenum.ParameterLevel)]
			hasRamp := nonZero(wire[string(hmenum.ParameterRampTime)]) || nonZero(wire[string(hmenum.ParameterRampTimeValue)])
			if !hasRamp || !hasLevel {
				t.Errorf("%s %s declares a transition, but the switch-on wrote %v (want LEVEL and a non-zero ramp)", dev.Model, ch.Address, wire)
			}
		}
	}
	if ramping == 0 || still == 0 {
		t.Fatalf("lights with a transition: %d, without: %d — the walk misses one side and this test would pass vacuously", ramping, still)
	}
	t.Logf("checked %d lights with a transition and %d without", ramping, still)
}

// nonZero reports whether a wire value is a number other than zero.
func nonZero(v any) bool {
	switch n := v.(type) {
	case int:
		return n != 0
	case int32:
		return n != 0
	case int64:
		return n != 0
	case float64:
		return n != 0
	}
	return false
}
