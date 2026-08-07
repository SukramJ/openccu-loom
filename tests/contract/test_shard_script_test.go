// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// shardScriptTotal is the shard count ci.yml's test matrix uses. The number
// itself is a tuning knob, but the partition below has to hold for whatever
// value the workflow picks, so the workflow-parity check keeps the two in
// step.
const shardScriptTotal = 4

// runTestShard invokes script/test_shard.sh for one shard, reading the
// package list from a file so the check is hermetic and does not pay for a
// module load per shard.
func runTestShard(t *testing.T, index, total int, listPath string) []string {
	t.Helper()
	scriptPath, err := filepath.Abs("../../script/test_shard.sh")
	if err != nil {
		t.Fatalf("resolve test_shard.sh: %v", err)
	}
	out, err := exec.Command(scriptPath, strconv.Itoa(index), strconv.Itoa(total), listPath).Output()
	if err != nil {
		t.Fatalf("test_shard.sh %d %d: %v", index, total, err)
	}
	var pkgs []string
	for line := range strings.Lines(string(out)) {
		if line = strings.TrimSpace(line); line != "" {
			pkgs = append(pkgs, line)
		}
	}
	return pkgs
}

// TestTestShardScriptPartitionsEveryPackage locks the property the sharded CI
// test matrix rests on: the shards together run exactly the package set a
// single `go test ./...` would, with no package tested twice and — the one
// that actually bites — none silently dropped. A shard split that loses a
// package produces a green CI run for code nothing executed, and nothing else
// in the pipeline would notice.
func TestTestShardScriptPartitionsEveryPackage(t *testing.T) {
	t.Parallel()

	// A synthetic list rather than `go list ./...`: the partitioning rule is
	// about positions in a list, not about this module's package names, and
	// a fixed input makes the expected assignment checkable by hand.
	const packageCount = 25
	want := make([]string, 0, packageCount)
	for i := range packageCount {
		want = append(want, "example.com/m/pkg"+strconv.Itoa(i))
	}
	listPath := filepath.Join(t.TempDir(), "packages.txt")
	if err := os.WriteFile(listPath, []byte(strings.Join(want, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write package list: %v", err)
	}

	seen := make(map[string]int, len(want))
	for shard := 1; shard <= shardScriptTotal; shard++ {
		got := runTestShard(t, shard, shardScriptTotal, listPath)
		if len(got) == 0 {
			t.Errorf("shard %d/%d is empty — a runner with nothing to do means the split is wrong",
				shard, shardScriptTotal)
		}
		for _, pkg := range got {
			seen[pkg]++
		}
	}

	for _, pkg := range want {
		switch seen[pkg] {
		case 1: // exactly one shard runs it, as required
		case 0:
			t.Errorf("package %s lands in no shard — it would never be tested", pkg)
		default:
			t.Errorf("package %s lands in %d shards — duplicated work", pkg, seen[pkg])
		}
	}
	if len(seen) != len(want) {
		t.Errorf("shards emitted %d distinct packages, want %d", len(seen), len(want))
	}
}

// TestTestShardScriptRejectsAnOutOfRangeIndex guards the failure mode that
// would otherwise be silent: a workflow that grew its matrix without telling
// the script must not quietly yield an empty package list, because an empty
// `go test` invocation exits 0 and the shard reports green.
func TestTestShardScriptRejectsAnOutOfRangeIndex(t *testing.T) {
	t.Parallel()

	scriptPath, err := filepath.Abs("../../script/test_shard.sh")
	if err != nil {
		t.Fatalf("resolve test_shard.sh: %v", err)
	}
	for _, args := range [][]string{
		{"0", "4"},
		{"5", "4"},
		{"1", "0"},
	} {
		if err := exec.Command(scriptPath, args...).Run(); err == nil {
			t.Errorf("test_shard.sh %v exited 0, want a non-zero status", args)
		}
	}
}

// TestCIWorkflowShardMatrixMatchesTheScript keeps ci.yml's matrix and the
// total this package asserts against from drifting apart. The partition test
// above proves the split is exhaustive for shardScriptTotal shards; that
// proof is only worth anything if the workflow actually runs that many.
func TestCIWorkflowShardMatrixMatchesTheScript(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("../../.github/workflows/ci.yml")
	if err != nil {
		t.Fatalf("read ci.yml: %v", err)
	}
	workflow := string(raw)

	// The matrix entry and the argument handed to the script both spell the
	// count, so both have to name shardScriptTotal.
	wantMatrix := "shard: [" + shardList(shardScriptTotal) + "]"
	if !strings.Contains(workflow, wantMatrix) {
		t.Errorf("ci.yml has no %q matrix — the shard count drifted from the contract's %d",
			wantMatrix, shardScriptTotal)
	}
	wantInvocation := "script/test_shard.sh \"${{ matrix.shard }}\" " + strconv.Itoa(shardScriptTotal)
	if !strings.Contains(workflow, wantInvocation) {
		t.Errorf("ci.yml does not invoke %q — the shard total drifted from the matrix", wantInvocation)
	}
}

// shardList renders the matrix literal for n shards, e.g. "1, 2, 3, 4".
func shardList(n int) string {
	parts := make([]string, 0, n)
	for i := 1; i <= n; i++ {
		parts = append(parts, strconv.Itoa(i))
	}
	return strings.Join(parts, ", ")
}
