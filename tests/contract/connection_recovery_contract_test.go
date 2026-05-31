// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

// connection_recovery_contract_test.go defines the stable API contract for
// ConnectionRecoveryCoordinator.
//
// STABILITY GUARANTEE: Any change that breaks these tests requires a MAJOR
// version bump.
//
// The contract ensures that: 1. DefaultMaxRecoveryAttempts and backoff
// constants are stable. 2. RecoveryStage enum values are stable. 3.
// InterfaceRecoveryState counters behave correctly. 4. Exponential backoff
// formula is correct. 5. Stage transitions are recorded correctly. 6. Full
// recovery cycles (success + failure + exhaustion) work.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/coordinators"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// =============================================================================
// Contract: Constants Stability
// =============================================================================

// TestRecoveryContractDefaultMaxAttemptsIs8 pins DefaultMaxRecoveryAttempts
// at the reference value (MAX_RECOVERY_ATTEMPTS = 8). An earlier divergence
// (10 attempts) was unwound on parity-audit feedback because allowing two
// extra retries before FAILED gave no operational benefit and lengthened the
// window in which a wedged CCU saw repeated init() probes.
func TestRecoveryContractDefaultMaxAttemptsIs8(t *testing.T) {
	t.Parallel()
	if coordinators.DefaultMaxRecoveryAttempts != 8 {
		t.Fatalf("DefaultMaxRecoveryAttempts = %d, want 8", coordinators.DefaultMaxRecoveryAttempts)
	}
}

// TestRecoveryContractBaseRetryDelayIs2s pins the base retry delay constant.
// Base is 2 s: a freshly rebooted CCU often answers quickly, so we
// retry while the wire is still warm.
func TestRecoveryContractBaseRetryDelayIs2s(t *testing.T) {
	t.Parallel()
	c := newRecoveryCoord(t)
	// Verify via NextRetryDelay before any failures: must equal the base delay.
	if got := c.NextRetryDelay("iface"); got != 2*time.Second {
		t.Fatalf("base retry delay = %v, want 2s", got)
	}
}

// TestRecoveryContractMaxRetryDelayIs120s pins the saturation cap.
// A CCU down longer than two minutes typically needs a full ReGa startup;
// retrying sooner provides no benefit.
func TestRecoveryContractMaxRetryDelayIs120s(t *testing.T) {
	t.Parallel()
	c := newRecoveryCoord(t)
	// Drive many failures; delay must saturate at 120 s.
	for i := 0; i < 20; i++ {
		runFailing(t, c, "iface")
	}
	if got := c.NextRetryDelay("iface"); got != 120*time.Second {
		t.Fatalf("saturated retry delay = %v, want 120s", got)
	}
}

// =============================================================================
// Contract: RecoveryStage Enum Stability
// =============================================================================

// TestRecoveryContractStageEnumValuesAreStable pins every RecoveryStage value.
// Python contract: RecoveryStage.{IDLE,COOLDOWN,TCP_CHECKING,...}.value unchanged.
func TestRecoveryContractStageEnumValuesAreStable(t *testing.T) {
	t.Parallel()
	cases := []struct {
		stage hmenum.RecoveryStage
		want  string
	}{
		{hmenum.RecoveryStageIdle, "idle"},
		{hmenum.RecoveryStageCooldown, "cooldown"},
		{hmenum.RecoveryStageTCPChecking, "tcp_checking"},
		{hmenum.RecoveryStageRPCChecking, "rpc_checking"},
		{hmenum.RecoveryStageWarmingUp, "warming_up"},
		{hmenum.RecoveryStageStabilityCheck, "stability_check"},
		{hmenum.RecoveryStageReconnecting, "reconnecting"},
		{hmenum.RecoveryStageDataLoading, "data_loading"},
		{hmenum.RecoveryStageRecovered, "recovered"},
		{hmenum.RecoveryStageFailed, "failed"},
		{hmenum.RecoveryStageHeartbeat, "heartbeat"},
		{hmenum.RecoveryStageDetecting, "detecting"},
	}
	for _, tc := range cases {
		if got := tc.stage.String(); got != tc.want {
			t.Errorf("RecoveryStage(%q).String() = %q, want %q", tc.stage, got, tc.want)
		}
	}
}

// =============================================================================
// Contract: InterfaceRecoveryState Counters
// =============================================================================

// TestRecoveryContractInitialStateIsZero pins the zero-value contract.
// Python contract: initial attempt_count == 0, consecutive_failures == 0.
func TestRecoveryContractInitialStateIsZero(t *testing.T) {
	t.Parallel()
	c := newRecoveryCoord(t)
	st := c.State("never-seen")
	if st.Attempts != 0 {
		t.Errorf("Attempts = %d, want 0", st.Attempts)
	}
	if st.ConsecutiveFailures != 0 {
		t.Errorf("ConsecutiveFailures = %d, want 0", st.ConsecutiveFailures)
	}
	if !st.LastAttempt.IsZero() {
		t.Errorf("LastAttempt = %v, want zero", st.LastAttempt)
	}
}

// TestRecoveryContractFailureIncrementsCounters pins failure counting.
// Python contract: record_failure increments both attempt_count and
// consecutive_failures independently.
func TestRecoveryContractFailureIncrementsCounters(t *testing.T) {
	t.Parallel()
	c := newRecoveryCoord(t)
	runFailing(t, c, "iface")
	st := c.State("iface")
	if st.Attempts != 1 {
		t.Errorf("Attempts after 1 failure = %d, want 1", st.Attempts)
	}
	if st.ConsecutiveFailures != 1 {
		t.Errorf("ConsecutiveFailures after 1 failure = %d, want 1", st.ConsecutiveFailures)
	}

	runFailing(t, c, "iface")
	st = c.State("iface")
	if st.Attempts != 2 {
		t.Errorf("Attempts after 2 failures = %d, want 2", st.Attempts)
	}
	if st.ConsecutiveFailures != 2 {
		t.Errorf("ConsecutiveFailures after 2 failures = %d, want 2", st.ConsecutiveFailures)
	}
}

// TestRecoveryContractSuccessResetsConsecutiveFailures pins the reset contract.
// Python contract: record_success resets consecutive_failures to 0 but
// total attempt_count still increments.
func TestRecoveryContractSuccessResetsConsecutiveFailures(t *testing.T) {
	t.Parallel()
	c := newRecoveryCoord(t)
	runFailing(t, c, "iface")
	runFailing(t, c, "iface")
	runSucceeding(t, c, "iface")
	st := c.State("iface")
	if st.ConsecutiveFailures != 0 {
		t.Errorf("ConsecutiveFailures after success = %d, want 0", st.ConsecutiveFailures)
	}
	if st.Attempts != 3 {
		t.Errorf("Attempts (2 failures + 1 success) = %d, want 3", st.Attempts)
	}
}

// TestRecoveryContractResetAttemptsZerosCounters pins ResetAttempts.
// Python contract: reset() clears attempt_count, consecutive_failures.
func TestRecoveryContractResetAttemptsZerosCounters(t *testing.T) {
	t.Parallel()
	c := newRecoveryCoord(t)
	runFailing(t, c, "iface")
	runFailing(t, c, "iface")
	c.ResetAttempts("iface")
	st := c.State("iface")
	if st.ConsecutiveFailures != 0 {
		t.Errorf("ConsecutiveFailures after reset = %d, want 0", st.ConsecutiveFailures)
	}
}

// TestRecoveryContractCanRetryTrueInitially pins the can_retry equivalent.
// Python contract: can_retry is True when attempt_count < MAX_RECOVERY_ATTEMPTS.
func TestRecoveryContractCanRetryTrueInitially(t *testing.T) {
	t.Parallel()
	c := newRecoveryCoordWithLimit(t, 3)
	// Before any attempt, a run must proceed (not be blocked by the cap).
	called := false
	c.Run(context.Background(), "iface", []coordinators.Pipeline{{
		Stage: hmenum.RecoveryStageReconnecting,
		Run: func(_ context.Context) error {
			called = true
			return nil
		},
	}})
	if !called {
		t.Fatal("first run must proceed — can_retry should be true initially")
	}
}

// TestRecoveryContractCanRetryFalseAtMaxAttempts pins the exhaustion contract.
// Python contract: can_retry is False when attempt_count >= MAX_RECOVERY_ATTEMPTS.
func TestRecoveryContractCanRetryFalseAtMaxAttempts(t *testing.T) {
	t.Parallel()
	const limit = 3
	c := newRecoveryCoordWithLimit(t, limit)
	for i := 0; i < limit; i++ {
		runFailing(t, c, "iface")
	}
	// Next run: pipeline step must NOT be called (cap exhausted).
	called := false
	res := c.Run(context.Background(), "iface", []coordinators.Pipeline{{
		Stage: hmenum.RecoveryStageReconnecting,
		Run:   func(_ context.Context) error { called = true; return nil },
	}})
	if called {
		t.Error("step ran after cap exhausted — can_retry must be false")
	}
	if res != hmenum.RecoveryResultFailed {
		t.Errorf("result after cap = %v, want failed", res)
	}
}

// =============================================================================
// Contract: Exponential Backoff Formula
// =============================================================================

// TestRecoveryContractBackoffSequence pins the sequence 2,2,4,8,16,32,64,120,120.
// Formula: min(max, base * 2^(attempts-1)); base=2s, max=120s.
func TestRecoveryContractBackoffSequence(t *testing.T) {
	t.Parallel()
	c := newRecoveryCoord(t)
	// Default bounds: base=2s, max=120s.
	expected := []time.Duration{
		2 * time.Second,   // 0 failures → base
		2 * time.Second,   // 1 failure  → base * 2^0 = 2
		4 * time.Second,   // 2 failures → base * 2^1 = 4
		8 * time.Second,   // 3 failures → base * 2^2 = 8
		16 * time.Second,  // 4 failures → base * 2^3 = 16
		32 * time.Second,  // 5 failures → base * 2^4 = 32
		64 * time.Second,  // 6 failures → base * 2^5 = 64
		120 * time.Second, // 7 failures → base * 2^6 = 128, capped to 120
		120 * time.Second, // 8 failures → 120 (saturated)
	}

	for i, want := range expected {
		if got := c.NextRetryDelay("iface"); got != want {
			t.Errorf("step %d: NextRetryDelay = %v, want %v", i, got, want)
		}
		if i < len(expected)-1 {
			runFailing(t, c, "iface")
		}
	}
}

// TestRecoveryContractDelayCapAtMax pins cap enforcement.
// Delay must never exceed MAX_RETRY_DELAY (120s).
func TestRecoveryContractDelayCapAtMax(t *testing.T) {
	t.Parallel()
	c := newRecoveryCoord(t)
	for i := 0; i < 15; i++ {
		runFailing(t, c, "iface")
	}
	got := c.NextRetryDelay("iface")
	if got != 120*time.Second {
		t.Errorf("saturated delay = %v, want 120s", got)
	}
	if got > 120*time.Second {
		t.Errorf("delay exceeded cap: %v", got)
	}
}

// TestRecoveryContractFirstFailureDelayIsBase pins the base retry delay.
// After the first failure the formula yields base * 2^0 = 2s.
func TestRecoveryContractFirstFailureDelayIsBase(t *testing.T) {
	t.Parallel()
	c := newRecoveryCoord(t)
	runFailing(t, c, "iface")
	if got := c.NextRetryDelay("iface"); got != 2*time.Second {
		t.Errorf("delay after 1 failure = %v, want 2s (base * 2^0)", got)
	}
}

// TestRecoveryContractThirdFailureDelay pins the 2-failure step.
// Formula: base * 2^1 = 2 * 2 = 4s.
func TestRecoveryContractThirdFailureDelay(t *testing.T) {
	t.Parallel()
	c := newRecoveryCoord(t)
	runFailing(t, c, "iface")
	runFailing(t, c, "iface")
	if got := c.NextRetryDelay("iface"); got != 4*time.Second {
		t.Errorf("delay after 2 failures = %v, want 4s (base * 2^1)", got)
	}
}

// TestRecoveryContractFourthFailureDelay pins the 3-failure step.
// Formula: base * 2^2 = 2 * 4 = 8s.
func TestRecoveryContractFourthFailureDelay(t *testing.T) {
	t.Parallel()
	c := newRecoveryCoord(t)
	runFailing(t, c, "iface")
	runFailing(t, c, "iface")
	runFailing(t, c, "iface")
	if got := c.NextRetryDelay("iface"); got != 8*time.Second {
		t.Errorf("delay after 3 failures = %v, want 8s (base * 2^2)", got)
	}
}

// =============================================================================
// Contract: Full Recovery Cycles
// =============================================================================

// TestRecoveryContractFailedCycleIncrementCounters pins the failed-cycle flow.
// Python contract: failed recovery cycle increments attempt counters.
func TestRecoveryContractFailedCycleIncrementCounters(t *testing.T) {
	t.Parallel()
	c := newRecoveryCoord(t)
	runFailing(t, c, "iface")
	st := c.State("iface")
	if st.Attempts != 1 {
		t.Errorf("Attempts after failed cycle = %d, want 1", st.Attempts)
	}
	if st.ConsecutiveFailures != 1 {
		t.Errorf("ConsecutiveFailures after failed cycle = %d, want 1", st.ConsecutiveFailures)
	}
}

// TestRecoveryContractMaxAttemptsExhausted pins the exhaustion flow.
// Python contract: after MAX_RECOVERY_ATTEMPTS, can_retry is False.
func TestRecoveryContractMaxAttemptsExhausted(t *testing.T) {
	t.Parallel()
	const limit = 4
	c := newRecoveryCoordWithLimit(t, limit)
	for i := 0; i < limit; i++ {
		runFailing(t, c, "iface")
	}
	st := c.State("iface")
	if st.ConsecutiveFailures != limit {
		t.Errorf("ConsecutiveFailures = %d, want %d", st.ConsecutiveFailures, limit)
	}
	// Next run must be blocked.
	called := false
	c.Run(context.Background(), "iface", []coordinators.Pipeline{{
		Stage: hmenum.RecoveryStageDetecting,
		Run:   func(_ context.Context) error { called = true; return nil },
	}})
	if called {
		t.Error("step ran after exhaustion — must not proceed")
	}
}

// TestRecoveryContractSuccessfulCycleResetsConsecutive pins the success-cycle flow.
// Python contract: successful recovery cycle resets consecutive_failures and
// stamps last_success.
func TestRecoveryContractSuccessfulCycleResetsConsecutive(t *testing.T) {
	t.Parallel()
	c := newRecoveryCoord(t)
	runFailing(t, c, "iface")
	runSucceeding(t, c, "iface")
	st := c.State("iface")
	if st.ConsecutiveFailures != 0 {
		t.Errorf("ConsecutiveFailures after success = %d, want 0", st.ConsecutiveFailures)
	}
	if st.LastSuccess.IsZero() {
		t.Error("LastSuccess not stamped after successful cycle")
	}
}

// TestRecoveryContractNormalStageOrder pins the eight-stage progression order.
// Python contract: IDLE→COOLDOWN→TCP_CHECKING→RPC_CHECKING→WARMING_UP→
// STABILITY_CHECK→RECONNECTING→DATA_LOADING→RECOVERED
func TestRecoveryContractNormalStageOrder(t *testing.T) {
	t.Parallel()
	expectedStages := []hmenum.RecoveryStage{
		hmenum.RecoveryStageCooldown,
		hmenum.RecoveryStageTCPChecking,
		hmenum.RecoveryStageRPCChecking,
		hmenum.RecoveryStageWarmingUp,
		hmenum.RecoveryStageStabilityCheck,
		hmenum.RecoveryStageReconnecting,
		hmenum.RecoveryStageDataLoading,
		hmenum.RecoveryStageRecovered,
	}
	pipeline := coordinators.DefaultRecoveryPipeline(coordinators.RecoveryStageDeps{})
	if len(pipeline) < len(expectedStages) {
		t.Fatalf("DefaultRecoveryPipeline has %d stages, want at least %d", len(pipeline), len(expectedStages))
	}
	for i, want := range expectedStages {
		if pipeline[i].Stage != want {
			t.Errorf("stage[%d] = %v, want %v", i, pipeline[i].Stage, want)
		}
	}
}

// TestRecoveryContractDefaultPipelineRunsAllStages pins that the default
// pipeline executes all 8 stages on a no-op run.
func TestRecoveryContractDefaultPipelineRunsAllStages(t *testing.T) {
	t.Parallel()
	c := newRecoveryCoord(t)
	var executed []hmenum.RecoveryStage
	pipeline := coordinators.DefaultRecoveryPipeline(coordinators.RecoveryStageDeps{
		TCPProbe: func(_ context.Context) error {
			executed = append(executed, hmenum.RecoveryStageTCPChecking)
			return nil
		},
		RPCProbe: func(_ context.Context) error {
			executed = append(executed, hmenum.RecoveryStageRPCChecking)
			return nil
		},
		Reconnect: func(_ context.Context) error {
			executed = append(executed, hmenum.RecoveryStageReconnecting)
			return nil
		},
		LoadData: func(_ context.Context) error {
			executed = append(executed, hmenum.RecoveryStageDataLoading)
			return nil
		},
	})
	res := c.Run(context.Background(), "iface", pipeline)
	if res != hmenum.RecoveryResultSuccess {
		t.Fatalf("full pipeline result = %v, want success", res)
	}
	// Must have run at least the 4 probed stages.
	probed := map[hmenum.RecoveryStage]bool{}
	for _, s := range executed {
		probed[s] = true
	}
	for _, want := range []hmenum.RecoveryStage{
		hmenum.RecoveryStageTCPChecking,
		hmenum.RecoveryStageRPCChecking,
		hmenum.RecoveryStageReconnecting,
		hmenum.RecoveryStageDataLoading,
	} {
		if !probed[want] {
			t.Errorf("stage %v was not executed", want)
		}
	}
}

// TestRecoveryContractResetReleasesExhaustedCap pins that ResetAttempts
// re-enables runs that were blocked by the cap.
func TestRecoveryContractResetReleasesExhaustedCap(t *testing.T) {
	t.Parallel()
	const limit = 2
	c := newRecoveryCoordWithLimit(t, limit)
	for i := 0; i < limit; i++ {
		runFailing(t, c, "iface")
	}
	c.ResetAttempts("iface")
	// After reset, a run must proceed.
	called := false
	res := c.Run(context.Background(), "iface", []coordinators.Pipeline{{
		Stage: hmenum.RecoveryStageDetecting,
		Run:   func(_ context.Context) error { called = true; return nil },
	}})
	if !called {
		t.Error("step not called after ResetAttempts — cap must be released")
	}
	if res != hmenum.RecoveryResultSuccess {
		t.Errorf("result after reset = %v, want success", res)
	}
}

// TestRecoveryContractHistoryCapsBound pins the ring-buffer cap.
// Python contract: no direct equivalent, but the ring is bounded.
func TestRecoveryContractHistoryCapsBound(t *testing.T) {
	t.Parallel()
	c := newRecoveryCoordWithLimit(t, 0) // unlimited attempts
	for i := 0; i < 30; i++ {
		runFailing(t, c, "iface")
	}
	hist := c.History("iface")
	if len(hist) > 25 {
		t.Errorf("history len = %d, want <= 25 (ring cap)", len(hist))
	}
	if len(hist) == 0 {
		t.Error("history must not be empty")
	}
}

// TestRecoveryContractClassifyOverridesFailureReason pins Classify wiring.
// Python contract: no direct equivalent — Go-specific contract.
func TestRecoveryContractClassifyOverridesFailureReason(t *testing.T) {
	t.Parallel()
	c := newRecoveryCoord(t)
	c.Run(context.Background(), "iface", []coordinators.Pipeline{{
		Stage: hmenum.RecoveryStageDetecting,
		Run:   func(_ context.Context) error { return errors.New("auth boom") },
		Classify: func(_ error) *hmenum.FailureReason {
			r := hmenum.FailureReasonAuth
			return &r
		},
	}})
	hist := c.History("iface")
	if len(hist) == 0 {
		t.Fatal("no history entry recorded")
	}
	if hist[0].Reason != hmenum.FailureReasonAuth {
		t.Errorf("failure reason = %v, want auth", hist[0].Reason)
	}
}

// =============================================================================
// Helpers
// =============================================================================

func newRecoveryCoord(t *testing.T) *coordinators.ConnectionRecoveryCoordinator {
	t.Helper()
	bus := events.NewBus()
	return coordinators.NewConnectionRecoveryCoordinator("contract-ccu", bus)
}

func newRecoveryCoordWithLimit(t *testing.T, limit int) *coordinators.ConnectionRecoveryCoordinator {
	t.Helper()
	bus := events.NewBus()
	return coordinators.NewConnectionRecoveryCoordinatorWithLimit("contract-ccu", bus, limit)
}

func runFailing(t *testing.T, c *coordinators.ConnectionRecoveryCoordinator, iface string) {
	t.Helper()
	c.Run(context.Background(), iface, []coordinators.Pipeline{{
		Stage: hmenum.RecoveryStageDetecting,
		Run:   func(_ context.Context) error { return errors.New("injected failure") },
	}})
}

func runSucceeding(t *testing.T, c *coordinators.ConnectionRecoveryCoordinator, iface string) {
	t.Helper()
	c.Run(context.Background(), iface, []coordinators.Pipeline{{
		Stage: hmenum.RecoveryStageDetecting,
		Run:   func(_ context.Context) error { return nil },
	}})
}
