# Plan C4 — Combined-parameter auto-routing

**Status:** ⚠️ **already implemented — this plan is a verification +
documentation-correction task, not a build task.** Self-contained.

## Summary

The intended change ("route `COMBINED_PARAMETER` / `LEVEL_COMBINED`
writes through `CommandTracker.AddCombinedParameter` instead of always
`AddSetValue`") was assumed to be an open gap per by-design entry
**A4-P01**. Code verification shows the routing **already exists** in the
north-bound optimistic-write path. The remaining work is therefore:

1. confirm the routing covers the production path (done below),
2. flip `docs/parity/by_design.md` §A4-P01 to **RESOLVED** (with the
   stale claim corrected),
3. optionally reconcile two parallel "is convertable?" helpers, and
4. add a regression test that pins the routing so it cannot silently
   regress.

No behavioural change to the write path is needed.

## Current state (verified)

The optimistic-update path already auto-routes convertable parameters:

- `internal/client/interface_client_orchestration.go:243` —
  `func (c *InterfaceClient) WriteUnconfirmedValue(channelAddress string,
  parameter hmenum.Parameter, paramsetKey hmenum.ParamsetKey, value any)`:
  ```go
  if paramconvert.IsConvertable(parameter) {
      if s, ok := value.(string); ok {
          c.CommandTracker().AddCombinedParameter(channelAddress, string(parameter), s)
          return
      }
  }
  c.CommandTracker().AddSetValue(channelAddress, parameter, paramsetKey, value)
  ```
  Its doc comment already states it mirrors the Python `add_set_value`
  routing of convertable parameters through `add_combined_parameter`.
- This is the function the production optimistic hook calls. The
  north-bound write path is `ValueWriter.SetValue` →
  `SetValueWithOptions` → the `CommandTrackerFn` installed at
  `cmd/openccu-loom/daemon_wiring.go:130`
  (`valueWriter.SetCommandTrackerFn(...)`), whose closure
  (per the doc at `internal/client/value_writer.go:208-215`) "looks up
  the IC via the central registry and calls
  `InterfaceClient.WriteUnconfirmedValue`." So the call-site that
  by-design A4-P01 said "always calls `AddSetValue`" in fact routes
  combined params correctly.
- The tracker methods exist and behave as required:
  `internal/client/reliability/command_tracker.go:91 AddSetValue`,
  `:122 AddCombinedParameter` (parses the combined wire string via
  `backends.ParseCombinedParameter` and delegates to `AddPutParamset`,
  recording each constituent sub-parameter under `ParamsetKeyValues`).
- `internal/model/value/converter.go:32` defines
  `ConvertableParameters = [COMBINED_PARAMETER, LEVEL_COMBINED]` and
  `:37 IsConvertableParameter(p)`. **Note:** the orchestration call-site
  uses a *different* helper, `paramconvert.IsConvertable`, not
  `value.IsConvertableParameter`. Two parallel predicates exist.

Stale documentation:

- `docs/parity/by_design.md` §A4-P01 (~line 2249) still claims
  "`InterfaceClient.SetValue` always calls `AddSetValue` without
  auto-routing." This is **no longer true** for the optimistic path
  (`WriteUnconfirmedValue`). The entry must be corrected.

## Design decisions

1. **Do not change the write path.** Routing is correct and matches the
   Python reference semantics. Touching it risks regressing optimistic
   reads on constituent DPs (LEVEL / LEVEL_2).
2. **Treat this as a documentation + guard task.** The value the original
   request sought (proactive close of A4-P01) is delivered by marking the
   entry RESOLVED and pinning the behaviour with a test.
3. **Reconcile the duplicate predicate (optional, recommended).** Decide
   between `paramconvert.IsConvertable` and `value.IsConvertableParameter`
   and converge on one to avoid drift where one list gains a parameter the
   other lacks. Lowest-risk option: keep the call-site helper
   (`paramconvert.IsConvertable`) and make `value.IsConvertableParameter`
   delegate to it (or vice-versa), so the parameter set is defined once.
   Verify both currently list the same two parameters before merging.

## Implementation steps

1. **Confirm the daemon hook target.** Read
   `cmd/openccu-loom/daemon_wiring.go:130` and confirm the
   `SetCommandTrackerFn` closure calls `WriteUnconfirmedValue` (not
   `AddSetValue` directly). If — contrary to the doc — it calls
   `AddSetValue` directly, that *is* a real gap: change the closure to
   call `WriteUnconfirmedValue` so routing applies. (Expected: already
   correct; this is the one scenario that would turn this into a real
   code fix.)
2. **Add a regression test** (`command_tracker` or orchestration test):
   call `WriteUnconfirmedValue` with `COMBINED_PARAMETER` + a valid
   combined wire string and assert the tracker holds the **decomposed**
   constituent DataPointKeys (via `GetLastSentValue` on a sub-parameter),
   not the raw combined string; and that a plain parameter still records
   one `AddSetValue` entry. Also cover `LEVEL_COMBINED`.
3. **Reconcile predicates (optional).** Unify
   `paramconvert.IsConvertable` and `value.IsConvertableParameter` onto a
   single source-of-truth list. Add a tiny test asserting both report the
   same set (or that one delegates to the other).
4. **Flip the by-design entry.** Edit `docs/parity/by_design.md` §A4-P01
   to RESOLVED: state that `WriteUnconfirmedValue`
   (`interface_client_orchestration.go`) routes convertable parameters
   through `AddCombinedParameter`, wired via the daemon optimistic hook
   (`daemon_wiring.go`). Correct the stale "always calls AddSetValue"
   sentence.

## Config / API / Doc changes

- **No config change**, no new `cfg:` field → `TestConfigFieldsHaveLabelsAndHelp`
  unaffected.
- **No REST/WS contract change** → no `make export-schemas`, no
  `APIVersion` bump.
- **Doc only:** `docs/parity/by_design.md` §A4-P01 → RESOLVED.

## Project-rule checklist

- [ ] `CommandPriority.Critical == 0` rule respected: the routing change
      touches only tracker recording, not priority handling; priority is
      threaded through `SetValueWithOptions` unchanged. **Do not** add any
      `if priority != 0` test.
- [ ] No CGo; pure-Go.
- [ ] Multi-CCU-safe: `WriteUnconfirmedValue` is per-`InterfaceClient`
      (one per `(central, interface)`); no global state.
- [ ] `context.Context` discipline unchanged (tracker recording is
      synchronous, no I/O).
- [ ] No `panic`.
- [ ] If a protocol/capability boundary were touched, a contract test
      would be required — here it is not, but the regression test in
      step 2 is mandatory.
- [ ] `make test` green incl. `TestDocPurity`.

## Acceptance criteria

- A `COMBINED_PARAMETER` (and `LEVEL_COMBINED`) optimistic write records
  the **decomposed** constituent DataPointKeys in the `CommandTracker`,
  proven by a committed regression test, so a subsequent north-bound read
  on a constituent DP (e.g. `LEVEL`) returns the optimistic value.
- `docs/parity/by_design.md` §A4-P01 reads RESOLVED with the corrected
  description.
- (If reconciled) a single predicate defines the convertable-parameter
  set, with a test pinning both call-sites to it.

## Effort

**XS** (verify + test + doc correction). Becomes **S** only if step 1
uncovers that the daemon hook bypasses `WriteUnconfirmedValue` (not
expected).

## References

- `CLAUDE.md` → "Critical Rules → `CommandPriority.Critical = 0`".
- `docs/parity/by_design.md` §A4-P01 (the entry this plan resolves).
- Code: `internal/client/interface_client_orchestration.go:243`
  (`WriteUnconfirmedValue`), `internal/client/value_writer.go:208`
  (`SetCommandTrackerFn`), `cmd/openccu-loom/daemon_wiring.go:130`
  (hook wiring), `internal/client/reliability/command_tracker.go:91/122`,
  `internal/model/value/converter.go:32/37`.
