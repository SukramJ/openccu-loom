// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

// The operator documentation contains shell commands that an operator is
// meant to copy and run, and until 2026-09-13 one of them could not be run
// at all: docs/user/multi-ccu.md's retain-cleanup pattern published to a
// topic ending in `/#`, which MQTT forbids in a PUBLISH (§4.7.0) and
// mosquitto_pub rejects outright, and passed `-n` and `-l` together, which
// are mutually exclusive. It shipped in v0.78.0. Nothing ran it, and nothing
// read it — it was a fenced code block, and fenced code blocks are prose to
// every gate this repository had.
//
// This test reads them. It is a linter for the two mistakes that make a
// documented mosquitto command unrunnable rather than merely wrong, which is
// the distinction that matters for a rollback instruction: an operator who
// follows a wrong-but-runnable command gets a wrong result they can see, and
// an operator who follows this one gets an error message and no way forward.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// docShellCommand is one shell command lifted out of a fenced block, with
// backslash continuations joined, plus where it came from.
type docShellCommand struct {
	file string
	line int
	text string
}

// TestOperatorMosquittoCommandsAreRunnable walks every Markdown file under
// docs/ and checks each mosquitto_pub / mosquitto_sub invocation in a fenced
// code block for the two errors that make it fail on execution.
func TestOperatorMosquittoCommandsAreRunnable(t *testing.T) {
	t.Parallel()

	cmds := collectDocShellCommands(t, filepath.Join(repoRoot(t), "docs"))

	pubs := 0
	for _, c := range cmds {
		isPub := strings.Contains(c.text, "mosquitto_pub")
		if !isPub && !strings.Contains(c.text, "mosquitto_sub") {
			continue
		}
		if isPub {
			pubs++
		}

		topic, ok := shellFlagValue(c.text, "-t")
		if ok && isPub && strings.ContainsAny(topic, "+#") {
			t.Errorf("%s:%d: mosquitto_pub publishes to %q, which carries an MQTT wildcard. A PUBLISH "+
				"topic may not contain `+` or `#` (MQTT §4.7.0) and mosquitto_pub rejects it — the "+
				"command cannot be run. Clearing a subtree is one publish per topic; list them with "+
				"`mosquitto_sub --retained-only` first.\n  %s", c.file, c.line, topic, c.text)
		}

		if hasShellFlag(c.text, "-n") && hasShellFlag(c.text, "-l") {
			t.Errorf("%s:%d: mosquitto command passes both `-n` and `-l`, which are mutually exclusive; "+
				"it exits with a usage error. To clear a retained message, pass `-r -n` alone.\n  %s",
				c.file, c.line, c.text)
		}
	}

	// Anti-vacuity. The extractor is the whole test: if it stops finding
	// commands — a fence label changes, a path moves — every assertion above
	// becomes unreachable and the file still reports a pass.
	if pubs == 0 {
		t.Error("found no mosquitto_pub invocation anywhere under docs/; the extractor is no longer " +
			"reading the operator documentation and this test checks nothing")
	}
}

// collectDocShellCommands returns every command inside a fenced shell block
// in the Markdown under root, with `\`-continuations folded into one line.
func collectDocShellCommands(t *testing.T, root string) []docShellCommand {
	t.Helper()

	var out []docShellCommand
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		raw, readErr := os.ReadFile(path) //nolint:gosec // repo-relative documentation
		if readErr != nil {
			return readErr
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		out = append(out, shellCommandsInMarkdown(filepath.ToSlash(filepath.Join("docs", rel)), string(raw))...)
		return nil
	})
	if err != nil {
		t.Fatalf("walk docs: %v", err)
	}
	return out
}

// shellCommandsInMarkdown extracts the fenced shell blocks of one document.
func shellCommandsInMarkdown(file, body string) []docShellCommand {
	var out []docShellCommand
	inShell := false
	pending := ""
	pendingLine := 0

	for i, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			// A fence always ends any half-collected command.
			pending, pendingLine = "", 0
			if inShell {
				inShell = false
				continue
			}
			lang := strings.ToLower(strings.TrimPrefix(trimmed, "```"))
			inShell = lang == "sh" || lang == "bash" || lang == "shell" || lang == "console"
			continue
		}
		if !inShell {
			continue
		}
		code := line
		if idx := strings.Index(code, " #"); idx >= 0 && strings.TrimSpace(code[:idx]) == "" {
			continue // whole-line comment
		}
		if strings.HasPrefix(strings.TrimSpace(code), "#") {
			continue
		}
		if pending == "" {
			pendingLine = i + 1
		}
		trimmedCode := strings.TrimRight(code, " \t")
		if strings.HasSuffix(trimmedCode, "\\") {
			pending += strings.TrimSuffix(trimmedCode, "\\") + " "
			continue
		}
		pending += trimmedCode
		if strings.TrimSpace(pending) != "" {
			out = append(out, docShellCommand{file: file, line: pendingLine, text: strings.TrimSpace(pending)})
		}
		pending, pendingLine = "", 0
	}
	return out
}

// shellFlagValue returns the argument following flag in a folded command
// line, with surrounding quotes stripped.
func shellFlagValue(cmd, flag string) (string, bool) {
	fields := strings.Fields(cmd)
	for i, f := range fields {
		if f == flag && i+1 < len(fields) {
			return strings.Trim(fields[i+1], "'\""), true
		}
	}
	return "", false
}

// hasShellFlag reports whether the folded command line passes flag as its
// own word. Clustered short options (`-rn`) are not used in these documents
// and are deliberately not matched: guessing at them would invent findings.
func hasShellFlag(cmd, flag string) bool {
	for _, f := range strings.Fields(cmd) {
		if f == flag {
			return true
		}
	}
	return false
}
