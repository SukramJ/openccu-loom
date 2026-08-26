// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/config"
)

// recordingSwapper captures the config the reload adapter hands to the
// supervisor, which is the whole question these tests ask: fresh or stale.
type recordingSwapper struct {
	mu   sync.Mutex
	last *config.Config
}

func (r *recordingSwapper) Swap(_ context.Context, newCfg *config.Config) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.last = newCfg
	return nil
}

func (r *recordingSwapper) password() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.last == nil {
		return ""
	}
	return r.last.North.MQTT.Password
}

// TestMQTTReloadAdapter_UsesFreshlyAssembledConfig covers the reported
// "reload changes nothing, I have to restart": an operator edits north.mqtt in
// the SPA, which writes the DB-tier section — no YAML file event follows, so
// the recorded snapshot still holds the boot values. Reading only that
// snapshot rebuilt the broker link from the stale credentials while reporting
// success. The adapter must re-assemble the effective config instead.
func TestMQTTReloadAdapter_UsesFreshlyAssembledConfig(t *testing.T) {
	t.Parallel()

	boot := config.Default()
	boot.North.MQTT.Enabled = true
	boot.North.MQTT.BrokerURL = "tcp://broker.example:1883"
	boot.North.MQTT.Password = "stale-boot-password"

	deps := newReloadDeps()
	deps.SetCurrentConfig(boot)
	// What the SPA persisted after boot.
	deps.SetConfigAssembler(func(context.Context) (*config.Config, error) {
		fresh := config.Clone(boot)
		fresh.North.MQTT.Password = "password-saved-in-the-spa"
		return fresh, nil
	})

	swapped := &recordingSwapper{}
	adapter := &mqttReloadAdapter{sup: swapped, deps: deps, bootCfg: boot, logger: slog.New(slog.DiscardHandler)}

	if _, err := adapter.Reload(context.Background()); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if got := swapped.password(); got != "password-saved-in-the-spa" {
		t.Errorf("reload swapped in the stale snapshot password %q — SPA section edits are invisible to the reload", got)
	}
	// The freshly assembled config must also become the recorded snapshot so
	// later readers agree with the running stack.
	if got := deps.CurrentConfig().North.MQTT.Password; got != "password-saved-in-the-spa" {
		t.Errorf("recorded snapshot not advanced after a successful swap: %q", got)
	}
}

// TestMQTTReloadAdapter_FallsBackAndWarnsWhenAssemblyFails pins the degraded
// path: a failing assembly still reloads from the last snapshot rather than
// refusing, but says so — a silent fallback would read as a fresh reload and
// send the operator hunting the wrong layer.
func TestMQTTReloadAdapter_FallsBackAndWarnsWhenAssemblyFails(t *testing.T) {
	t.Parallel()

	boot := config.Default()
	boot.North.MQTT.Enabled = true
	boot.North.MQTT.Password = "snapshot-password"

	deps := newReloadDeps()
	deps.SetCurrentConfig(boot)
	deps.SetConfigAssembler(func(context.Context) (*config.Config, error) {
		return nil, errors.New("database unavailable")
	})

	var logBuf bytes.Buffer
	swapped := &recordingSwapper{}
	adapter := &mqttReloadAdapter{
		sup: swapped, deps: deps, bootCfg: boot,
		logger: slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelWarn})),
	}

	if _, err := adapter.Reload(context.Background()); err != nil {
		t.Fatalf("Reload must still succeed on the snapshot: %v", err)
	}
	if got := swapped.password(); got != "snapshot-password" {
		t.Errorf("fallback did not use the recorded snapshot: %q", got)
	}
	if !strings.Contains(logBuf.String(), "mqtt.reload.stale_config") {
		t.Errorf("a stale-config reload must be logged; got:\n%s", logBuf.String())
	}
}

// TestReloadDeps_AssembleConfig_ReportsFreshness pins the freshness flag the
// adapter logs on: no assembler wired means the snapshot is returned and the
// caller is told it is not fresh.
func TestReloadDeps_AssembleConfig_ReportsFreshness(t *testing.T) {
	t.Parallel()

	deps := newReloadDeps()
	snapshot := config.Default()
	snapshot.North.MQTT.TopicBase = "from-snapshot"
	deps.SetCurrentConfig(snapshot)

	cfg, fresh := deps.AssembleConfig(context.Background())
	if fresh {
		t.Error("without an assembler the result must not be reported as fresh")
	}
	if cfg == nil || cfg.North.MQTT.TopicBase != "from-snapshot" {
		t.Errorf("fallback must return the recorded snapshot, got %#v", cfg)
	}

	deps.SetConfigAssembler(func(context.Context) (*config.Config, error) {
		assembled := config.Default()
		assembled.North.MQTT.TopicBase = "from-assembler"
		return assembled, nil
	})
	cfg, fresh = deps.AssembleConfig(context.Background())
	if !fresh {
		t.Error("a wired assembler must report a fresh assembly")
	}
	if cfg.North.MQTT.TopicBase != "from-assembler" {
		t.Errorf("assembler result not used: %q", cfg.North.MQTT.TopicBase)
	}
}

// TestLogConnectFailure_ReportsCredentialPresence pins the diagnostic that
// makes a rejected CONNECT attributable. A broker answers a missing password
// and a wrong one with the same "Not authorized (0x87)", so the presence flags
// are the only thing separating "wrong credential" from "no credential sent".
// The secret itself must never appear.
func TestLogConnectFailure_ReportsCredentialPresence(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelError}))
	logConnectFailure(logger, config.NorthMQTT{
		BrokerURL: "tcp://broker.example:1883",
		ClientID:  "loom",
		Username:  "loom",
		Password:  "", // wiped credential — the reported failure mode
	}, errors.New("CONNECT rejected: Not authorized (0x87)"))

	out := buf.String()
	for _, want := range []string{
		"mqtt.connect.failed",
		"username_set=true",
		"password_set=false",
		"protocol_version=5",
		"client_id=loom",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("connect-failure log must carry %q; got:\n%s", want, out)
		}
	}
}

// TestLogConnectFailure_NeverLogsTheSecret guards the other half: the log line
// lands in support bundles, so it may report that a password exists and how
// long it is, never what it is.
func TestLogConnectFailure_NeverLogsTheSecret(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelError}))
	logConnectFailure(logger, config.NorthMQTT{
		BrokerURL: "tcp://user:hunter2@broker.example:1883",
		Username:  "loom",
		Password:  "super-secret-password",
	}, errors.New("CONNECT rejected: Not authorized (0x87)"))

	out := buf.String()
	if strings.Contains(out, "super-secret-password") {
		t.Errorf("password leaked into the log:\n%s", out)
	}
	if strings.Contains(out, "hunter2") {
		t.Errorf("broker-URL userinfo leaked into the log:\n%s", out)
	}
	if !strings.Contains(out, "password_len=21") {
		t.Errorf("password length should be reported; got:\n%s", out)
	}
}
