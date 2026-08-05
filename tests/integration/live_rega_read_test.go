// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build integration_live

package integration

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/client/rega"
	"github.com/SukramJ/openccu-loom/internal/client/transport/jsonrpc"
)

// Live read-only verification of the ReGa scripts.
//
// The scripts run on the CCU's own interpreter; nothing in the repo
// executes them. The simulator answers by matching a regular expression
// against the script text and never evaluates the body, so a refactor of
// a script's control flow cannot be validated against it. This suite is
// the only place where the scripts actually run.
//
// Every script exercised here is read-only: it walks the ReGa DOM and
// writes to stdout. No AlReceipt(), no Add()/Remove(), no ReadyConfig(),
// no DevStartComTest(). The write-side scripts are deliberately absent —
// they need a named target device and explicit approval.
//
//	go test -tags=integration_live -timeout=180s \
//	 ./tests/integration/... -run TestLive_RegaRead -v
//
// Environment:
//
//	OPENCCU_LOOM_LIVE_CCU_HOST   CCU hostname or IP (required, else skip)
//	OPENCCU_LOOM_LIVE_CCU_USER   CCU user (empty = unauthenticated)
//	OPENCCU_LOOM_LIVE_CCU_PASS   CCU password
//	OPENCCU_LOOM_LIVE_CCU_SCHEME "http" (default) or "https"
//	OPENCCU_LOOM_LIVE_REGA_BASE  git ref holding the previous script
//	                             revision (default "HEAD")
//	OPENCCU_LOOM_LIVE_SYSVAR     system-variable name for usage_by_sysvar
//	                             (absent = that one script is skipped)

// regaScriptDir is the script directory relative to this test file.
const regaScriptDir = "../../internal/client/rega/scripts"

// regaRunScriptMethod mirrors the unexported constant in package rega:
// the JSON-RPC method the CCU exposes for HomeMatic Script execution.
const regaRunScriptMethod = "ReGa.runScript"

// liveRegaClient opens an authenticated JSON-RPC session to the CCU and
// registers the logout.
func liveRegaClient(t *testing.T, env liveCCUEnv) *jsonrpc.Client {
	t.Helper()

	scheme := os.Getenv("OPENCCU_LOOM_LIVE_CCU_SCHEME")
	if scheme == "" {
		scheme = "http"
	}

	cfg := jsonrpc.Config{
		Endpoint: scheme + "://" + env.host + "/api/homematic.cgi",
		Username: env.user,
		Password: env.pass,
		Host:     env.host,
	}
	if scheme == "https" {
		// A CCU serves its own self-signed certificate; this test targets
		// an operator-named host on the local network.
		cfg.HTTPClient = &http.Client{
			Timeout:   30 * time.Second,
			Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}, //nolint:gosec // self-signed CCU certificate
		}
	}

	client, err := jsonrpc.New(cfg)
	if err != nil {
		t.Fatalf("jsonrpc.New: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := client.Login(ctx); err != nil {
		t.Fatalf("CCU login failed: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = client.Logout(ctx)
	})
	return client
}

// TestLive_RegaReadScriptsDeserialise runs every read-only script through
// its production Runner method against the live CCU. A ScriptRuntimeError
// or a framing defect surfaces here as an empty body or a JSON parse
// error, because each method unmarshals into the DTO the daemon uses.
func TestLive_RegaReadScriptsDeserialise(t *testing.T) {
	env := checkLiveCCU(t)
	client := liveRegaClient(t, env)

	runner, err := rega.NewRunner(rega.Config{Client: client})
	if err != nil {
		t.Fatalf("rega.NewRunner: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	t.Run("get_alarm_messages", func(t *testing.T) {
		msgs, err := runner.GetAlarmMessages(ctx)
		if err != nil {
			t.Fatalf("GetAlarmMessages: %v", err)
		}
		t.Logf("alarm messages: %d", len(msgs))
		for i, m := range msgs {
			t.Logf("  [%d] %+v", i, m)
			if m.ID == "" {
				t.Errorf("alarm[%d]: empty id", i)
			}
			// A raised alarm always carries its occurrence time; 0 here
			// would mean the *Seconds() accessor did not reach the record.
			if m.Timestamp == 0 {
				t.Errorf("alarm[%d] (%s): timestamp is 0", i, m.Name)
			}
		}
	})

	t.Run("get_service_messages", func(t *testing.T) {
		msgs, err := runner.GetServiceMessages(ctx)
		if err != nil {
			t.Fatalf("GetServiceMessages: %v", err)
		}
		t.Logf("service messages: %d", len(msgs))
		for i, m := range msgs {
			t.Logf("  [%d] %+v", i, m)
			if m.ID == "" {
				t.Errorf("service[%d]: empty id", i)
			}
		}
	})

	t.Run("get_program_descriptions", func(t *testing.T) {
		progs, err := runner.GetProgramDescriptions(ctx)
		if err != nil {
			t.Fatalf("GetProgramDescriptions: %v", err)
		}
		t.Logf("programs: %d", len(progs))
		if len(progs) == 0 {
			t.Skip("CCU has no programs — nothing to verify")
		}
		for i, p := range progs {
			if i < 5 {
				t.Logf("  [%d] %+v", i, p)
			}
		}
	})

	t.Run("get_system_variable_descriptions", func(t *testing.T) {
		vars, err := runner.GetSystemVariableDescriptions(ctx)
		if err != nil {
			t.Fatalf("GetSystemVariableDescriptions: %v", err)
		}
		t.Logf("system variables: %d", len(vars))
		if len(vars) == 0 {
			t.Skip("CCU has no system variables — nothing to verify")
		}
		for i, v := range vars {
			if i < 5 {
				t.Logf("  [%d] %+v", i, v)
			}
		}
	})

	t.Run("get_inbox_devices", func(t *testing.T) {
		devs, err := runner.GetInboxDevices(ctx)
		if err != nil {
			t.Fatalf("GetInboxDevices: %v", err)
		}
		t.Logf("inbox devices: %d", len(devs))
		for i, d := range devs {
			t.Logf("  [%d] %+v", i, d)
		}
	})

	t.Run("usage_by_sysvar", func(t *testing.T) {
		name := os.Getenv("OPENCCU_LOOM_LIVE_SYSVAR")
		if name == "" {
			t.Skip("set OPENCCU_LOOM_LIVE_SYSVAR to a system-variable name to exercise usage_by_sysvar")
		}
		progs, err := runner.SysvarUsagePrograms(ctx, name)
		if err != nil {
			t.Fatalf("SysvarUsagePrograms(%q): %v", name, err)
		}
		t.Logf("programs using %q: %d", name, len(progs))
		for i, p := range progs {
			t.Logf("  [%d] %+v", i, p)
		}
	})
}

// liveScriptParam binds a script placeholder to its value. env names the
// environment variable to read; fallback applies when env is unset or
// empty. A parameter with neither yields a skip, because substituting an
// empty string would exercise a different path than the caller uses.
type liveScriptParam struct {
	placeholder string
	env         string
	fallback    string
}

// liveReadScripts are the read-only scripts compared against their
// previous revision, with the parameter substitution each one needs.
var liveReadScripts = []struct {
	file   string
	params []liveScriptParam
	// behaviourChanged states why this script is expected to answer
	// differently than the recorded revision. Set it only for a
	// deliberate fix, and remove it once that revision is the baseline —
	// an entry left behind turns the regression detector off for good.
	behaviourChanged string
}{
	{file: "get_alarm_messages.fn"},
	{file: "get_service_messages.fn"},
	{file: "get_program_descriptions.fn"},
	{file: "get_system_variable_descriptions.fn"},
	{file: "get_inbox_devices.fn"},
	{file: "usage_by_sysvar.fn", params: []liveScriptParam{
		{placeholder: "##name##", env: "OPENCCU_LOOM_LIVE_SYSVAR"},
	}, behaviourChanged: "reads the program rules instead of the variable's usage index, which is empty on some CCUs"},
	// poll_com_test only reads LastTestCompletedTime; the epoch start
	// timestamp makes the comparison succeed on any device that ever ran
	// a test, which is what puts the read-back branch under test.
	// start_com_test is absent: it transmits a radio frame.
	{file: "poll_com_test.fn", params: []liveScriptParam{
		{placeholder: "##address##", env: "OPENCCU_LOOM_LIVE_DEVICE"},
		{placeholder: "##started##", fallback: "1970-01-01 00:00:00"},
	}},
}

// runRegaBody executes a raw script body on the CCU and returns its
// sanitised output.
func runRegaBody(ctx context.Context, client *jsonrpc.Client, body string) (string, error) {
	var out string
	if err := client.Call(ctx, regaRunScriptMethod, map[string]any{"script": body}, &out); err != nil {
		return "", err
	}
	return rega.SanitizeJSONControls(out), nil
}

// TestLive_RegaReadScriptsMatchPreviousRevision runs the working-tree
// version of each read-only script and the revision recorded in git side
// by side on the same CCU and requires identical output. This is what
// makes a control-flow refactor verifiable: a lost assignment, a guard
// that stopped firing, or a variable now leaking across loop iterations
// all change the emitted JSON.
//
// A mismatch is retried once with the order reversed before failing, so
// a genuine state change on the CCU between the two calls (an alarm
// raised mid-test) does not read as a regression.
func TestLive_RegaReadScriptsMatchPreviousRevision(t *testing.T) {
	env := checkLiveCCU(t)
	client := liveRegaClient(t, env)

	base := os.Getenv("OPENCCU_LOOM_LIVE_REGA_BASE")
	if base == "" {
		base = "HEAD"
	}

	for _, sc := range liveReadScripts {
		t.Run(strings.TrimSuffix(sc.file, ".fn"), func(t *testing.T) {
			current, err := os.ReadFile(regaScriptDir + "/" + sc.file)
			if err != nil {
				t.Fatalf("read working-tree script: %v", err)
			}
			gitPath := "internal/client/rega/scripts/" + sc.file
			raw, err := exec.Command("git", "show", base+":"+gitPath).Output()
			if err != nil {
				t.Skipf("no %s revision of %s: %v", base, gitPath, err)
			}

			newBody, oldBody := string(current), string(raw)
			if newBody == oldBody {
				t.Skip("identical to the recorded revision — nothing to compare")
			}

			for _, p := range sc.params {
				value := os.Getenv(p.env)
				if value == "" {
					value = p.fallback
				}
				if value == "" {
					t.Skipf("set %s to exercise %s", p.env, sc.file)
				}
				newBody = strings.ReplaceAll(newBody, p.placeholder, value)
				oldBody = strings.ReplaceAll(oldBody, p.placeholder, value)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()

			gotOld, err := runRegaBody(ctx, client, oldBody)
			if err != nil {
				t.Fatalf("run %s revision: %v", base, err)
			}
			gotNew, err := runRegaBody(ctx, client, newBody)
			if err != nil {
				t.Fatalf("run working-tree revision: %v", err)
			}
			if gotNew == gotOld {
				t.Logf("identical output (%d bytes)", len(gotNew))
				return
			}
			if sc.behaviourChanged != "" {
				t.Logf("output differs as intended — %s\n--- %s ---\n%s\n--- working tree ---\n%s",
					sc.behaviourChanged, base, truncateForLog(gotOld), truncateForLog(gotNew))
				return
			}

			// Reversed order: distinguishes a real difference from CCU
			// state that moved between the two calls.
			gotNew2, err := runRegaBody(ctx, client, newBody)
			if err != nil {
				t.Fatalf("run working-tree revision (retry): %v", err)
			}
			gotOld2, err := runRegaBody(ctx, client, oldBody)
			if err != nil {
				t.Fatalf("run %s revision (retry): %v", base, err)
			}
			if gotNew2 == gotOld2 {
				t.Logf("first pass differed, reversed pass identical — CCU state moved mid-test; treating as equal")
				return
			}

			t.Errorf("output differs between the %s revision and the working tree\n--- %s ---\n%s\n--- working tree ---\n%s",
				base, base, truncateForLog(gotOld2), truncateForLog(gotNew2))
		})
	}
}

// TestLive_RegaAlarmRecordPathWithRelaxedFilter exercises the record-
// building body of get_alarm_messages.fn even when no alarm is currently
// raised. A CCU without a pending alarm answers the unmodified script
// with "[]", which compares equal between any two revisions and proves
// nothing about the loop that fills a record.
//
// Both revisions are run with the AlState gate relaxed from "== 1" to
// ">= 0", so every ALARMDP object flows through the accessors, the
// nested object guard and the Write() chain. The script stays read-only:
// the substitution touches a comparison, and the body calls nothing but
// getters.
func TestLive_RegaAlarmRecordPathWithRelaxedFilter(t *testing.T) {
	env := checkLiveCCU(t)
	client := liveRegaClient(t, env)

	base := os.Getenv("OPENCCU_LOOM_LIVE_REGA_BASE")
	if base == "" {
		base = "HEAD"
	}
	const file = "get_alarm_messages.fn"

	current, err := os.ReadFile(regaScriptDir + "/" + file)
	if err != nil {
		t.Fatalf("read working-tree script: %v", err)
	}
	raw, err := exec.Command("git", "show", base+":internal/client/rega/scripts/"+file).Output()
	if err != nil {
		t.Skipf("no %s revision of %s: %v", base, file, err)
	}

	const gate, relaxed = "oVar.AlState() == 1", "oVar.AlState() >= 0"
	newBody := strings.ReplaceAll(string(current), gate, relaxed)
	oldBody := strings.ReplaceAll(string(raw), gate, relaxed)
	if !strings.Contains(newBody, relaxed) || !strings.Contains(oldBody, relaxed) {
		t.Fatalf("AlState gate %q not found — the script changed shape, adjust this test", gate)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	gotOld, err := runRegaBody(ctx, client, oldBody)
	if err != nil {
		t.Fatalf("run %s revision: %v", base, err)
	}
	gotNew, err := runRegaBody(ctx, client, newBody)
	if err != nil {
		t.Fatalf("run working-tree revision: %v", err)
	}

	var records []rega.AlarmMessage
	if err := json.Unmarshal([]byte(gotNew), &records); err != nil {
		t.Fatalf("working-tree output is not valid AlarmMessage JSON: %v\n%s", err, truncateForLog(gotNew))
	}
	t.Logf("ALARMDP objects traversed: %d", len(records))
	for i, r := range records {
		if i < 5 {
			t.Logf("  [%d] %+v", i, r)
		}
	}
	if len(records) == 0 {
		t.Skip("CCU holds no ALARMDP system variables — record path not reachable here")
	}

	if gotNew != gotOld {
		t.Errorf("record path differs between the %s revision and the working tree\n--- %s ---\n%s\n--- working tree ---\n%s",
			base, base, truncateForLog(gotOld), truncateForLog(gotNew))
	} else {
		t.Logf("identical output over %d records (%d bytes)", len(records), len(gotNew))
	}
}

// truncateForLog caps a CCU response so a large program list stays
// readable in the test output.
func truncateForLog(s string) string {
	const limit = 4000
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "… (truncated)"
}
