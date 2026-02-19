# vai-lite Production Operations

This document defines release gates and on-call operating policy for `vai-lite`.

## 1. Release SLOs

The SDK is considered production-ready only when these targets are met over a rolling 7-day window:

1. End-to-end run success rate (`Messages.Run` + `Messages.RunStream`): `>= 99.0%`
2. Stream completion rate (`RunStream` reaches terminal `RunCompleteEvent` without transport failure): `>= 99.5%`
3. P95 model-turn latency:
   - non-tool single turn: `<= 8s`
   - tool turn (model + tool + follow-up): `<= 20s`
4. Tool execution success rate (handler returned no error): `>= 99.0%`

## 2. Error Budget

For a 30-day window:

1. Overall run failure budget: `1.0%`
2. Transient provider-capacity failures (`rate_limit`, `overloaded`, `RESOURCE_EXHAUSTED`): `<= 0.5%`
3. Non-retryable request-shape failures (`invalid_request_error`): `<= 0.2%`

If a budget is exhausted:

1. Freeze non-critical feature rollout.
2. Prioritize reliability work (fallback tuning, retry policy, request-shape hardening).
3. Require one clean 7-day burn-down before lifting freeze.

## 3. Retry And Backoff Policy

Retry only transient failures:

1. Retryable classes: `rate_limit_error`, `overloaded_error`, `api_error`, transport `502/503/504`.
2. Non-retryable classes: `invalid_request_error`, auth/permission failures, malformed schema/tool input.

Defaults:

1. Base backoff: `750ms`
2. Max backoff: `10s`
3. Exponential backoff with jitter.
4. Recommended max attempts:
   - standard providers: `2-3`
   - Gemini: `3`
   - Gemini OAuth: `4` with stricter pacing.

## 4. Provider Fallback Strategy

Fallback is caller-owned (the SDK does not auto-failover between providers).

Recommended order for tool-heavy flows:

1. Primary: preferred provider/model for quality.
2. Secondary: equivalent-capability provider with tested tool behavior.
3. Tertiary: non-tool fallback path (respond without function tools) if both fail.

Rules:

1. Only fallback on retryable failures after local retries are exhausted.
2. Preserve conversation/tool history when switching providers.
3. Mark provider switch in request metadata for traceability.

## 5. Integration Runner Controls

Integration tests include reliability controls in `integration/reliability_helpers_test.go`.

Environment knobs:

1. `VAI_INTEGRATION_PROVIDERS` (comma-separated provider names, or `all`)
2. `VAI_INTEGRATION_RETRY_ATTEMPTS`
3. `VAI_INTEGRATION_GEMINI_RETRY_ATTEMPTS`
4. `VAI_INTEGRATION_GEMINI_OAUTH_RETRY_ATTEMPTS`
5. `VAI_INTEGRATION_RETRY_BASE_MS`
6. `VAI_INTEGRATION_RETRY_MAX_MS`
7. `VAI_INTEGRATION_GEMINI_MIN_GAP_MS`
8. `VAI_INTEGRATION_GEMINI_OAUTH_MIN_GAP_MS`
9. `VAI_INTEGRATION_DIAGNOSTICS=1` for structured retry/classification logs

## 6. Incident Runbooks

### A) Capacity / Rate Limit Spikes

1. Confirm error class (`RESOURCE_EXHAUSTED`, `rate_limit`, `429`).
2. Increase provider-specific pacing and retry backoff.
3. Shift traffic to fallback provider if capacity persists.
4. Track recovery before restoring normal pacing.

### B) Authentication / Permission Failures

1. Verify active key/project credentials and expiry.
2. Confirm provider routing matches expected key namespace.
3. Rotate keys if unauthorized errors persist.
4. Do not retry aggressively; treat as config/secrets incident.

### C) Elevated Latency / Timeouts

1. Check per-provider p95 and transport status.
2. Reduce max-turn/tool-call limits for impacted workloads if needed.
3. Increase timeout only with a bounded rollout and monitoring.

### D) Tool-Loop Regressions

1. Re-run integration matrix for affected provider only.
2. Inspect stop-reason distribution (`max_tool_calls`, `max_turns`, `end_turn`, `error`).
3. Validate tool input reconstruction and handler error rates.

## 7. CI Release Gate

Workflow: `.github/workflows/integration-release-gate.yml`

Policy:

1. Run provider-matrix integration on schedule and release branches/tags.
2. Upload JSON and summary artifacts per provider run.
3. Block release tag promotion when any provider lane fails.
