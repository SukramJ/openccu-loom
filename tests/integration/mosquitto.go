// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

//go:build integration

package integration

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// mosquittoServer wraps a Mosquitto instance (dockerised, or a native
// `mosquitto` binary when Docker is unavailable) so the MQTT bridge can
// be exercised end-to-end. Tests skip automatically when neither a
// Docker daemon nor a native binary is reachable.
type mosquittoServer struct {
	name string
	port int
}

// URL returns the broker URL usable by [mqtt.TCPClient].
func (m *mosquittoServer) URL() string {
	return fmt.Sprintf("tcp://127.0.0.1:%d", m.port)
}

// startMosquitto brings up a Mosquitto broker on an ephemeral port and
// registers a Cleanup that stops it. It prefers a Docker container
// (the reproducible CI path) and falls back to a native `mosquitto`
// binary on PATH so the MQTT integration suite can run on developer
// machines without a Docker daemon. The test skips only when neither
// is available.
func startMosquitto(t *testing.T) *mosquittoServer {
	t.Helper()
	if srv, ok := startMosquittoDocker(t); ok {
		return srv
	}
	if srv, ok := startMosquittoNative(t); ok {
		return srv
	}
	t.Skip("no MQTT broker available: Docker daemon not reachable and no `mosquitto` binary on PATH")
	return nil
}

// startMosquittoDocker attempts the dockerised broker. Returns
// ok=false (without failing the test) when Docker is not on PATH or the
// daemon refuses the run, so the caller can fall back to the native
// binary.
func startMosquittoDocker(t *testing.T) (*mosquittoServer, bool) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		return nil, false
	}
	port, err := pickFreePort()
	if err != nil {
		t.Fatalf("pick port: %v", err)
	}
	name := fmt.Sprintf("openccu-loom-mosq-%d-%d", port, time.Now().UnixNano())

	args := []string{
		"run", "-d", "--rm",
		"--name", name,
		"-p", fmt.Sprintf("%d:1883", port),
		"eclipse-mosquitto:2",
		"mosquitto", "-c", "/mosquitto-no-auth.conf",
	}
	var buf bytes.Buffer
	cmd := exec.Command("docker", args...) //nolint:gosec // integration harness
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		// Docker is present but the daemon is not reachable (common on
		// dev machines with Docker Desktop stopped). Fall back rather
		// than skip.
		t.Logf("docker run mosquitto failed, falling back to native binary: %v %s", err, buf.String())
		return nil, false
	}

	if err := waitForPort(port, 10*time.Second); err != nil {
		_ = exec.Command("docker", "rm", "-f", name).Run() //nolint:gosec // cleanup
		t.Fatalf("mosquitto never accepted: %v", err)
	}
	// The broker port is open, but Mosquitto completes its
	// initialisation only after the listener thread is fully running.
	// A 500 ms grace period prevents sporadic `connection reset by peer`
	// errors on the CONNECT.
	time.Sleep(500 * time.Millisecond)
	t.Cleanup(func() {
		_ = exec.Command("docker", "rm", "-f", name).Run() //nolint:gosec // cleanup
	})
	return &mosquittoServer{name: name, port: port}, true
}

// startMosquittoNative runs a native `mosquitto` binary on an ephemeral
// port with a minimal anonymous-listener config in a temp directory.
// Returns ok=false when the binary is not on PATH.
func startMosquittoNative(t *testing.T) (*mosquittoServer, bool) {
	t.Helper()
	bin, err := exec.LookPath("mosquitto")
	if err != nil {
		return nil, false
	}
	port, err := pickFreePort()
	if err != nil {
		t.Fatalf("pick port: %v", err)
	}

	dir := t.TempDir()
	confPath := filepath.Join(dir, "mosquitto.conf")
	conf := fmt.Sprintf("listener %d 127.0.0.1\nallow_anonymous true\npersistence false\n", port)
	if err := os.WriteFile(confPath, []byte(conf), 0o600); err != nil {
		t.Fatalf("write mosquitto.conf: %v", err)
	}

	var buf bytes.Buffer
	cmd := exec.Command(bin, "-c", confPath) //nolint:gosec // integration harness, fixed binary
	cmd.Stderr = &buf
	cmd.Stdout = &buf
	if err := cmd.Start(); err != nil {
		t.Fatalf("start native mosquitto: %v", err)
	}

	if err := waitForPort(port, 10*time.Second); err != nil {
		_ = cmd.Process.Kill()
		t.Fatalf("native mosquitto never accepted: %v (%s)", err, buf.String())
	}
	time.Sleep(300 * time.Millisecond)
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	return &mosquittoServer{name: "native-mosquitto", port: port}, true
}

func waitForPort(port int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 500*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("port %d never opened", port)
}

// pickFreePort binds a TCP listener on 127.0.0.1:0, reads the assigned
// port and closes the listener. Reusing the port has a tiny race
// window — another process might grab it between close and the
// container starting — but in practice it is stable enough for a
// local integration run. Mosquitto needs an explicit host port for
// the `docker run -p` mapping, so we cannot lean on
// godevccu.EphemeralPort here.
func pickFreePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		_ = ln.Close()
		return 0, fmt.Errorf("unexpected listener address type %T", ln.Addr())
	}
	if err := ln.Close(); err != nil {
		return 0, err
	}
	return addr.Port, nil
}
