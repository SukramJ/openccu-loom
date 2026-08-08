// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build ignore

// crosscheck: Liest das vom Reachability-Tool produzierte Inventory und prüft
// per text-grep, ob ein als unreachable gemeldeter Identifier doch einen
// Production-Caller hat (RTA-False-Positive-Filter).
//
// Run: go run ./script/reachability/crosscheck.go
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type Entry struct {
	Package    string `json:"package"`
	Identifier string `json:"identifier"`
	File       string `json:"file"`
	Line       int    `json:"line"`
	Kind       string `json:"kind"`
}

type Inventory struct {
	Generated   string         `json:"generated"`
	Head        string         `json:"head"`
	Summary     map[string]int `json:"summary"`
	Unreachable []Entry        `json:"unreachable"`
}

type CrossCheckResult struct {
	Generated       string  `json:"generated"`
	Head            string  `json:"head"`
	TotalCandidates int     `json:"total_candidates"`
	FalsePositives  int     `json:"false_positives"`
	GenuineDeadCode int     `json:"genuine_dead_code"`
	Genuine         []Entry `json:"genuine"`
}

func main() {
	repoRoot, _ := os.Getwd()

	raw, err := os.ReadFile(filepath.Join(repoRoot, "notes/parity/dead-code-inventory.json"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "load inventory: %v\n", err)
		os.Exit(1)
	}
	var inv Inventory
	if err := json.Unmarshal(raw, &inv); err != nil {
		fmt.Fprintf(os.Stderr, "parse inventory: %v\n", err)
		os.Exit(1)
	}

	// Nur funcs prüfen (types sind oft False-Positive durch Interface-Dispatch)
	var candidates []Entry
	for _, e := range inv.Unreachable {
		if e.Kind == "func" {
			candidates = append(candidates, e)
		}
	}

	var genuine []Entry
	var falsePositives int
	for i, c := range candidates {
		if i%50 == 0 {
			fmt.Fprintf(os.Stderr, "progress %d/%d\n", i, len(candidates))
		}
		if hasProductionCaller(repoRoot, c) {
			falsePositives++
			continue
		}
		genuine = append(genuine, c)
	}

	out := CrossCheckResult{
		Generated:       time.Now().UTC().Format(time.RFC3339),
		Head:            inv.Head,
		TotalCandidates: len(candidates),
		FalsePositives:  falsePositives,
		GenuineDeadCode: len(genuine),
		Genuine:         genuine,
	}
	enc, _ := json.MarshalIndent(out, "", "  ")
	outPath := filepath.Join(repoRoot, "notes/parity/dead-code-genuine.json")
	if err := os.WriteFile(outPath, enc, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write output: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Cross-check complete:\n")
	fmt.Printf("  Candidates:       %d\n", out.TotalCandidates)
	fmt.Printf("  False-positives:  %d (%.1f%%)\n", out.FalsePositives, 100*float64(out.FalsePositives)/float64(out.TotalCandidates))
	fmt.Printf("  Genuine dead-code: %d (%.1f%%)\n", out.GenuineDeadCode, 100*float64(out.GenuineDeadCode)/float64(out.TotalCandidates))
	fmt.Printf("Output: %s\n", outPath)
}

// hasProductionCaller läuft grep auf cmd/ + internal/ + pkg/ (ohne _test.go)
// für den Identifier. Wenn irgendein Hit außerhalb des deklarierenden Files
// gefunden wird, ist es ein RTA-False-Positive.
func hasProductionCaller(repoRoot string, e Entry) bool {
	cmd := exec.Command(
		"grep",
		"-rln",
		"--include=*.go",
		"--exclude=*_test.go",
		"-w",
		e.Identifier,
		"cmd/", "internal/", "pkg/",
	)
	cmd.Dir = repoRoot
	out, _ := cmd.Output()
	declFile := filepath.Clean(e.File)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		if filepath.Clean(line) == declFile {
			// Nur das Deklarations-File → kein Caller
			continue
		}
		return true
	}
	return false
}
