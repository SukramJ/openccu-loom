// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build integration

package integration

import (
	"bytes"
	"fmt"
	"net"
	"os/exec"
	"testing"
	"time"
)

// mosquittoServer wraps a dockerised Mosquitto instance so the
// MQTT bridge can be exercised end-to-end. Tests skip automatically
// when Docker is not on PATH.
type mosquittoServer struct {
	name string
	port int
}

// URL returns the broker URL usable by [mqtt.TCPClient].
func (m *mosquittoServer) URL() string {
	return fmt.Sprintf("tcp://127.0.0.1:%d", m.port)
}

// startMosquitto spawns a Mosquitto container on an ephemeral port,
// waits until it accepts TCP, and registers a Cleanup that stops
// the container.
func startMosquitto(t *testing.T) *mosquittoServer {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not on PATH; install Docker to run MQTT integration tests")
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
		t.Skipf("docker run mosquitto failed: %v %s", err, buf.String())
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
	return &mosquittoServer{name: name, port: port}
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
