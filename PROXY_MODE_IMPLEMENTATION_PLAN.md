# VAI Proxy Mode (Hosted Gateway) — Implementation Plan

This document is a thorough build plan for adding **proxy/server mode** to `vai-lite` so VAI can be launched as a hosted AI gateway.

Inputs:
- `DEVELOPER_GUIDE.md` (current direct-mode primitives and types)
- `LIVE_AUDIO_MODE_DESIGN.md` (Live WebSocket endpoint design and wire protocol)
- `API_CONTRACT_HARDENING.md` (strict JSON decoding decisions for proxy request bodies)
- `RUNS_API_AND_EVENT_SCHEMA.md` (server-side Run/RunStream endpoints and SSE event schema)
- `PHASE_00_DECISIONS_AND_DEFAULTS.md` (locked defaults and pre-coding decisions)

Repo reality check:
- Today `vai-lite` is **direct-mode only** (see `README.md`).
- The canonical API types already exist in `pkg/core/types/*` and are based on the **Anthropic Messages API** shape.
- Providers are implemented in `pkg/core/providers/*` and normalize responses/events to `pkg/core/types`.
- The Go SDK (`sdk/*`) adds tool-loop orchestration (`Run`/`RunStream`) and optional non-live voice mode.

Proxy mode will expose these same canonical types over HTTP and add hosted-gateway concerns:
- authentication, rate limiting, and governance
- request validation and abuse limits
- standardized errors
- observability and usage accounting
- configuration and deployment

---

## 0. Scope: What “Proxy Mode” Means Here

### Proxy mode delivers
- A Go **server binary** (and containerizable deployment) that exposes:
  - `POST /v1/messages` (single-turn; streaming via SSE when `stream=true`)
    - includes optional `voice` support (STT input + TTS output)
  - `GET /v1/models` (capabilities + allowlisted models)
  - `GET /v1/live` (WebSocket upgrade endpoint for Live Audio Mode)
  - Health endpoints (`/healthz`, `/readyz`)
  - Optional: `/metrics` (Prometheus)
- A foundation for higher-level server endpoints:
  - `POST /v1/runs` and `POST /v1/runs:stream` (server-side tool loop; added after `/v1/messages` is solid)
- A stable HTTP contract suitable for non-Go SDKs (JS/Python/Rust/curl).
- A governance layer around provider execution (auth, rate limiting, logging, usage).

### Proxy mode explicitly does not deliver (in this phase)
- A full “admin control plane” product (UI, org management, billing portal).
  - We will, however, design storage interfaces so it can be added without rewrite.

---

## 1. Goals and Non-Goals

### 1.0 Initiative Dependencies (Make This Explicit)

Your top-level initiatives have hard dependencies:

1. Proxy mode (`/v1/messages` + SSE) is the foundation for:
   - non-Go SDKs (they need a stable server endpoint)
   - the chat app (it needs a stable endpoint + streaming semantics)
   - live audio mode (it is fundamentally a long-lived streaming session hosted by the gateway)
2. `/v1/runs:stream` is the “thin SDK unlock”:
   - without it, each non-Go SDK must re-implement the full tool loop (`RunStream` semantics), which is a large and subtle surface area.
   - live audio mode can reuse the same run-controller semantics.

This implies sequencing:
- ship `/v1/messages` + SSE first
- then ship `/v1/runs:stream` (even if tools are limited to gateway-managed tools)
- then build SDKs and the chat app against those endpoints

### Goals
1. **Hostable**: run as a single stateless HTTP service behind a load balancer.
2. **Shared core**: reuse `pkg/core` translation and provider code; avoid “proxy-only” behavior drift.
3. **Streaming**: first-class SSE that matches `pkg/core/types.StreamEvent` shapes.
4. **Multi-provider**: preserve `provider/model` routing via `pkg/core/engine.go`.
5. **Governance**: API key auth, rate limiting, and request size/time limits.
6. **Observability**: request IDs, structured logs, metrics hooks, and usage reporting.
7. **Live-ready**: host Live Audio Mode (`/v1/live`) under the same gateway binary and reuse auth/limits/obs.
8. **Run-ready**: add a server-side tool-loop endpoint so non-Go SDKs can be thin.

### Non-goals (v1 proxy)
- **v1.0 scope decision**: prioritize perfect parity for `POST /v1/messages` (including SSE) over shipping `Run`/`RunStream` server-side on day 1.
- **Server-side tool execution for arbitrary customer code** is not part of v1.0.
  - A server-side `Run` API is still achievable by using a safe tool contract (gateway-managed tools and/or registered HTTP tools), but that is a separate design step.
- Cross-request caching of model outputs (possible later; not required for launch).

---

## 2. Design Principles

1. **Canonical API = `pkg/core/types`**:
   - The HTTP request/response payloads are the JSON serialization of:
     - `types.MessageRequest`
     - `types.MessageResponse`
     - `types.StreamEvent` (for SSE)
   - Proxy mode must make this true in practice by implementing strict JSON decoding for:
     - `MessageRequest.System` (string or `[]ContentBlock`, not an arbitrary `[]any`)
     - `Tool.Config` (decode into typed config structs by tool type)
     - request content blocks (reject unknown types; no “placeholder” blocks)
2. **Strict boundaries**:
   - `pkg/core` remains the source of truth for provider translation.
   - Proxy-only concerns live under `pkg/gateway/*` (or `pkg/server/*`).
3. **Security first**:
   - Hard limits on request size, base64 payload size, and streaming duration.
   - No SSRF: the gateway does not fetch arbitrary URLs as part of proxy mode.
4. **Incremental hardening**:
   - Start with BYOK for upstream providers (to launch) plus strict gateway limits.
   - Add managed keys and more storage later behind stable interfaces.

---

## 3. Public HTTP API (Proxy)

### 3.1 `POST /v1/messages`

Purpose:
- Single-turn message execution, provider-routed via `req.model` (`provider/model-name`).

Request:
- Body: JSON `types.MessageRequest`
- Headers:
  - `Authorization: Bearer <vai_api_key>` (gateway auth)
  - `Idempotency-Key: <string>` (optional; v1 can accept but not implement dedupe)
  - `X-Request-ID: <string>` (optional; if absent, server generates)

Response (non-streaming):
- `200 application/json`: JSON `types.MessageResponse`
- Important response headers:
  - `X-Request-ID`
  - `X-Model` (resolved/normalized)
  - `X-Input-Tokens`, `X-Output-Tokens`, `X-Total-Tokens` (if available)
  - `X-Cost-USD` (if available)
  - `X-Duration-Ms`

Response (streaming):
- If `req.stream == true`, respond with:
  - `200 text/event-stream; charset=utf-8`
  - SSE events where:
    - `event:` equals `StreamEvent.EventType()` (e.g. `message_start`)
    - `data:` is the JSON encoding of the event object
  - End of stream occurs after `message_stop` or `error`

Notes on tool calls:
- When the model calls a **function tool**, the proxy returns `tool_use` blocks and `stop_reason="tool_use"` in the response (matching Anthropic semantics).
- The proxy does not execute customer tools server-side; client loops by sending a follow-up request containing `tool_result` blocks.

Notes on non-live voice:
- `types.MessageRequest` already has `voice` configuration, and the repo already has Cartesia STT/TTS plumbing under `pkg/core/voice/*`.
- Proxy v1 decision: **support `voice`** (see `PHASE_00_DECISIONS_AND_DEFAULTS.md`).
  - If `voice.input` is set, the gateway transcribes `audio` blocks to `text` before calling the upstream LLM.
  - If `voice.output` is set:
    - non-streaming responses append a final `audio` block (base64) with `transcript`
    - streaming responses emit additional `audio_chunk` SSE events while preserving upstream text deltas
  - Voice is **BYOK** in v1:
    - Cartesia API key is supplied per request via `X-Provider-Key-Cartesia` when `request.voice` is set.
    - ElevenLabs is reserved for **Live Audio Mode only** (not `/v1/messages`) initially.

### 3.1.1 Block Type Parity vs Provider Support (Important)

The canonical content block types already exist in `pkg/core/types/content.go`:
- Input: `text`, `image`, `audio`, `video`, `document`, `tool_result`
- Output: `text`, `tool_use`, `thinking`, `image`, `audio`

“/v1/messages parity” should mean:
- The gateway accepts the canonical request shape and forwards it to the provider through the shared core.
- If the request uses a block type that a specific provider/model cannot handle (or is not implemented in our translation layer), the gateway fails fast with a clear `invalid_request_error` that names:
  - provider/model
  - unsupported block type(s)
  - suggested alternatives (if any)

This is better than silently dropping blocks (which would be correctness loss).

Practical implications for v1:
- `image` is broadly supported if translation is implemented.
- `audio` input support depends on provider/model; OpenAI and Gemini advertise audio input capability, Anthropic does not.
- `video` is Gemini-only in this repo’s capability model.
- `document` is supported by Gemini translation; for OpenAI Responses it may require file upload semantics (more than pure translation), so it may be unsupported initially unless implemented fully.

### 3.2 `GET /v1/models`

Purpose:
- Discover supported providers, capabilities, and allowlisted model IDs.

Response (proposed):
```json
{
  "object": "list",
  "data": [
    {
      "id": "anthropic/claude-sonnet-4",
      "provider": "anthropic",
      "capabilities": {
        "vision": true,
        "tools": true,
        "tool_streaming": true,
        "structured_output": true,
        "native_tools": ["web_search", "web_fetch", "code_execution", "computer_use", "text_editor"]
      }
    }
  ]
}
```

Implementation note:
- Provider capabilities exist in `core.ProviderCapabilities` (`pkg/core/provider.go`).
- The allowlisted model list should be driven by **gateway config**, not scraped from providers.

### 3.3 Health endpoints

- `GET /healthz`: always 200 if process is up.
- `GET /readyz`: 200 only if configuration is valid and required dependencies are available.
  - For v1 proxy (stateless), readiness should at least ensure:
    - auth configuration loads
    - key store loads (even if empty for `auth_mode=optional|disabled`)
    - model allowlist config loads (if enabled)
    - storage (if enabled) is reachable
  - If the gateway is configured for managed/hybrid upstream keys, readiness should additionally validate that at least one managed provider can be registered.

### 3.4 Optional: `GET /metrics`

- Prometheus exposition format.
- Include counters/histograms with low cardinality:
  - request count by route/status/provider
  - latency histograms by route/provider
  - stream completion vs disconnect
  - rate-limit blocks

---

## 4. Authentication, Tenancy, and Rate Limiting

### 4.1 Gateway API keys (required)

Auth model:
- `Authorization: Bearer vai_sk_...`

Note:
- Even with upstream BYOK, hosted VAI still needs gateway auth for rate limiting, abuse prevention, and tenant policy enforcement.

Self-hosting note:
- For self-hosted deployments in a trusted network, requiring a separate VAI gateway key can be unnecessary friction.
- Recommended approach: support an explicit auth mode setting so operators can choose the tradeoff.

Auth modes (proposed):
- `required` (hosted default): reject requests without a valid gateway key.
- `optional` (self-host convenience): accept requests without a gateway key, but apply only global limits (no per-tenant quotas).
- `disabled` (trusted-only): no gateway auth; rely on network controls (bind to `127.0.0.1`, private VPC, or an external reverse proxy with auth).

Security tradeoff:
- With auth disabled, the proxy can become an “open relay” for compute/bandwidth even if callers must supply BYOK headers. This is usually fine only when bound to localhost/private networks or protected by an upstream gateway.

Key types (v1):
- `client` keys: used by external callers, subject to per-key quotas.
- `admin` keys: optional, for operational endpoints (if any are added later).

Tenancy model (v1):
- A key maps to a `project_id` (or `tenant_id`) and a set of limits:
  - requests/minute
  - concurrent streams
  - max request body size override (rare)
  - allowed providers/models (allowlist)

Storage (v1):
- Start with a config-backed in-memory key store:
  - `VAI_GATEWAY_KEYS_JSON` (env) or `gateway_keys.json` (file path in config)
- Define a storage interface so Postgres can be swapped in later:
  - `KeyStore.Get(key) -> Principal`
  - `UsageStore.Record(event)`

### 4.2 Rate limiting (required)

We need two kinds of limits:
1. **Token bucket** per API key (requests/minute).
2. **Concurrency limit** per API key (max in-flight requests and max streaming connections).

Implementation plan:
- `pkg/gateway/ratelimit` with an interface that supports:
  - `Allow(principal, cost) (ok bool, retryAfter time.Duration)`
- Start with in-memory buckets (safe for single instance).
- For multi-instance scaling, plan a Redis-backed implementation (later).

### 4.3 Abuse limits (required)

Global hard caps regardless of tenant:
- Max JSON body size (e.g. 4–16MB depending on multimodal needs)
- Max decoded base64 bytes per content block and per request (even if we don’t decode, we can estimate from base64 length)
- Max `messages` count and max total text length
- Max streaming duration (e.g. 2–5 minutes) and idle timeout

### 4.4 Browser clients / CORS (optional)

If the gateway is intended to be called directly from a browser (e.g. a chat app using the TS SDK), the proxy must implement CORS and preflight correctly.

Decision (proxy v1):
- CORS is **disabled by default** and enabled only via an explicit allowlist of origins (see `PHASE_00_DECISIONS_AND_DEFAULTS.md`).

---

## 5. Upstream Credential Modes (Managed vs BYO)

Proxy mode must support a hosted gateway launch. Start with **BYOK (Bring Your Own Key)**, then add managed keys later.

### 5.1 BYOK (launch default)

Goal:
- Callers supply upstream provider credentials per request, while VAI still enforces gateway auth, rate limiting, and policy.

Recommended input channel (v1):
- Headers only (no type changes to `types.MessageRequest`):
  - `X-Provider-Key-Anthropic: sk-ant-...`
  - `X-Provider-Key-OpenAI: sk-...` (also used for `oai-resp/*`)
  - `X-Provider-Key-Gemini: ...`
  - `X-Provider-Key-Groq: ...`
  - `X-Provider-Key-Cerebras: ...`
  - `X-Provider-Key-OpenRouter: ...`

Rules:
- The gateway MUST NOT log provider keys and MUST redact these headers in all request logs.
- BYOK does not remove the need for gateway auth (`Authorization: Bearer vai_sk_...`) in hosted mode; otherwise the service is an open relay.
- When the request targets `provider/model`, require the matching `X-Provider-Key-<Provider>` header (or return a clear `authentication_error` / `invalid_request_error`).
- The gateway should parse `req.model` into `(provider, modelName)` and pass a request copy to the provider with `reqCopy.model = modelName` (providers in this repo generally assume the prefix was stripped upstream).

### 5.2 Managed Keys (phase 2)

Meaning:
- Gateway operator configures upstream credentials via env or secret store:
  - `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GEMINI_API_KEY`, etc.
- The gateway uses its own upstream keys when BYOK headers are absent (or when tenant policy requires managed mode).

Implementation note:
- Managed keys can be implemented as a simple fallback after BYOK header resolution.

Important design choice:
- If we ever add body-based provider keys (`provider_keys`) for non-header environments, it must be consumed by the gateway and MUST NOT be forwarded upstream.

Recommendation:
- v1: headers only (no type change).
- v1.1+: add body field if needed for non-header environments.

### 5.3 Passthrough mode (optional later)

Meaning:
- Use the same provider API key header expected by the upstream API (e.g. forwarding `Authorization`).

This is discouraged for hosted:
- It complicates gateway auth vs upstream auth separation.
- It is easier to reason about and secure explicit BYO headers.

---

## 6. Provider Instantiation: Correctness and Performance

### 6.1 Problem

Current provider implementations store an API key in the provider struct (e.g. `pkg/core/providers/openai.Provider.apiKey`) and create a new `http.Client` per provider instance.

In a hosted gateway we eventually need:
- BYOK (dynamic per request) without creating a brand-new `http.Client`/`Transport` for every request, so we preserve connection pooling and avoid per-request TCP/TLS churn.

### 6.2 Phase 1 (BYOK): simplest correct path

- Create provider instances per request (or per request/provider) using the caller’s BYOK header.
- Inject a shared, configured `*http.Client` (or shared `Transport`) so all those short-lived provider structs reuse connections.

Required core/provider hardening:
- Standardize HTTP client configuration:
  - request timeout policy: use request context deadlines + transport timeouts
  - sane idle conn pooling
  - TLS handshake timeout
  - response header timeout

Plan:
- Add provider `Option` to inject `*http.Client` (if not already) across providers.
- In the gateway, construct an `http.Client` (or `http.Transport`) once and pass it to providers created per request.

### 6.3 Phase 2 (scaling BYOK): two viable designs

Option A: Provider cache keyed by upstream key
- Keep providers “keyful” as today.
- Maintain an LRU/TTL cache:
  - key = `{providerName, apiKey, baseURL, compatibilityOptions}`
  - value = provider instance with shared `http.Client`
- Pros:
  - minimal provider code changes
  - quick path to BYO
- Cons:
  - unbounded key cardinality risk unless aggressively limited
  - memory pressure and eviction complexity

Option B (recommended): Key is resolved per request via context
- Refactor provider implementations to avoid storing apiKey directly, and instead:
  - take an `APIKeyProvider` function/interface and set headers using `apiKey(ctx)`
- Pros:
  - no key-cache needed
  - clean semantics for managed + BYO + per-tenant upstream routing
- Cons:
  - requires touching each provider implementation

Recommendation:
- Phase 1: implement BYOK proxy using per-request provider instances + shared `http.Client`.
- Phase 2: implement Option B for long-term correctness and simplicity; keep old constructors as wrappers.

Concrete interface sketch:
- `type APIKeyProvider func(ctx context.Context) (string, error)`
- Provider stores `apiKeyProvider` and calls it while building the outbound request.
- Gateway places resolved upstream keys into request context:
  - `ctx = context.WithValue(ctx, upstreamKeyCtxKey("openai"), key)`

---

## 7. Request Validation and Policy

### 7.1 Model allowlisting

Hosted gateway must not allow arbitrary upstream model names by default.

Config-driven allowlist:
- `allowed_models: ["anthropic/claude-sonnet-4", "openai/gpt-4o", ...]`
- Optionally include a per-tenant allowlist override.

Validation rules:
- Reject model strings not in the allowlist with `invalid_request_error`.
- Reject unknown providers with `not_found_error` or `invalid_request_error` (choose one and standardize).

### 7.2 Schema validation (baseline)

Validate early, before calling providers:
- `model` present and `provider/model` format
- at least 1 message
- roles are `user` or `assistant` (and optionally `system` only via `system` field)
- `max_tokens` bounds
- `tools`:
  - names are unique and safe
  - tool schema is valid JSON schema (basic checks)
- request JSON shape and round-trip correctness:
  - `system` is either a string or `[]ContentBlock` (reject arbitrary arrays/objects)
  - each `tool.config` is decoded into the expected typed config struct for that tool type
  - reject unknown content block types in request bodies

### 7.3 URL ingestion policy (SSRF safety)

Gateway policy:
- The proxy does not fetch arbitrary URLs on behalf of users.
- If users include image URLs in content blocks, the gateway passes them to upstream providers (the upstream’s egress policy applies).
- Do not add “gateway fetch URL and base64 it” behavior in proxy mode.

If a future “web_fetch” feature is provided at the gateway layer:
- It must be allowlist-based and have strict egress controls.
- Treat as a separate feature with its own security review.

### 7.4 Provider Feature Compatibility Validation (Required)

Some providers (and some provider APIs) will silently ignore unsupported features if we let them (for example: tool types or content blocks that our translation layer does not map for a given provider).

Proxy mode should treat this as a correctness bug and fail fast:
- Validate requested **content block types** against the chosen provider/model.
  - Example: `pkg/core/providers/openai` (Chat Completions) currently translates `image` and `audio` blocks, but not `document`/`video`. Those should be rejected for `openai/*` requests instead of being silently dropped.
- Validate requested **native tool types** against the chosen provider API.
  - Example: OpenAI “native web search / code interpreter / file search” are available on the Responses API (`oai-resp/*`), not on Chat Completions (`openai/*`). If a request targets `openai/*` but includes `tools:[{"type":"web_search"}]`, the gateway should reject and suggest:
    - switch to `oai-resp/<model>` when you want provider-native tools, or
    - use VAI-managed function tools (like `vai_web_search`) with `/v1/runs` or client-side tool execution.

Implementation plan:
- Add a gateway-side validator that runs after parsing the model string but before calling the provider.
- Prefer to move “unsupported feature” validation into provider implementations (similar to `pkg/core/providers/oai_resp.Provider.validateRequest`) so direct-mode and proxy-mode share exact behavior.

---

## 8. Streaming (SSE) Implementation Details

### 8.1 SSE formatting

For each `types.StreamEvent`:
- `event: <EventType()>`
- `data: <json>`
- blank line terminator

Example:
```
event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi"}}

```

Voice streaming addition:
- When `request.voice.output` is enabled, the gateway emits additional `types.AudioChunkEvent` events:
  - `event: audio_chunk`
  - `data: {"type":"audio_chunk","format":"pcm","audio":"<base64>","sample_rate_hz":24000}`

### 8.2 Flush and disconnect behavior

Requirements:
- Set headers:
  - `Content-Type: text/event-stream; charset=utf-8`
  - `Cache-Control: no-cache`
  - `Connection: keep-alive`
- Use an `http.Flusher` and flush after each event.
- If the client disconnects:
  - stop the upstream stream
  - ensure provider `EventStream.Close()` is called
  - record stream termination reason in logs/metrics

### 8.3 Ping / keepalive

Support periodic `ping` events (`types.PingEvent`) to keep intermediaries from timing out.
- Interval should be configurable (e.g. 15s).
- Only send if no provider events have been emitted recently.

---

## 9. Errors: Canonical Envelope + HTTP Mapping

### 9.1 Canonical error response

All HTTP errors return:
```json
{
  "error": {
    "type": "invalid_request_error",
    "message": "explanation",
    "param": "field_name",
    "code": "provider_specific_or_gateway_code",
    "request_id": "req_...",
    "retry_after": 10
  }
}
```

Where the inner shape is compatible with `pkg/core.Error` (`pkg/core/errors.go`).

### 9.2 Mapping provider errors

Today provider packages expose provider-specific error types.

Proxy mode must translate them to the canonical envelope:
- `invalid_request_error` -> `400`
- `authentication_error` -> `401`
- `permission_error` -> `403`
- `not_found_error` -> `404`
- `rate_limit_error` -> `429` (+ `Retry-After` if available)
- `overloaded_error` -> `503`
- `api_error` / `provider_error` -> `502` or `500` (pick and standardize)

Implementation plan:
- Create `pkg/gateway/apierror` with:
  - `func FromError(err error, requestID string) (*core.Error, httpStatus int)`
  - explicit `errors.As` checks for known provider error structs
  - a safe fallback for unknown errors

Streaming errors:
- Emit a terminal SSE event:
  - `event: error`
  - `data: {"type":"error","error":{...}}`
- Then close the connection.

Error shape alignment (proxy v1 decision):
- SSE terminal `error` events embed the same inner error object as non-streaming HTTP errors (same fields as `core.Error`).

---

## 10. Usage Accounting and Cost (Launch Minimal, Expand Later)

### 10.1 What we can do immediately

For every request:
- Record:
  - request_id, project_id
  - provider, model
  - input/output/total tokens (if returned)
  - latency
  - stop_reason (non-streaming) or stream completion state
- Return token headers to the caller when available.

### 10.2 Cost estimation (optional for launch)

If we want `usage.cost_usd` consistently:
- Maintain a pricing table keyed by normalized model ID.
- Compute cost from `usage.input_tokens` and `usage.output_tokens`.

Plan:
- Add `pkg/gateway/pricing` with a versioned pricing config file.
- Treat missing pricing as “unknown cost” rather than guessing.

---

## 11. Observability: Logs, Metrics, Tracing

### 11.1 Request IDs

- Accept inbound `X-Request-ID` else generate `req_<ulid>` (or similar).
- Include `X-Request-ID` in every response.
- Include request_id in all logs.

### 11.2 Structured logs

Use `slog` consistently:
- log start + end of request
- include: principal/project_id, provider/model, status, duration_ms
- for streaming: include completion reason (`completed`, `client_disconnect`, `upstream_error`)

### 11.3 Metrics (phase 1 or 1.5)

Expose Prometheus metrics behind optional config:
- `vai_http_requests_total{route,method,status}`
- `vai_upstream_requests_total{provider,status}`
- `vai_request_duration_seconds_bucket{route,provider}`
- `vai_stream_disconnects_total{provider}`

### 11.4 Tracing (later)

Add OpenTelemetry hooks after launch if needed:
- trace per request
- propagate `traceparent`

---

## 12. Repository and Package Layout Plan

### 12.1 New binary

- `cmd/vai-proxy/main.go`
  - loads config
  - initializes shared HTTP client/transport for upstream calls
  - (optional) initializes managed-key provider registry for hybrid mode
  - wires middleware: request IDs, auth, rate limiting, logging, panic recovery
  - starts HTTP server

### 12.2 New gateway packages (proposed)

- `pkg/gateway/server`
  - HTTP router + handler wiring
- `pkg/gateway/handlers`
  - `MessagesHandler` (POST /v1/messages)
  - `ModelsHandler` (GET /v1/models)
  - `HealthHandler` (GET /healthz, /readyz)
- `pkg/gateway/auth`
  - API key parsing + `Principal` resolution
- `pkg/gateway/ratelimit`
  - per-key rate + concurrency limiting
- `pkg/gateway/apierror`
  - canonical error envelope + mapping
- `pkg/gateway/config`
  - env/file config, validation
- `pkg/gateway/usage`
  - usage event emission hooks (log-only v1, DB later)

Notes:
- Keep `pkg/core` unchanged as much as possible in phase 1.
- Avoid importing `sdk` from the proxy to prevent pulling in direct-mode tool-loop APIs as “server surface area”.
  - Instead, the proxy should call `pkg/core.ParseModelString` and use gateway-owned provider factories (BYOK header -> provider instance) plus shared HTTP client configuration.

### 12.3 Extract shared provider bootstrap (managed/hybrid) (recommended refactor)

Today provider registration logic lives in `sdk/client.go`.

Plan:
- Create `pkg/bootstrap/providers.go` with:
  - `func RegisterManagedProviders(engine *core.Engine, cfg ProviderConfig, logger *slog.Logger) error`
- Both:
  - `sdk.NewClient()` and
  - `cmd/vai-proxy`
  call the same provider registration function.

This reduces drift and ensures proxy mode uses identical provider option defaults.

---

## 13. Test Plan (Proxy Mode)

### 13.1 Unit tests (required)

- Auth:
  - missing/invalid bearer token
  - key lookup and principal extraction
- Rate limiting:
  - request bursts -> 429 with Retry-After
  - concurrent stream limit enforcement
- Request validation:
  - invalid model format
  - disallowed model/provider
  - oversized request body
- Error mapping:
  - map known provider errors -> canonical envelope + status code

### 13.2 Handler tests with fake providers (required)

Create a `fakeProvider` implementing `core.Provider` and a `fakeEventStream` implementing `core.EventStream`.

Tests:
- `POST /v1/messages` non-stream:
  - returns `types.MessageResponse` unchanged
- `POST /v1/messages` stream:
  - emits SSE lines with correct `event:` and `data:` json
  - flush semantics: ensure output is incremental (best-effort; in tests we can at least verify ordering)

### 13.3 Integration tests (optional for phase 1)

- Start the proxy in-process and run a small matrix against live providers only when credentials are present (build tag `integration`).
- Reuse the existing integration gating philosophy in `integration/*`.

---

## 14. Milestones (Execution Order)

### Milestone 1: Skeleton server + health
1. Add `cmd/vai-proxy` with config loading and HTTP server wiring.
2. Add `/healthz` and `/readyz`.
3. Add request ID middleware + structured logs.
4. Add optional CORS middleware with an explicit origin allowlist (browser support).

### Milestone 2: Gateway auth + rate limits
1. Add API key store (config-backed in-memory).
2. Require `Authorization: Bearer`.
3. Enforce per-key rate + concurrency limits.
4. Redact BYOK headers in all request logs.

Implementation detail:
- Make gateway auth configurable via `auth_mode`:
  - `required` / `optional` / `disabled`
- Even when `auth_mode=disabled`, keep global abuse limits (body size, streaming duration, etc.).

### Milestone 3: BYOK `/v1/messages` (non-stream)
1. Implement gateway request parsing and validation (including model allowlist).
2. Parse `req.model` into `(provider, modelName)` and set `reqCopy.model = modelName` for the provider call.
3. Resolve upstream BYOK header for the selected provider (and reject if missing).
4. Create a provider instance using the resolved key and a shared `*http.Client`.
5. If `request.voice.input` is set: preprocess input audio (STT) before provider call.
6. Execute the request and return `types.MessageResponse`.
7. If `request.voice.output` is set: append synthesized audio to the final response (TTS).
8. Canonical error envelope + status mapping.

Implementation detail:
- Decode requests strictly so the HTTP JSON contract is actually `pkg/core/types`:
  - `MessageRequest.System` must decode to `string` or `[]ContentBlock` (not `[]any`)
  - `Tool.Config` must decode into typed config structs (not `map[string]any`)
  - reject unknown request content blocks (do not fall back to placeholder blocks)

### Milestone 4: Streaming `/v1/messages` (SSE)
1. Parse `req.model` into `(provider, modelName)` and set `reqCopy.model = modelName` for the provider call.
2. Resolve BYOK header and create a provider instance with a shared `*http.Client`.
3. If `request.voice.input` is set: preprocess input audio (STT) before provider call.
4. Call the provider’s streaming method and write SSE events in canonical `types.StreamEvent` JSON shapes.
5. If `request.voice.output` is set: synthesize incremental TTS and emit `audio_chunk` SSE events alongside upstream text deltas.
6. Handle pings and disconnect cleanup.

### Milestone 4.5: Live Audio Mode (`/v1/live` WebSocket)

Implement the Live endpoint per `LIVE_AUDIO_MODE_DESIGN.md`:
- `wss://<gateway>/v1/live`
- strict handshake/versioning (`hello` / `hello_ack`)
- streaming STT + 600ms silence endpointing + barge-in reset semantics
- streaming TTS tagged by `assistant_audio_id`
- enforce per-tenant concurrency + bandwidth limits

### Milestone 5: `/v1/runs` and `/v1/runs:stream` (recommended next)

Why this exists:
- Non-Go SDKs become much thinner if they do not need to re-implement the tool loop semantics of `sdk/RunStream`.
- It also makes Live Audio Mode easier to build: the live WS session can drive the same run controller semantics.

Proposed endpoints:
- `POST /v1/runs` (blocking; returns final result + optional step summary)
- `POST /v1/runs:stream` (SSE; streams run lifecycle events)

Key design decision required before implementing:
- Tool execution contract:
  - **Gateway-managed tools only (recommended v1)**: the gateway executes a small allowlisted set of function tools it ships with (start with `vai_web_search`; optionally add `vai_web_fetch` if needed).
    - Everything else is either provider-native or client-executed.
    - If the request includes function tools outside the allowlist, reject with a clear error that directs callers to use `POST /v1/messages` and execute tools in their app code, or to register HTTP tools (future).
  - **Registered HTTP tools (recommended long-term)**: per-tenant tool registry mapping `{tool_name -> webhook/callback URL + auth}`; gateway invokes tools by HTTP when the model calls them.
  - Avoid “upload code and run it in the gateway” for security reasons.

Request/response shape proposal:
- `POST /v1/runs` body:
  - `{ "request": <types.MessageRequest>, "run": { "max_turns": 8, "max_tool_calls": 20, "max_tokens": 0, "timeout_ms": 60000, "parallel_tools": true, "tool_timeout_ms": 30000 } }`
- Response includes:
  - final `types.MessageResponse`
  - aggregated usage
  - stop reason
  - optional step trace for debugging (bounded / configurable)

Event model:
- Reuse the semantics of `sdk/RunStream.Events()` (step start/complete, tool calls/results, history deltas, wrapped provider stream events).
- For server API stability, define these event types in a server-owned package (not `sdk`) so non-Go SDKs can rely on them.

### Milestone 6: `/v1/models` and model allowlist
1. Implement allowlist config and validation.
2. Expose `/v1/models` from config + provider capability metadata.

### Milestone 6.5: OpenAPI spec (for non-Go SDKs)
1. Add `api/openapi.yaml` describing `/v1/messages`, `/v1/runs(:stream)`, and `/v1/models`.
2. Treat OpenAPI as a compatibility contract:
   - changes require explicit review and versioning.
3. Use it to generate thin clients for JS/Python/Rust.

### Milestone 7: Usage events
1. Add per-request usage capture.
2. Emit structured usage logs (and optionally store).
3. Add `X-*-Tokens` headers when possible.

### Milestone 8: Managed keys / hybrid mode (optional)
1. Add managed-keys fallback (optional) for self-hosted / hybrid tenants.
2. Implement upstream key resolution strategy (provider refactor or cache) if per-request provider creation becomes a bottleneck.
3. Add hybrid tenant policy controls (BYOK-only, managed-only, or prefer-BYOK).

### Milestone 9: Live-audio readiness hooks (no WS yet)
1. Ensure server config supports WS listen path reservation.
2. Ensure auth and rate-limit primitives work for long-lived connections.
3. Ensure request ID + metrics can be reused for WS sessions.

---

## 15. Open Questions to Resolve Before Coding

1. Which features are required for launch:
   - BYOK-only, or hybrid managed fallback?
2. What is the initial allowlisted model set?
3. Do we need `/v1/messages` compatibility with Anthropic headers (like `anthropic-version`) as passthrough, or do we keep VAI-only headers?
4. What storage is expected for gateway API keys at launch:
   - config file/env only, or Postgres?
5. Request size constraints for multimodal:
   - target max body size and base64 limits.

6. Browser clients:
   - confirm hosted default is CORS disabled, with explicit `allowed_origins` allowlist when browser usage is desired.
   - confirm whether any endpoints should remain “server-to-server only” (no CORS) even when CORS is enabled globally.

7. `Run` API tool contract:
   - confirm v1 is gateway-managed tools only (start with `vai_web_search`).
   - decide whether to include `vai_web_fetch` in v1.
   - confirm behavior when non-allowlisted function tools are present (recommended: reject with guidance to use `/v1/messages`).

---

## 16. How This Sets Up Live Audio Mode

`LIVE_AUDIO_MODE_DESIGN.md` assumes a future WS endpoint that will need:
- the same auth and rate limiting layer
- the same request ID and observability conventions
- provider routing and credential resolution

Proxy mode should therefore:
- keep all cross-cutting concerns (auth, ratelimit, config, logging) in reusable packages
- keep `pkg/core` as the shared, provider-agnostic translation surface

When proxy mode is complete, Live Audio Mode can be implemented as:
- a WS handler under the same server, with session lifecycle and backpressure control
- built on the same provider registry and voice-provider configuration patterns
