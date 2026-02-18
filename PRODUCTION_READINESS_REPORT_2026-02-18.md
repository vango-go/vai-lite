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

The project has strong momentum and broad test volume, but there are still release-blocking correctness issues in stream lifecycle behavior, weak coverage in provider-critical code, and unresolved provider-specific reliability failures (Gemini thought-signature handling, Gemini OAuth capacity handling, Groq tool-choice behavior handling).

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

## Current Test Quality Snapshot
1. Test count is high (`192` test functions discovered), but execution confidence is uneven.
2. Integration tests are build-tagged (`//go:build integration`) and are not part of default `go test ./...` runs.
3. Critical production packages have `0.0%` direct coverage:
   - `pkg/core/providers/openai`
   - `pkg/core/providers/oai_resp`
   - `pkg/core/providers/gemini`
   - `pkg/core/providers/gemini_oauth`
   - `pkg/core/providers/groq`
   - `pkg/core/providers/cerebras`
   - `pkg/core/voice/stt`
   - `pkg/core/voice/tts`
4. Total statement coverage is `27.3%`, which is low for a production SDK that performs multi-provider streaming and tool execution.

## Pending Items (Prioritized)

## P0 - Release Blocking Correctness

### P0-1: `RunStream.Close()` can strand event consumers and leave run goroutine active
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

### P0-2: Terminal stream event can be dropped when provider returns `(event, io.EOF)`
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

### P0-3: Cancellation is conflated with timeout in run loops
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

### P0-4: Usage aggregation can report `TotalTokens=0` despite nonzero input/output
- Evidence:
  - Live integration log: `run_test.go:345: Usage: input=1283, output=173, total=0` for Anthropic.
  - `pkg/core/types/usage.go:14-19` sums `TotalTokens` fields directly; no fallback recompute from input/output.
- Risk:
  - Broken billing/accounting/quotas for providers not filling `total_tokens`.
- Required fix:
  1. In `Usage.Add`, compute total defensively when any side missing total.
  2. Add provider-normalization tests where only input/output are provided.

## P1 - Provider Reliability/Behavior Gaps

### P1-1: Gemini tool-loop request path fails without required thought signatures in one scenario
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

### P1-2: Gemini OAuth tests intermittently fail with capacity (`RESOURCE_EXHAUSTED`)
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

### P1-3: Groq tool-choice behavior can hard-fail max-turns test
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

## P1 - Test Strategy Gaps

### P1-4: Provider-critical modules lack direct unit tests
- Evidence: coverage `0.0%` for most provider implementations and stream parsers.
- Risk:
  - Translation/parsing regressions ship undetected.
- Required fix:
  1. Add fixture-based tests for each provider request translation.
  2. Add response parsing tests for text/tools/multimodal/usage/error branches.
  3. Add streaming parser tests for edge cases and finalization semantics.

### P1-5: Integration suite not in default quality gate
- Evidence:
  - Integration uses `//go:build integration` and does not run in `go test ./...`.
- Risk:
  - Breakages merge without exercising live adapters.
- Required fix:
  1. Add CI stage for integration matrix with controlled schedules and credentials.
  2. Keep default unit tests fast but enforce integration on pre-release branches/tags.

### P1-6: Some integration assertions are too soft for regression detection
- Evidence examples:
  - `integration/run_test.go:56-59` warns/logs instead of failing missing weather content.
  - Multiple tests accept broad outcomes due model variance.
- Risk:
  - False-green suite despite real behavioral regressions.
- Required fix:
  1. Tighten assertions around SDK invariants (event ordering, history deltas, stop reasons, tool execution counts where deterministic).
  2. Keep model-variance allowances explicit and limited.

## P2 - Operational Readiness Gaps

### P2-1: No documented production SLOs / error budgets / provider fallback strategy
- Current state:
  - Multi-provider SDK behavior exists, but release criteria are not codified.
- Required fix:
  1. Define SLOs (success rate, p95 latency, stream completion rate).
  2. Define provider fallback and retry policies.
  3. Define incident runbooks for provider-specific failures.

### P2-2: Missing deterministic load/stress coverage for stream+tool concurrency
- Current state:
  - `-race` passes, but no targeted sustained/concurrency stress suite demonstrated.
- Required fix:
  1. Add stress harness for `RunStream` cancel/interrupt/close race combinations.
  2. Add soak tests for parallel tool execution and voice streaming paths.

### P2-3: Endpoint/transport diagnostics are insufficient for rapid triage
- Session evidence:
  - Network/DNS behavior changed with sandbox permissions and produced confusing signals.
- Required fix:
  1. Add optional transport diagnostics mode in integration runner.
  2. Emit structured provider error metadata (endpoint, operation, classified retryability).

## Consolidated Release Gate (Must Pass Before “Production Ready”)
1. Fix and test P0-1 (`RunStream.Close` lifecycle/channel closure).
2. Fix and test P0-2 (terminal stream event + `io.EOF` handling).
3. Fix and test P0-3 (cancel vs timeout stop reason mapping).
4. Fix and test P0-4 (usage total aggregation fallback).
5. Resolve Gemini thought-signature failure in `Run_MaxToolCallsLimit`.
6. Implement and validate capacity handling strategy for Gemini OAuth.
7. Stabilize Groq tool-choice max-turn behavior handling (or mark as expected model variance with clear policy).
8. Raise provider-package coverage from 0% to meaningful thresholds (recommended minimum 60% for translation/parsing logic paths).
9. Add CI integration workflow with provider throttling and deterministic reporting.
10. Tighten weak assertions in integration tests to catch SDK regressions rather than model prose variance.

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
1. Gemini: `16 pass / 1 fail`
2. Gemini OAuth: `14 pass / 3 fail`
3. Detailed per-test outcomes: `/tmp/gemini_individual_rerun/summary.txt`

## Appendix B: Non-Google Integration Snapshot (this session)
1. Anthropic: mostly pass in matrix run.
2. OAI Responses: mostly pass in matrix run.
3. Groq: recurring failure in `TestMessages_Run_MaxTurnsLimit/groq` (`tool_use_failed`), and skip in one max-tool-calls scenario due model refusal.

## Final Recommendation
Do not label this SDK production-ready until all P0 issues are resolved and revalidated, and until provider reliability + coverage gaps are materially reduced. The highest-risk failures are deterministic SDK lifecycle/stream semantics and token accounting correctness; these should be fixed before any broader rollout.
