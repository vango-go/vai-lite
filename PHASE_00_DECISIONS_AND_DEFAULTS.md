# Phase 00: Decisions and Defaults (Lock Before Coding)

This file locks the remaining “small but expensive to change later” defaults for the VAI Gateway (proxy mode) before production code lands.

Status: **LOCKED for proxy v1**, unless explicitly changed in a follow-up PR.

Related:
- `PROXY_MODE_IMPLEMENTATION_PLAN.md`
- `API_CONTRACT_HARDENING.md`
- `VAI_GATEWAY_HOSTING_ARCHITECTURE_AND_DESIGN.md`

---

## 1. Proxy v1 Feature Decisions

### 1.1 Voice (`request.voice`)

Decision:
- **Proxy v1 supports `voice`** on `POST /v1/messages` (non-stream + SSE).

Rationale:
- Voice is a core product requirement, and the direct-mode SDK already implements a correct STT→LLM→TTS pipeline.
- Proxy mode must expose this capability over HTTP so non-Go SDKs and browser clients can rely on it.

Behavior:
- If `request.voice.input` is set:
  - The gateway transcribes any `audio` content blocks in `request.messages[*].content` (base64) into `text` blocks before calling the upstream LLM.
  - The gateway returns `metadata.user_transcript` in the final `types.MessageResponse`.
- If `request.voice.output` is set:
  - Non-streaming (`stream=false`): the gateway synthesizes the final assistant text into an `audio` content block appended to the response (base64) with `transcript` populated.
  - Streaming (`stream=true`): the gateway emits incremental audio chunks as additional SSE events while passing through the upstream LLM’s streaming text deltas.

Error cases:
- If `request.voice` is set but `X-Provider-Key-Cartesia` is missing, return:
  - HTTP `401`
  - `{"error":{"type":"authentication_error","message":"missing voice provider api key header","param":"X-Provider-Key-Cartesia"}}`

Configuration:
- Proxy v1 uses **BYOK for voice providers**:
  - Cartesia key is supplied per request via `X-Provider-Key-Cartesia` (required when `request.voice` is set).
  - Live mode may additionally require `X-Provider-Key-ElevenLabs` when ElevenLabs TTS is selected for the session.

SSE wire format:
- The gateway continues to emit canonical `types.StreamEvent` events from the upstream LLM.
- When `voice.output` is enabled, the gateway additionally emits:
  - `event: audio_chunk`
  - `data: {"type":"audio_chunk","format":"pcm","audio":"<base64>","sample_rate_hz":24000,"is_final":false}`

Notes:
- For streaming, the gateway emits PCM audio chunks regardless of the requested `voice.output.format` (Cartesia streaming output is PCM).
- Clients can ignore `audio_chunk` events and still receive a final appended `audio` block in the terminal response.

---

### 1.2 Streaming error parity (SSE vs JSON errors)

Decision:
- **SSE terminal `error` events embed the same inner error object as non-streaming HTTP errors** (compatible with `core.Error`).

Details:
- Non-stream HTTP errors:
  - `{"error": { ...core.Error... }}`
- SSE error event:
  - `event: error`
  - `data: {"type":"error","error":{ ...core.Error fields... }}`

Required type expansion:
- Expand `pkg/core/types.Error` (used in `types.ErrorEvent`) to include the optional fields used in `core.Error`:
  - `param`, `code`, `request_id`, `retry_after`, `provider_error`

---

### 1.3 Live Audio Mode (WebSocket)

Decision:
- Proxy v1 includes the Live WebSocket endpoint described in `LIVE_AUDIO_MODE_DESIGN.md`:
  - `wss://<gateway>/v1/live`

Scope notes:
- Live Audio Mode is implemented as part of the same gateway binary and shares:
  - gateway auth
  - rate limiting / concurrency caps
  - request IDs + observability
  - provider routing + BYOK upstream keys
  - the voice pipeline (streaming STT + streaming TTS)

---

### 1.4 Provider keying and instantiation (BYOK)

Decisions:
- Proxy v1 is **BYOK-first** (headers only) with a **shared** `*http.Client`/`Transport` in the gateway for connection reuse.
- Providers may be instantiated per request as long as the HTTP transport is shared.

Long-term refactor (planned, not required for v1):
- Prefer “key resolved per request” over provider caching keyed by upstream API key:
  - Avoid unbounded key-cardinality caches.
  - Avoid storing upstream keys in long-lived provider structs.
- Approach: refactor providers to accept an `APIKeyProvider` function/interface and read the key from request context at outbound request build time.

---

### 1.5 Browser clients (CORS + preflight)

Decision:
- Proxy v1 supports browser usage **only via explicit allowlisted origins**.

Defaults:
- CORS is **disabled by default** (no `Access-Control-Allow-Origin`), which effectively blocks cross-origin browser access.
- Operators enable browser access by setting an allowlist:
  - `cors.allowed_origins = ["https://app.example.com", "http://localhost:3000"]`

Rules:
- Never use `Access-Control-Allow-Origin: *` for hosted mode.
- Always return `Vary: Origin` when CORS is enabled.
- Allow headers required by SDK usage:
  - `Authorization`, `Content-Type`, `X-Request-ID`, `Idempotency-Key`,
  - `X-Provider-Key-Anthropic`, `X-Provider-Key-OpenAI`, `X-Provider-Key-Gemini`, `X-Provider-Key-Groq`, `X-Provider-Key-Cerebras`, `X-Provider-Key-OpenRouter`
- Expose response headers needed by SDKs:
  - `X-Request-ID`, `X-Model`, `X-Input-Tokens`, `X-Output-Tokens`, `X-Total-Tokens`, `X-Cost-USD`, `X-Duration-Ms`
- Handle preflight (`OPTIONS`) for all public routes.

Note:
- This decision is about cross-origin browser access, not about whether callers use BYOK vs managed provider keys.

---

## 2. Health and Readiness Semantics

### 2.1 `/healthz`

Decision:
- `/healthz` returns `200` if the process is running (no deep checks).

### 2.2 `/readyz`

Decision:
- `/readyz` is a **configuration + dependency readiness** check, consistent with BYOK-first proxy v1.

Readiness MUST validate:
- gateway config loads and validates
- auth mode is valid and key store is loadable (even if empty for `optional/disabled`)
- model allowlist config loads (if enabled)
- HTTP server/router is constructed successfully

Readiness MAY validate (only when enabled/configured):
- storage dependencies (e.g. Postgres) are reachable when a storage backend is configured/enabled
- Redis is reachable when a Redis-based limiter is configured/enabled
- managed upstream provider keys exist and at least one managed provider is registerable **only if** the gateway is configured to run in managed/hybrid mode

Explicitly NOT required for readiness in BYOK-only mode:
- “at least one usable provider in managed mode”
