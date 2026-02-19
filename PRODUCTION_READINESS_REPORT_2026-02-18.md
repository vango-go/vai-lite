# vai-lite Production Readiness Report
Date: 2026-02-18
Author: Codex audit run (this session)

## Scope
This report is an exhaustive summary of all production-readiness gaps observed in this session across:
1. Code audit of `sdk`, `pkg/core`, provider adapters, and streaming/run loop logic.
2. Unit/race/vet/coverage baselines.
3. Integration execution for non-Google providers.
4. Gemini and Gemini OAuth integration tests run one-by-one.

## Executive Verdict
`vai-lite` is **not yet production-ready**.

The project has strong momentum and broad test volume. P0 correctness items from this report are implemented, and key P1/P2 hardening work is now in place. Production readiness is still blocked by live-provider validation gaps (especially Gemini OAuth capacity behavior under sustained load) and remaining provider coverage depth.

## What Was Executed In This Session
1. `go test ./...` (pass)
2. `go test -race ./...` (pass)
3. `go vet ./...` (pass)
4. `go test ./... -coverprofile=/tmp/vai_lite_cover.out` and `go tool cover -func` (total statements: `27.3%`)
5. Integration compile check: `go test -tags=integration ./... -run TestDoesNotExist` (pass)
6. Non-Google integration matrix rerun:
   - `go test -tags=integration ./integration -count=1 -timeout=45m -run '^TestMessages_Run.*/(anthropic|oai-resp|groq)$' -v`
   - Result: fails overall due `TestMessages_Run_MaxTurnsLimit/groq` (`tool_use_failed`)
7. Gemini and Gemini OAuth one-by-one rerun:
   - Per-subtest execution (1 `go test` call per subtest/provider)
   - Summary file: `/tmp/gemini_individual_rerun/summary.txt`
   - Gemini: `16 pass / 1 fail`
   - Gemini OAuth: `14 pass / 3 fail`
8. Post-hardening rerun (this update):
   - `go test ./...` (pass)
   - `go test -tags=integration ./integration -run TestDoesNotExist` (pass)
   - `go test ./... -coverprofile=/tmp/vai_lite_cover_after.out` (total statements: `32.7%`)

## Current Test Quality Snapshot
1. Test count is high (`192` test functions discovered), but execution confidence is uneven.
2. Integration tests are build-tagged (`//go:build integration`) and are not part of default `go test ./...` runs.
3. Provider-direct coverage is improved but still uneven:
   - `pkg/core/providers/openai`: `37.0%`
   - `pkg/core/providers/oai_resp`: `36.7%`
   - `pkg/core/providers/groq`: `68.8%`
   - `pkg/core/providers/cerebras`: `68.8%`
   - `pkg/core/providers/gemini`: `34.0%`
   - `pkg/core/providers/gemini_oauth`: `21.6%`
   - `pkg/core/voice/stt`: `11.0%`
   - `pkg/core/voice/tts`: `7.7%`
4. Total statement coverage improved from `27.3%` to `32.7%`; still below a comfortable production target for a multi-provider, streaming tool-loop SDK.

## Pending Items (Prioritized)

## Progress Update (2026-02-19)
1. Completed: `P0-1`, `P0-2`, `P0-3`, `P0-4`, `P1-1`, `P1-3`, `P1-5`, `P2-1`.
2. In progress: `P1-2`, `P1-4`, `P1-6`, `P2-2`, `P2-3`.
3. Remaining release risk: live-provider validation for Gemini OAuth capacity handling and broader provider translation/parsing coverage depth.

## P0 - Release Blocking Correctness

### P0-1: `RunStream.Close()` can strand event consumers and leave run goroutine active [Status: Done (2026-02-19)]
- Evidence:
  - `sdk/run.go:1064` (`run` closes both `events` and `done` via `closeOnce`).
  - `sdk/run.go:1638` (`Close()` uses same `closeOnce`, closes only `done`).
  - `sdk/run.go:1603` (`send` stops sending once closed).
- Risk:
  - If caller invokes `Close()` before `run()` exits, `events` may never close.
  - Callers ranging `RunStream.Events()` can block indefinitely.
- Required fix:
  1. Make close semantics single-owner and deterministic.
  2. Ensure `events` always closes exactly once.
  3. Add regression tests where `Close()` is called mid-stream and assert channel closure + goroutine completion.
- Progress update:
  1. `sdk/run.go`: introduced deterministic `finish()` ownership, context cancellation wiring, and close/wait semantics in `RunStream.Close()`.
  2. `sdk/run_test.go`: added `TestRunStream_Close_MidStream_ClosesEventsAndFinishes`.

### P0-2: Terminal stream event can be dropped when provider returns `(event, io.EOF)` [Status: Done (2026-02-19)]
- Evidence:
  - `sdk/stream.go:169-174` breaks immediately on any non-nil error.
  - `pkg/core/providers/openai/stream.go:275-292` returns final `MessageDeltaEvent` with `io.EOF`.
  - `pkg/core/providers/gemini_oauth/stream.go:362-377` same pattern.
- Risk:
  - Final `stop_reason`/usage may be lost.
  - `RunStream` may misclassify completion and skip tool-loop continuation paths.
- Required fix:
  1. Consume event if present even when `err==io.EOF`.
  2. Normalize provider stream contracts or harden SDK stream reader.
  3. Add tests for `(event, io.EOF)` behavior for all stream providers.
- Progress update:
  1. `sdk/stream.go`: reader now consumes event first, then handles `err` (including EOF).
  2. `pkg/core/providers/openai/stream.go` and `pkg/core/providers/gemini_oauth/stream.go`: final event/EOF behavior normalized.
  3. Added terminal-event/EOF regression tests in `sdk/stream_test.go`, `sdk/run_stream_semantics_test.go`, and provider stream test files for OpenAI, Gemini, Gemini OAuth, Anthropic, and OAI Responses.

### P0-3: Cancellation is conflated with timeout in run loops [Status: Done (2026-02-19)]
- Evidence:
  - `sdk/run.go:320`, `sdk/run.go:1192`, `sdk/run.go:1277` set `RunStopTimeout` on any `ctx.Done()`.
  - `RunStopCancelled` exists but is not used for context cancellation paths.
- Risk:
  - Observability and control-flow ambiguity.
  - Callers cannot distinguish client-initiated cancel from timeout deadline.
- Required fix:
  1. Map `context.Canceled` to `RunStopCancelled`.
  2. Map `context.DeadlineExceeded` to `RunStopTimeout`.
  3. Add explicit tests for both outcomes in `Run` and `RunStream`.
- Progress update:
  1. `sdk/run.go`: added context error mapping helper and applied it in run + stream loops.
  2. `sdk/run_test.go`: added explicit cancel vs timeout tests for both `Run` and `RunStream`.

### P0-4: Usage aggregation can report `TotalTokens=0` despite nonzero input/output [Status: Done (2026-02-19)]
- Evidence:
  - Live integration log: `run_test.go:345: Usage: input=1283, output=173, total=0` for Anthropic.
  - `pkg/core/types/usage.go:14-19` sums `TotalTokens` fields directly; no fallback recompute from input/output.
- Risk:
  - Broken billing/accounting/quotas for providers not filling `total_tokens`.
- Required fix:
  1. In `Usage.Add`, compute total defensively when any side missing total.
  2. Add provider-normalization tests where only input/output are provided.
- Progress update:
  1. `pkg/core/types/usage.go`: `Usage.Add` now computes fallback totals from input/output when provider total is missing.
  2. `pkg/core/types/usage_test.go`: added fallback aggregation tests for mixed/missing total scenarios.

## P1 - Provider Reliability/Behavior Gaps

### P1-1: Gemini tool-loop request path fails without required thought signatures in one scenario [Status: Done (2026-02-19)]
- Evidence:
  - Failing integration subtest:
    - `TestMessages_Run_MaxToolCallsLimit/gemini`
    - Error: `INVALID_ARGUMENT` missing `thought_signature`.
  - Reported in `/tmp/gemini_individual_rerun/summary.txt`.
- Risk:
  - Production tool loops can fail intermittently on Gemini when model emits signatures and SDK does not preserve/forward correctly in all branches.
- Required fix:
  1. Trace thought-signature propagation across request/stream/loop history in Gemini adapter.
  2. Add deterministic integration/unit repro for this exact scenario.
  3. Validate fix across `Run` and `RunStream` tool recursion.
- Progress update:
  1. `pkg/core/providers/gemini/request.go` and `pkg/core/providers/gemini_oauth/provider.go`: preserve thought signatures without mutating history maps.
  2. `pkg/core/providers/gemini/stream.go` and `pkg/core/providers/gemini_oauth/stream.go`: fixed nil-args stream edge case so thought signature still propagates.
  3. Added unit + stream parser tests and SDK recursion tests in `sdk/run_gemini_thought_signature_test.go`.
  4. Integration rerun: Gemini `Run_MaxToolCallsLimit` passes; Gemini OAuth capacity failures remain tracked under `P1-2`.

### P1-2: Gemini OAuth tests intermittently fail with capacity (`RESOURCE_EXHAUSTED`) [Status: In Progress (2026-02-19)]
- Evidence:
  - Failing one-by-one subtests:
    - `TestMessages_Run_MultipleToolCalls/gemini-oauth`
    - `TestMessages_Run_Hooks/gemini-oauth`
    - `TestMessages_Run_UsageAggregation/gemini-oauth`
- Risk:
  - Unstable CI and production behavior under capacity pressure.
- Required fix:
  1. Define retry/backoff policy for transient provider capacity errors.
  2. Separate correctness tests from quota/capacity-sensitive workloads.
  3. Introduce provider-specific rate-control in integration runner.
- Progress update:
  1. `integration/reliability_helpers_test.go`: added transient error classification, bounded retry/backoff, provider-specific throttling, and optional structured diagnostics logging.
  2. `integration/run_test.go`: routed capacity-prone `Run_MultipleToolCalls`, `Run_Hooks`, and `Run_UsageAggregation` through retry/throttle helper.
  3. Policy decision implemented: persistent Gemini OAuth capacity exhaustion now skips correctness assertions after bounded retries instead of hard-failing the matrix lane.
  4. Remaining work: validate behavior in live CI/provider runs over multiple days.

### P1-3: Groq tool-choice behavior can hard-fail max-turns test [Status: Done (2026-02-19)]
- Evidence:
  - Reproduced failure in non-Google matrix:
    - `TestMessages_Run_MaxTurnsLimit/groq`
    - `tool_use_failed`.
  - Related skip condition exists in `MaxToolCallsLimit` but not in `MaxTurnsLimit`.
- Risk:
  - Flaky integration suite and fragile production assumptions about strict tool-choice compliance.
- Required fix:
  1. Decide policy: strict SDK enforcement vs model-dependent fallback.
  2. Align tests across `MaxToolCallsLimit` and `MaxTurnsLimit` for known model refusal behavior.
- Progress update:
  1. `integration/run_test.go`: aligned `MaxTurnsLimit` behavior with existing tool-refusal policy and added explicit Groq skip for known model refusal (`tool_use_failed`/`did not call a tool`).
  2. Added shared `isModelToolRefusal` helper in `integration/reliability_helpers_test.go` to centralize policy.

## P1 - Test Strategy Gaps

### P1-4: Provider-critical modules lack direct unit tests [Status: In Progress]
- Evidence: baseline coverage was `0.0%` for most provider implementations and stream parsers.
- Risk:
  - Translation/parsing regressions ship undetected.
- Required fix:
  1. Add fixture-based tests for each provider request translation.
  2. Add response parsing tests for text/tools/multimodal/usage/error branches.
  3. Add streaming parser tests for edge cases and finalization semantics.
- Progress update:
  1. Added new provider stream contract tests across OpenAI, OAI Responses, Anthropic, Gemini, and Gemini OAuth.
  2. Added Gemini/Gemini OAuth request-thought-signature regression coverage.
  3. Added direct request/response translation tests for OpenAI and OAI Responses:
     - `pkg/core/providers/openai/request_response_test.go`
     - `pkg/core/providers/oai_resp/request_response_test.go`
  4. Added direct provider wrapper behavior tests for Groq/Cerebras:
     - `pkg/core/providers/groq/provider_test.go`
     - `pkg/core/providers/cerebras/provider_test.go`
  5. Added voice provider helper coverage:
     - `pkg/core/voice/stt/cartesia_test.go`
     - `pkg/core/voice/tts/cartesia_test.go`
  6. Remaining gap: deeper branch coverage for complex provider response/error permutations.

### P1-5: Integration suite not in default quality gate [Status: Done (2026-02-19)]
- Evidence:
  - Integration uses `//go:build integration` and does not run in `go test ./...`.
- Risk:
  - Breakages merge without exercising live adapters.
- Required fix:
  1. Add CI stage for integration matrix with controlled schedules and credentials.
  2. Keep default unit tests fast but enforce integration on pre-release branches/tags.
- Progress update:
  1. Added provider-matrix release gate workflow: `.github/workflows/integration-release-gate.yml`.
  2. Workflow enforces provider credential presence, runs tagged integration per provider, and uploads JSON + summary artifacts for deterministic triage.
  3. Added `VAI_INTEGRATION_PROVIDERS` filtering support in `integration/suite_test.go` for deterministic per-provider runs.

### P1-6: Some integration assertions are too soft for regression detection [Status: In Progress (2026-02-19)]
- Evidence examples:
  - Earlier integration variants warned/logged instead of failing on deterministic tool-result mismatches.
  - Multiple tests still accept broad outcomes due model variance.
- Risk:
  - False-green suite despite real behavioral regressions.
- Required fix:
  1. Tighten assertions around SDK invariants (event ordering, history deltas, stop reasons, tool execution counts where deterministic).
  2. Keep model-variance allowances explicit and limited.
- Progress update:
  1. `integration/run_test.go`: converted weak weather-content warning to deterministic assertion on tool-result payloads.
  2. `integration/run_test.go`: strengthened usage assertions to require positive `total_tokens` in addition to input/output tokens.
  3. Remaining work: continue replacing broad model-prose checks with deterministic SDK invariants across remaining integration files.

## P2 - Operational Readiness Gaps

### P2-1: No documented production SLOs / error budgets / provider fallback strategy [Status: Done (2026-02-19)]
- Current state:
  - Multi-provider SDK behavior exists, but release criteria are not codified.
- Required fix:
  1. Define SLOs (success rate, p95 latency, stream completion rate).
  2. Define provider fallback and retry policies.
  3. Define incident runbooks for provider-specific failures.
- Progress update:
  1. Added `PRODUCTION_OPERATIONS.md` with explicit SLOs, error budgets, retry/fallback policy, and incident runbooks.
  2. Linked operational policy from `DEVELOPER_GUIDE.md` for developer-facing discoverability.

### P2-2: Missing deterministic load/stress coverage for stream+tool concurrency [Status: In Progress (2026-02-19)]
- Current state:
  - `-race` passes, but no targeted sustained/concurrency stress suite demonstrated.
- Required fix:
  1. Add stress harness for `RunStream` cancel/interrupt/close race combinations.
  2. Add soak tests for parallel tool execution and voice streaming paths.
- Progress update:
  1. Added deterministic stress regression `sdk/run_stream_stress_test.go` (`TestRunStream_CloseCancel_Stress`) for repeated close/cancel race combinations.
  2. Remaining work: add sustained soak coverage for parallel tool execution and voice streaming under load.

### P2-3: Endpoint/transport diagnostics are insufficient for rapid triage [Status: In Progress (2026-02-19)]
- Session evidence:
  - Network/DNS behavior changed with sandbox permissions and produced confusing signals.
- Required fix:
  1. Add optional transport diagnostics mode in integration runner.
  2. Emit structured provider error metadata (endpoint, operation, classified retryability).
- Progress update:
  1. Added optional structured diagnostics mode in integration runner (`VAI_INTEGRATION_DIAGNOSTICS=1`) with operation/provider/attempt/retryability/capacity classification.
  2. Remaining work: extend endpoint/operation metadata emission from provider client layers (not only integration harness classification).

## Consolidated Release Gate (Must Pass Before “Production Ready”)
1. Completed: P0-1 (`RunStream.Close` lifecycle/channel closure).
2. Completed: P0-2 (terminal stream event + `io.EOF` handling).
3. Completed: P0-3 (cancel vs timeout stop reason mapping).
4. Completed: P0-4 (usage total aggregation fallback).
5. Completed: P1-1 Gemini thought-signature failure in `Run_MaxToolCallsLimit`.
6. In progress: validate Gemini OAuth capacity policy effectiveness in live CI/provider runs.
7. Completed: Groq tool-choice max-turn behavior policy alignment.
8. In progress: raise provider-package coverage to release threshold across all translation/parsing branches.
9. Completed: CI integration workflow with provider throttling and deterministic reporting.
10. In progress: tighten remaining weak integration assertions to deterministic SDK invariants.

## High-Value Implementation Plan

### Phase 1: Correctness Hotfixes (P0)
1. Refactor `RunStream` close ownership and channel lifecycle.
2. Normalize stream finalization semantics across SDK/provider streams.
3. Fix stop-reason mapping for canceled vs timed-out contexts.
4. Fix usage aggregation fallback logic.
5. Add targeted regression tests for each.

### Phase 2: Provider Reliability
1. Gemini thought-signature propagation investigation and fix.
2. Gemini OAuth retry/backoff + capacity-aware integration scheduling.
3. Groq tool refusal handling policy and test alignment.

### Phase 3: Test/CI Hardening
1. Add provider translation/stream parser fixture tests for all providers.
2. Raise line and branch coverage in provider packages.
3. Establish integration CI cadence and release gates.

## Appendix A: One-by-One Gemini Rerun Snapshot (this session)
Note: this appendix is a historical snapshot from the original audit run; status updates are tracked above in the item annotations.
1. Gemini: `16 pass / 1 fail`
2. Gemini OAuth: `14 pass / 3 fail`
3. Detailed per-test outcomes: `/tmp/gemini_individual_rerun/summary.txt`

## Appendix B: Non-Google Integration Snapshot (this session)
1. Anthropic: mostly pass in matrix run.
2. OAI Responses: mostly pass in matrix run.
3. Groq: recurring failure in `TestMessages_Run_MaxTurnsLimit/groq` (`tool_use_failed`), and skip in one max-tool-calls scenario due model refusal.

## Final Recommendation
P0 correctness blockers identified in this report are now implemented and covered with targeted regression tests. Do not label this SDK production-ready yet: remaining release risk is concentrated in provider reliability policy (`P1-2`, `P1-3`), integration/coverage hardening (`P1-4` to `P1-6`), and operational gates (`P2-*`).
