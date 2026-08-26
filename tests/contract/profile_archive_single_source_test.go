// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"os"
	"path/filepath"
	"testing"
)

// TestProfileArchivesHaveOneSource asserts that no package carries its own
// copy of the eQ-3 profile archives.
//
// Two stores used to embed the same 65 files each, beside the shared
// metadata module's copy. Because nothing kept them in step, a data refresh
// reached the module while both copies kept the CCU's HTML references and
// the pre-3.89.5 constraint set — the same profile answered differently
// depending on which code path read it. Reintroducing a copy would
// reintroduce that split, silently.
func TestProfileArchivesHaveOneSource(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	var offenders []string
	err := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".gz" {
			return nil
		}
		// The archives are named <RECEIVER_TYPE>.json.gz; any .json.gz under
		// a data/ directory is the shape being guarded against.
		if filepath.Base(filepath.Dir(path)) == "data" {
			rel, _ := filepath.Rel(root, path)
			offenders = append(offenders, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk internal/: %v", err)
	}
	if len(offenders) > 0 {
		t.Errorf("%d embedded archive(s) found beside the shared module — read them through ccudata.ProfilesFS() instead:\n  %v",
			len(offenders), offenders[:min(len(offenders), 5)])
	}
}
