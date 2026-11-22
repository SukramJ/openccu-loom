// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package contract

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// closedFirmwareRule is one pattern that marks a citation into the HmIP
// server's closed binary rather than into its observable behaviour.
type closedFirmwareRule struct {
	label string
	re    *regexp.Regexp
}

// closedFirmwareRules are the shapes such a citation takes. The class names
// are the ones the repository's comments actually cited before the rule was
// written, not a guess at the binary's vocabulary; a new citation of a
// different class is caught by the package prefix, which every one of them
// carried. A class name quoted inside an observed log line is not cited here
// on purpose, so the list stays limited to names no log line shows.
//
// The rule and its reasoning: CLAUDE.md "Citing the CCU firmware" and
// notes/contributor/engineering-rules.md "Citing closed firmware".
func closedFirmwareRules() []closedFirmwareRule {
	return []closedFirmwareRule{
		{"package-qualified class", regexp.MustCompile(`\bde\.eq3\.`)},
		{"disassembly vocabulary", regexp.MustCompile(`(?i)\b(?:javap|bytecode|decompil\w*)`)},
		{"internal class name", regexp.MustCompile(`\b(?:` + strings.Join([]string{
			"AbstractPersistency",
			"AccessPointUtil",
			"BidCosVirtualChannelParameter",
			"ChannelTypeReader",
			"ClimateStateParameterFactory",
			"DeviceSubcommandHandler",
			"DeviceUtil",
			"GeneralConfigurationParameterFactory",
			"GeneralStateParameterFactory",
			"GroupDeviceAddressHelper",
			"GroupDeviceHandler",
			"HMIPApplicationHandler",
			"HmIPGroupDefinitionProvider",
			"IncomingHMIPFrameHandler",
			"LegacyServiceHandler",
			"NotificationUtil",
			"StateParameterFactory",
			"TransactionTaskFactory",
			"VirtualRemoteControl",
			"XmlRpcParser",
			"XmlRpcSerializer",
		}, "|") + `)\b`)},
	}
}

func matchClosedFirmware(line string) (string, bool) {
	for _, r := range closedFirmwareRules() {
		if r.re.MatchString(line) {
			return r.label, true
		}
	}
	return "", false
}

// TestClosedFirmwareRulesBite measures the patterns on lines of both kinds:
// the citation shapes that were removed, and the neighbouring vocabulary that
// must stay legal — the service's own name, this repo's Go identifiers that
// share a stem with a firmware class, and matter.js `Class#method` citations.
func TestClosedFirmwareRulesBite(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		line string
		want bool
	}{
		{"// (HMIPServer de.eq3.cbcs.legacy.Foo#bar)", true},
		{"// the server's XmlRpcParser casts it to Integer", true},
		{"// GeneralStateParameterFactory#createLevel", true},
		{"// read off with javap", true},
		{"// found in the Bytecode", true},
		{"// HMIPServer throws NotImplementedException for replaceDevice", false},
		{"// [hmenum.SmokeDetectorAlarmStatus] constants, in VALUE_LIST order", false},
		{"// the zero-padded grammar RepetitionsLabel formats", false},
		{"// GroupDeviceName is the backing virtual device's label", false},
		{"// Mirrors matter.js SwitchServer.ts (#handleSwitchPositionChange)", false},
		{`// logged "Could not execute BackendUpdateDevicesCommand"`, false},
	} {
		if _, got := matchClosedFirmware(tc.line); got != tc.want {
			t.Errorf("matchClosedFirmware(%q) = %v, want %v", tc.line, got, tc.want)
		}
	}
}

// TestNoClosedFirmwareInternalsInGoComments rejects a comment line that cites
// the HmIP server by its internals. Only comment lines are scanned: the
// patterns themselves live in string literals above.
func TestNoClosedFirmwareInternalsInGoComments(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..", "..")
	var hits []string
	for _, dir := range []string{"internal", "pkg", "cmd", "tests"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") {
				return nil
			}
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for i, line := range strings.Split(string(content), "\n") {
				trimmed := strings.TrimSpace(line)
				if !strings.HasPrefix(trimmed, "//") {
					continue
				}
				if label, ok := matchClosedFirmware(trimmed); ok {
					hits = append(hits, fmt.Sprintf("  %s:%d [%s]\n    %s", path, i+1, label, trimmed))
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
	reportClosedFirmwareHits(t, hits)
}

// TestNoClosedFirmwareInternalsInMarkdown applies the same rules to every
// Markdown file and to the JSON working files under notes/ and docs/: all of
// them are public in the repository, and docs/ is published on top.
func TestNoClosedFirmwareInternalsInMarkdown(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..", "..")
	skipDirs := map[string]bool{".git": true, ".claude": true, "node_modules": true, "spa_dist": true, "vendor": true}
	var hits []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		isJSONNote := strings.HasSuffix(path, ".json") &&
			(strings.HasPrefix(rel, "notes"+string(filepath.Separator)) || strings.HasPrefix(rel, "docs"+string(filepath.Separator)))
		if !strings.HasSuffix(path, ".md") && !isJSONNote {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(content), "\n") {
			if label, ok := matchClosedFirmware(line); ok {
				hits = append(hits, fmt.Sprintf("  %s:%d [%s]\n    %s", rel, i+1, label, strings.TrimSpace(line)))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	reportClosedFirmwareHits(t, hits)
}

func reportClosedFirmwareHits(t *testing.T, hits []string) {
	t.Helper()
	if len(hits) == 0 {
		return
	}
	t.Fatalf("%d line(s) cite the HmIP server by its internals:\n\n%s\n\n"+
		"State the behaviour and cite what a reader can check from the outside: the\n"+
		"paramset description, an observed wire fault or log line, a test in this repo,\n"+
		"or open firmware sources (CLAUDE.md, \"Citing the CCU firmware\").",
		len(hits), strings.Join(hits, "\n"))
}
