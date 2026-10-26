// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"log/slog"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/metrics"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
)

// mustBuildMQTT is buildMQTT for a configuration the test expects to build.
func mustBuildMQTT(t *testing.T, cfg *config.Config, logger *slog.Logger, collector *metrics.MqttCollector,
	channelHidden func(central, channelAddress string) bool, centralNames func() []string, maintenance mqttMaintenanceHooks,
) *mqttStack {
	t.Helper()
	stack, err := buildMQTT(cfg, logger, collector, channelHidden, centralNames, maintenance)
	if err != nil {
		t.Fatalf("buildMQTT: %v", err)
	}
	return stack
}

// mustNorthTCPConfig is northTCPConfig for a configuration the test expects
// to build.
func mustNorthTCPConfig(t *testing.T, cfg *config.Config, logger *slog.Logger) mqtt.TCPConfig {
	t.Helper()
	tcpCfg, err := northTCPConfig(cfg, logger)
	if err != nil {
		t.Fatalf("northTCPConfig: %v", err)
	}
	return tcpCfg
}

// testBrokerPKI is a private CA and a leaf certificate it issued for
// localhost / 127.0.0.1, the shape of a broker behind a home-grown CA.
type testBrokerPKI struct {
	caFile string
	leaf   tls.Certificate
}

func newTestBrokerPKI(t *testing.T) testBrokerPKI {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "loom test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "localhost"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1)},
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caFile := filepath.Join(t.TempDir(), "ca.crt")
	if err := os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return testBrokerPKI{caFile: caFile, leaf: tls.Certificate{Certificate: [][]byte{leafDER}, PrivateKey: leafKey}}
}

// tlsBrokerHandshake starts a TLS listener serving pki's leaf, lets the
// client built from tcpCfg dial it once, and reports whether the TLS
// handshake succeeded — i.e. whether the configured trust accepted the
// broker. The listener speaks no MQTT: it closes the connection after the
// handshake, so the client's Connect fails either way and only the
// handshake outcome is the answer.
func tlsBrokerHandshake(t *testing.T, pki testBrokerPKI, tcpCfg mqtt.TCPConfig) error {
	t.Helper()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{pki.leaf}, MinVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	result := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			result <- err
			return
		}
		defer func() { _ = conn.Close() }()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		result <- conn.(*tls.Conn).Handshake()
	}()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	tcpCfg.BrokerURL = strings.Replace(tcpCfg.BrokerURL, "localhost:8883", "localhost:"+port, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := mqtt.NewTCPClient(tcpCfg)
	_ = client.Connect(ctx)
	_ = client.Disconnect(ctx)
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		t.Fatal("the broker never saw a connection")
		return nil
	}
}

func northTLSTestConfig(brokerURL, caFile string, insecure bool) *config.Config {
	cfg := northSweepTestConfig()
	cfg.North.MQTT.BrokerURL = brokerURL
	cfg.North.MQTT.TLSCAFile = caFile
	cfg.North.MQTT.TLSInsecureSkipVerify = insecure
	return cfg
}

// TestBothBrokerConnectionsTrustTheConfiguredCA drives mqtt.tls_ca_file
// through the composition root to a real TLS handshake, on BOTH connections
// buildMQTT makes to the broker: the command plane and the retained-store
// sweeps. A broker certificate from a private CA is accepted with the CA and
// refused without it — the second half is what shows the first is the CA's
// doing and not a verifier that accepts anything.
func TestBothBrokerConnectionsTrustTheConfiguredCA(t *testing.T) {
	t.Parallel()
	pki := newTestBrokerPKI(t)
	logger := slog.New(slog.DiscardHandler)

	cfg := northTLSTestConfig("ssl://localhost:8883", pki.caFile, false)
	main := mustNorthTCPConfig(t, cfg, logger)
	stack := mustBuildMQTT(t, cfg, logger, nil, nil, func() []string { return nil }, mqttMaintenanceHooks{})
	if stack.sweepTCP.TLSConfig == nil || main.TLSConfig == nil {
		t.Fatal("a TLS broker_url built a connection without a TLS configuration")
	}
	if stack.sweepTCP.TLSConfig == main.TLSConfig {
		t.Error("the sweep and command connections share one *tls.Config")
	}
	for name, tcpCfg := range map[string]mqtt.TCPConfig{"command": main, "sweep": stack.sweepTCP} {
		if tcpCfg.TLSConfig.ServerName != "localhost" {
			t.Errorf("%s: ServerName = %q, want the broker host", name, tcpCfg.TLSConfig.ServerName)
		}
		if err := tlsBrokerHandshake(t, pki, tcpCfg); err != nil {
			t.Errorf("%s: the broker certificate from the configured CA was refused: %v", name, err)
		}
	}

	withoutCA := mustNorthTCPConfig(t, northTLSTestConfig("ssl://localhost:8883", "", false), logger)
	if err := tlsBrokerHandshake(t, pki, withoutCA); err == nil {
		t.Error("without tls_ca_file the private-CA certificate was accepted: the system roots cannot know it")
	}
}

// TestInsecureSkipVerifyAcceptsAnyBrokerAndSaysSo: the explicit opt-out
// reaches both connections, accepts a certificate nothing vouches for, and
// is logged every time a connection is built.
func TestInsecureSkipVerifyAcceptsAnyBrokerAndSaysSo(t *testing.T) {
	t.Parallel()
	pki := newTestBrokerPKI(t)
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))

	cfg := northTLSTestConfig("mqtts://localhost:8883", "", true)
	stack := mustBuildMQTT(t, cfg, logger, nil, nil, func() []string { return nil }, mqttMaintenanceHooks{})
	if !strings.Contains(logs.String(), "mqtt.tls.insecure_skip_verify") {
		t.Errorf("no warning logged for tls_insecure_skip_verify: %q", logs.String())
	}
	if !stack.sweepTCP.TLSConfig.InsecureSkipVerify {
		t.Error("the sweep connection verifies although the operator opted out")
	}
	if err := tlsBrokerHandshake(t, pki, mustNorthTCPConfig(t, cfg, slog.New(slog.DiscardHandler))); err != nil {
		t.Errorf("tls_insecure_skip_verify still refused the broker: %v", err)
	}
}

// TestPlaintextBrokerHasNoTLSConfig: on tcp:// neither connection carries a
// TLS configuration (go-mqtt would warn that it ignores one).
func TestPlaintextBrokerHasNoTLSConfig(t *testing.T) {
	t.Parallel()
	cfg := northSweepTestConfig()
	stack := mustBuildMQTT(t, cfg, slog.New(slog.DiscardHandler), nil, nil, func() []string { return nil }, mqttMaintenanceHooks{})
	if main := mustNorthTCPConfig(t, cfg, slog.New(slog.DiscardHandler)); main.TLSConfig != nil || stack.sweepTCP.TLSConfig != nil {
		t.Error("a tcp:// broker got a TLS configuration")
	}
}

// TestAnUnusableCAFailsTheBuild: the CA is loaded again when the stack is
// built, because the file can vanish between validation and a reload; the
// build then fails naming the file instead of falling back to the system
// roots.
func TestAnUnusableCAFailsTheBuild(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "gone.crt")
	cfg := northTLSTestConfig("tls://localhost:8883", missing, false)
	_, err := buildMQTT(cfg, slog.New(slog.DiscardHandler), nil, nil, func() []string { return nil }, mqttMaintenanceHooks{})
	if err == nil || !strings.Contains(err.Error(), "gone.crt") {
		t.Fatalf("err = %v, want a build error naming the CA file", err)
	}
}
