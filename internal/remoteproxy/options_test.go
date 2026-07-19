// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package remoteproxy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeOptionsFile writes the given content to a fresh options.json inside
// a per-test temp directory and returns the path.
func writeOptionsFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "options.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write options file: %v", err)
	}
	return path
}

func TestLoadOptions(t *testing.T) {
	t.Run("valid multi-instance file loads", func(t *testing.T) {
		path := writeOptionsFile(t, `{
			"log_level": "debug",
			"instances": [
				{"name": "primary", "url": "http://127.0.0.1:8080", "token": "tok-a", "tls_insecure": false},
				{"name": "backup", "url": "https://example.internal:8443", "token": "", "tls_insecure": true}
			]
		}`)
		opts, err := LoadOptions(path)
		if err != nil {
			t.Fatalf("LoadOptions: unexpected error: %v", err)
		}
		if opts.LogLevel != "debug" {
			t.Errorf("LogLevel = %q, want %q", opts.LogLevel, "debug")
		}
		if len(opts.Instances) != 2 {
			t.Fatalf("Instances = %d, want 2", len(opts.Instances))
		}
		if opts.Instances[0].Name != "primary" || opts.Instances[0].URL != "http://127.0.0.1:8080" {
			t.Errorf("instance 0 = %+v, want name=primary url=http://127.0.0.1:8080", opts.Instances[0])
		}
		if opts.Instances[1].Name != "backup" || !opts.Instances[1].TLSInsecure {
			t.Errorf("instance 1 = %+v, want name=backup tls_insecure=true", opts.Instances[1])
		}
	})

	t.Run("missing file errors", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "does-not-exist.json")
		_, err := LoadOptions(path)
		if err == nil {
			t.Fatal("LoadOptions: expected error for missing file, got nil")
		}
	})

	t.Run("malformed JSON errors", func(t *testing.T) {
		path := writeOptionsFile(t, `{ this is not json`)
		_, err := LoadOptions(path)
		if err == nil {
			t.Fatal("LoadOptions: expected error for malformed JSON, got nil")
		}
	})

	t.Run("unknown top-level fields are tolerated", func(t *testing.T) {
		path := writeOptionsFile(t, `{
			"log_level": "info",
			"instances": [
				{"name": "primary", "url": "http://127.0.0.1:8080"}
			],
			"future_field_from_a_newer_schema": "unknown-to-this-binary"
		}`)
		opts, err := LoadOptions(path)
		if err != nil {
			t.Fatalf("LoadOptions: expected lenient fallback to tolerate unknown field, got error: %v", err)
		}
		if len(opts.Instances) != 1 || opts.Instances[0].Name != "primary" {
			t.Errorf("opts = %+v, want single primary instance", opts)
		}
	})

	t.Run("empty instances rejected", func(t *testing.T) {
		path := writeOptionsFile(t, `{"log_level": "info", "instances": []}`)
		_, err := LoadOptions(path)
		if err == nil {
			t.Fatal("LoadOptions: expected error for empty instances, got nil")
		}
	})

	t.Run("bad log_level rejected", func(t *testing.T) {
		path := writeOptionsFile(t, `{
			"log_level": "trace",
			"instances": [{"name": "primary", "url": "http://127.0.0.1:8080"}]
		}`)
		_, err := LoadOptions(path)
		if err == nil {
			t.Fatal("LoadOptions: expected error for invalid log_level, got nil")
		}
	})

	t.Run("duplicate names rejected", func(t *testing.T) {
		path := writeOptionsFile(t, `{
			"instances": [
				{"name": "primary", "url": "http://127.0.0.1:8080"},
				{"name": "primary", "url": "http://127.0.0.1:9090"}
			]
		}`)
		_, err := LoadOptions(path)
		if err == nil {
			t.Fatal("LoadOptions: expected error for duplicate instance names, got nil")
		}
	})
}

func TestOptionsValidate(t *testing.T) {
	validInstance := func(name string) Instance {
		return Instance{Name: name, URL: "http://127.0.0.1:8080"}
	}

	t.Run("log level", func(t *testing.T) {
		cases := []struct {
			name    string
			level   string
			wantErr bool
		}{
			{"empty is default", "", false},
			{"debug", "debug", false},
			{"info", "info", false},
			{"warn", "warn", false},
			{"error", "error", false},
			{"unknown level rejected", "trace", true},
			{"uppercase rejected", "INFO", true},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				opts := Options{LogLevel: tc.level, Instances: []Instance{validInstance("primary")}}
				err := opts.Validate()
				if tc.wantErr && err == nil {
					t.Fatalf("Validate: expected error for log_level %q, got nil", tc.level)
				}
				if !tc.wantErr && err != nil {
					t.Fatalf("Validate: unexpected error for log_level %q: %v", tc.level, err)
				}
			})
		}
	})

	t.Run("empty instances rejected", func(t *testing.T) {
		opts := Options{Instances: nil}
		if err := opts.Validate(); err == nil {
			t.Fatal("Validate: expected error for empty instances, got nil")
		}
	})

	t.Run("instance names", func(t *testing.T) {
		cases := []struct {
			name    string
			inst    string
			wantErr bool
		}{
			{"empty name rejected", "", true},
			{"uppercase rejected", "PRIMARY", true},
			{"space rejected", "my instance", true},
			{"leading hyphen rejected", "-primary", true},
			{"too long rejected", strings.Repeat("a", 65), true},
			{"max length accepted", strings.Repeat("a", 64), false},
			{"single char accepted", "a", false},
			{"digits and separators accepted", "primary-2_a", false},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				opts := Options{Instances: []Instance{{Name: tc.inst, URL: "http://127.0.0.1:8080"}}}
				err := opts.Validate()
				if tc.wantErr && err == nil {
					t.Fatalf("Validate: expected error for name %q, got nil", tc.inst)
				}
				if !tc.wantErr && err != nil {
					t.Fatalf("Validate: unexpected error for name %q: %v", tc.inst, err)
				}
			})
		}
	})

	t.Run("duplicate names rejected", func(t *testing.T) {
		opts := Options{Instances: []Instance{validInstance("primary"), validInstance("primary")}}
		if err := opts.Validate(); err == nil {
			t.Fatal("Validate: expected error for duplicate instance names, got nil")
		}
	})

	t.Run("distinct names accepted", func(t *testing.T) {
		opts := Options{Instances: []Instance{validInstance("primary"), validInstance("backup")}}
		if err := opts.Validate(); err != nil {
			t.Fatalf("Validate: unexpected error for distinct names: %v", err)
		}
	})

	t.Run("invalid instance URL rejected", func(t *testing.T) {
		opts := Options{Instances: []Instance{{Name: "primary", URL: "ftp://example.com"}}}
		if err := opts.Validate(); err == nil {
			t.Fatal("Validate: expected error for ftp:// URL, got nil")
		}
	})

	t.Run("normalizes instance URL in place", func(t *testing.T) {
		opts := Options{Instances: []Instance{{Name: "primary", URL: "http://127.0.0.1:8080/base/"}}}
		if err := opts.Validate(); err != nil {
			t.Fatalf("Validate: unexpected error: %v", err)
		}
		if got, want := opts.Instances[0].URL, "http://127.0.0.1:8080/base"; got != want {
			t.Errorf("Instances[0].URL = %q, want %q", got, want)
		}
	})
}

func TestParseInstanceURL(t *testing.T) {
	t.Run("accepted", func(t *testing.T) {
		cases := []struct {
			name string
			raw  string
			want string
		}{
			{"plain http accepted", "http://127.0.0.1:8080", "http://127.0.0.1:8080"},
			{"plain https accepted", "https://example.internal:8443", "https://example.internal:8443"},
			{"trailing slash normalized away", "http://127.0.0.1:8080/base/", "http://127.0.0.1:8080/base"},
			{"root path trailing slash normalized away", "http://127.0.0.1:8080/", "http://127.0.0.1:8080"},
			{"whitespace around url trimmed", "  http://127.0.0.1:8080  ", "http://127.0.0.1:8080"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				u, err := parseInstanceURL(tc.raw)
				if err != nil {
					t.Fatalf("parseInstanceURL(%q): unexpected error: %v", tc.raw, err)
				}
				if got := u.String(); got != tc.want {
					t.Errorf("parseInstanceURL(%q) = %q, want %q", tc.raw, got, tc.want)
				}
			})
		}
	})

	t.Run("rejected", func(t *testing.T) {
		cases := []struct {
			name string
			raw  string
		}{
			{"ftp scheme rejected", "ftp://example.com/path"},
			{"no scheme rejected", "127.0.0.1:8080"},
			{"missing host rejected", "http:///path"},
			{"query rejected", "http://127.0.0.1:8080/?debug=1"},
			{"fragment rejected", "http://127.0.0.1:8080/#top"},
			{"userinfo rejected", "http://user:pass@127.0.0.1:8080"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				if _, err := parseInstanceURL(tc.raw); err == nil {
					t.Fatalf("parseInstanceURL(%q): expected error, got nil", tc.raw)
				}
			})
		}
	})
}
