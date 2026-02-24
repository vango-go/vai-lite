# Phase 02 — Gateway Skeleton + Middleware Hardening (Implementation Plan)

This phase hardens the newly added gateway binary so it is safe and predictable for hosted and self-hosted proxy usage.

It focuses on cross-cutting correctness and safety, not feature expansion.

Primary references:
- `PHASE_00_DECISIONS_AND_DEFAULTS.md` (locked decisions)
- `PROXY_MODE_IMPLEMENTATION_PLAN.md` (overall sequencing)
- `VAI_GATEWAY_HOSTING_ARCHITECTURE_AND_DESIGN.md` (deployment expectations)

Code reality:
- Gateway binary exists: `cmd/vai-proxy/main.go`
- Gateway packages exist: `pkg/gateway/*`
- `/healthz`, `/readyz`, and `/v1/messages` exist (JSON + SSE): `pkg/gateway/server/server.go`

---

## 0. Exit Criteria (Definition of Done)

Phase 02 is complete when:

1. Limits are enforced consistently:
   - `MaxBodyBytes` enforced for JSON endpoints (`/v1/messages`, `/v1/runs*` once added).
   - Request decoding time is bounded (handler timeout + upstream timeouts where applicable).
   - A clear, centralized place exists to add decoded base64 budgets (per-block + total).

2. Auth behavior is production-appropriate:
   - `auth_mode=required|optional|disabled` works (already implemented).
   - Auth errors return canonical JSON error envelope with `request_id` (already implemented for auth middleware).
   - BYOK headers are never logged (explicitly verified in logging/middleware).

3. SSE is robust through proxies:
   - `ping` events exist when upstream is quiet.
   - `X-Accel-Buffering: no` is set (already set).
   - Client disconnect closes upstream streams promptly.
   - Streaming duration limit is enforced.

4. CORS is correct and safe:
   - Disabled by default.
   - Allowlist-based origin matching.
   - Preflight handling for all public routes.
   - `Vary: Origin` and explicit allowed/exposed headers per `PHASE_00_DECISIONS_AND_DEFAULTS.md`.

5. `/readyz` matches the Phase 00 semantics:
   - Validates config load + key store load + allowlist load.
   - Does not require “managed upstream provider exists” in BYOK-first mode.

---

## 1. Work Items

### 1.1 Add CORS middleware (allowlist + preflight)

Implement `pkg/gateway/mw/cors.go`:
- Config:
  - `VAI_CORS_ALLOWED_ORIGINS` (CSV) or a config struct field
- Behavior:
  - If no allowlist configured: do not emit CORS headers.
  - If `Origin` matches allowlist:
    - emit `Access-Control-Allow-Origin: <origin>`
    - emit `Vary: Origin`
    - emit `Access-Control-Allow-Credentials: true` (only if we decide to support cookies later; otherwise omit)
    - allow headers:
      - `Authorization`, `Content-Type`, `X-Request-ID`, `Idempotency-Key`
      - all `X-Provider-Key-*` we support including `X-Provider-Key-Cartesia` and `X-Provider-Key-ElevenLabs`
    - expose response headers:
      - `X-Request-ID`, `X-Model`, `X-Input-Tokens`, `X-Output-Tokens`, `X-Total-Tokens`, `X-Cost-USD`, `X-Duration-Ms`
- Preflight:
  - For `OPTIONS`, return `204` if allowed; `403` if not.

Tests:
- Middleware unit tests for allow/deny behavior.

### 1.2 Add rate limiting + concurrency caps (in-memory v1)

Implement `pkg/gateway/ratelimit/*`:
- Token bucket or fixed-window (start simple; correctness > cleverness).
- Keys:
  - per-principal (gateway API key) when authenticated
  - else “anonymous” bucket when `auth_mode=optional|disabled`
- Separate limits for:
  - requests/sec (non-stream)
  - concurrent SSE streams
  - concurrent WS sessions (placeholder for Phase 06/Live)

Wire into middleware chain in `pkg/gateway/server/server.go`.

Tests:
- Burst returns `429` with `Retry-After`.
- Concurrent stream limit returns `429`.

### 1.3 Harden SSE behavior (`/v1/messages` streaming)

Enhance `pkg/gateway/handlers/messages.go`:
- Emit gateway `ping` events when upstream produces no events for `sse.ping_interval`.
- Enforce max stream duration (config).
- Ensure upstream stream is closed when client disconnects (detect `Write` errors; also watch `r.Context().Done()`).

Tests:
- Fake event stream that pauses; verify `ping` is emitted.

### 1.4 Improve `/readyz` checks

Update `pkg/gateway/handlers/health.go`:
- Validate that the loaded config meets readiness semantics:
  - auth_mode valid
  - if required: key store non-empty
  - model allowlist parses (already done in config)
- Return JSON with fields useful for operators:
  - `ok`, `auth_mode`, `allowlist_enabled`

### 1.5 Default limits + config documentation

Update docs to include *actual enforced defaults* (numbers + env var names):
- `PHASE_00_DECISIONS_AND_DEFAULTS.md` (already locks conceptual behavior; add operational knobs once implemented)
- add a small operator section to `README.md` or a new `GATEWAY_CONFIG.md` (your call)

---

## 2. Deferred (Explicitly Not In This Phase)

- `/v1/runs` and `/v1/runs:stream` (Phase 03)
- `/v1/live` WebSocket (Phase 05/Live)
- Postgres/Redis backends (later hardening)

