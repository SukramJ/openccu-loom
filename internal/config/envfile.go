// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package config

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// DefaultEnvFile is the conventional name the daemon looks for in
// the working directory at startup. Any key already present in the
// process environment wins — file values fill in missing keys only.
const DefaultEnvFile = ".env"

// LoadEnvFile reads KEY=VALUE pairs from path and exports them via
// [os.Setenv], skipping any key that is already set in the process
// environment. Returns nil when path does not exist (the file is
// optional by design).
//
// Supported line syntax (subset of POSIX shell, intentionally
// narrow):
//
//   - `KEY=VALUE`
//   - `KEY="VALUE"`  → double-quoted; supports \\ and \" escapes
//   - `KEY='VALUE'`  → single-quoted; literal, no escapes
//   - blank lines and `# comment` lines are skipped
//   - leading `export ` is tolerated for compatibility with
//     hand-edited files that double as bash sourceables
//
// Lines without an `=` are reported as ErrEnvFileSyntax wrapped with
// the line number. The loader is intentionally strict so a typo
// surfaces at boot rather than silently producing an unauthenticated
// CCU dial.
func LoadEnvFile(path string) error {
	if path == "" {
		return nil
	}
	f, err := os.Open(path) //nolint:gosec // operator-supplied path
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("config: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	return parseEnvFile(f, path, os.Getenv, os.Setenv)
}

// ErrEnvFileSyntax marks a malformed line.
var ErrEnvFileSyntax = errors.New("config: envfile: malformed line")

func parseEnvFile(r io.Reader, path string, getEnv func(string) string, setEnv func(string, string) error) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			return fmt.Errorf("%w: %s:%d", ErrEnvFileSyntax, path, lineNo)
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		if key == "" {
			return fmt.Errorf("%w: %s:%d", ErrEnvFileSyntax, path, lineNo)
		}
		// Strip an optional inline comment when the value is NOT
		// quoted. Quoted values keep `#` verbatim.
		decoded, err := decodeEnvValue(val)
		if err != nil {
			return fmt.Errorf("%w: %s:%d: %w", ErrEnvFileSyntax, path, lineNo, err)
		}
		// Real env wins.
		if getEnv(key) != "" {
			continue
		}
		if err := setEnv(key, decoded); err != nil {
			return fmt.Errorf("config: setenv %s: %w", key, err)
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("config: read %s: %w", path, err)
	}
	return nil
}

// decodeEnvValue handles the three quoting forms documented in
// [LoadEnvFile]. Unquoted values strip a trailing `# comment` so
// hand-edits can annotate inline.
func decodeEnvValue(v string) (string, error) {
	switch {
	case strings.HasPrefix(v, `"`):
		if !strings.HasSuffix(v, `"`) || len(v) < 2 {
			return "", errors.New("unterminated double-quoted value")
		}
		raw := v[1 : len(v)-1]
		var b strings.Builder
		for i := 0; i < len(raw); i++ {
			if raw[i] == '\\' && i+1 < len(raw) {
				switch raw[i+1] {
				case '"', '\\':
					b.WriteByte(raw[i+1])
					i++
					continue
				case 'n':
					b.WriteByte('\n')
					i++
					continue
				}
			}
			b.WriteByte(raw[i])
		}
		return b.String(), nil
	case strings.HasPrefix(v, "'"):
		if !strings.HasSuffix(v, "'") || len(v) < 2 {
			return "", errors.New("unterminated single-quoted value")
		}
		return v[1 : len(v)-1], nil
	default:
		if h := strings.IndexByte(v, '#'); h >= 0 {
			v = strings.TrimSpace(v[:h])
		}
		return v, nil
	}
}
