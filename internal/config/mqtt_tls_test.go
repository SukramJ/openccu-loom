// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package config

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeMQTTTestCA writes a self-signed CA certificate as PEM and returns
// its absolute path.
func writeMQTTTestCA(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "mqtt test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("certificate: %v", err)
	}
	path := filepath.Join(t.TempDir(), "ca.crt")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatalf("write CA: %v", err)
	}
	return path
}

func mqttTLSYAML(brokerURL, extra string) []byte {
	return []byte(minimalCentralYAML + `
north:
  mqtt:
    enabled: true
    broker_url: ` + brokerURL + `
` + extra)
}

// TestMQTTTLSOptionsAreParsed: tls_ca_file and tls_insecure_skip_verify
// reach NorthMQTT from YAML, and a usable CA on a TLS broker_url passes
// validation.
func TestMQTTTLSOptionsAreParsed(t *testing.T) {
	ca := writeMQTTTestCA(t)
	cfg, err := Parse(mqttTLSYAML("ssl://broker.example:8883", "    tls_ca_file: "+ca+"\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.North.MQTT.TLSCAFile != ca || cfg.North.MQTT.TLSInsecureSkipVerify {
		t.Errorf("TLSCAFile = %q, TLSInsecureSkipVerify = %v; want %q, false",
			cfg.North.MQTT.TLSCAFile, cfg.North.MQTT.TLSInsecureSkipVerify, ca)
	}
	cfg, err = Parse(mqttTLSYAML("mqtts://broker.example", "    tls_insecure_skip_verify: true\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !cfg.North.MQTT.TLSInsecureSkipVerify {
		t.Error("tls_insecure_skip_verify: true did not reach NorthMQTT")
	}
}

// TestMQTTTLSOptionsAreRefused pins every refusal: the TLS knobs on a
// plaintext broker_url (an operator who set one expects encryption), the
// contradictory pair, a relative path, and a CA file the daemon could not
// dial with — each with the key the operator has to look at.
func TestMQTTTLSOptionsAreRefused(t *testing.T) {
	ca := writeMQTTTestCA(t)
	dir := t.TempDir()
	notACert := filepath.Join(dir, "key.pem")
	if err := os.WriteFile(notACert, []byte("-----BEGIN PRIVATE KEY-----\nAAAA\n-----END PRIVATE KEY-----\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "missing.crt")
	for _, tc := range []struct {
		name, url, extra string
		want             []string
	}{
		{"CA on tcp", "tcp://broker:1883", "    tls_ca_file: " + ca + "\n", []string{"mqtt.tls_ca_file", "requires a TLS broker_url"}},
		{"insecure on mqtt", "mqtt://broker", "    tls_insecure_skip_verify: true\n", []string{"mqtt.tls_insecure_skip_verify", "requires a TLS broker_url"}},
		{"CA and insecure", "tls://broker", "    tls_ca_file: " + ca + "\n    tls_insecure_skip_verify: true\n", []string{"mqtt.tls_ca_file", "cannot be combined with tls_insecure_skip_verify"}},
		{"relative CA", "tls://broker", "    tls_ca_file: certs/ca.crt\n", []string{"mqtt.tls_ca_file", "absolute path"}},
		{"missing CA", "tls://broker", "    tls_ca_file: " + missing + "\n", []string{"mqtt.tls_ca_file", "missing.crt"}},
		{"not a certificate", "tls://broker", "    tls_ca_file: " + notACert + "\n", []string{"mqtt.tls_ca_file", "key.pem"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(mqttTLSYAML(tc.url, tc.extra))
			if err == nil {
				t.Fatal("accepted")
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("err = %v, want it to mention %q", err, w)
				}
			}
		})
	}
}

// TestMQTTTLSOptionsFromEnv: both knobs have an OPENCCU_LOOM_MQTT_* overlay,
// and a malformed boolean for the security switch is an error naming the
// variable rather than a silently kept YAML value.
func TestMQTTTLSOptionsFromEnv(t *testing.T) {
	cfg := Default()
	if err := cfg.OverlayFromEnv(mapEnv{
		"OPENCCU_LOOM_MQTT_TLS_CA_FILE":              "/ssl/ca.crt",
		"OPENCCU_LOOM_MQTT_TLS_INSECURE_SKIP_VERIFY": "true",
	}.Get); err != nil {
		t.Fatalf("OverlayFromEnv: %v", err)
	}
	if cfg.North.MQTT.TLSCAFile != "/ssl/ca.crt" || !cfg.North.MQTT.TLSInsecureSkipVerify {
		t.Errorf("TLSCAFile = %q, TLSInsecureSkipVerify = %v", cfg.North.MQTT.TLSCAFile, cfg.North.MQTT.TLSInsecureSkipVerify)
	}
	if err := cfg.OverlayFromEnv(mapEnv{"OPENCCU_LOOM_MQTT_TLS_INSECURE_SKIP_VERIFY": "off"}.Get); err != nil {
		t.Fatalf("OverlayFromEnv: %v", err)
	}
	if cfg.North.MQTT.TLSInsecureSkipVerify {
		t.Error("OPENCCU_LOOM_MQTT_TLS_INSECURE_SKIP_VERIFY=off left the switch on")
	}
	err := cfg.OverlayFromEnv(mapEnv{"OPENCCU_LOOM_MQTT_TLS_INSECURE_SKIP_VERIFY": "maybe"}.Get)
	if err == nil || !strings.Contains(err.Error(), "OPENCCU_LOOM_MQTT_TLS_INSECURE_SKIP_VERIFY") {
		t.Errorf("err = %v, want an error naming the variable", err)
	}
}

// TestMQTTTLSScheme: the scheme spellings go-mqtt dials as TLS.
func TestMQTTTLSScheme(t *testing.T) {
	for scheme, want := range map[string]bool{"tls": true, "ssl": true, "mqtts": true, "tcp": false, "mqtt": false, "": false} {
		if got := MQTTTLSScheme(scheme); got != want {
			t.Errorf("MQTTTLSScheme(%q) = %v, want %v", scheme, got, want)
		}
	}
}
