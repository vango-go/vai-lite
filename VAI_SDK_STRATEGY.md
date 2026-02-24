# VAI SDK Strategy (Python, TypeScript, Rust)

This document defines how non-Go SDKs should be designed to minimize duplicated logic and maximize correctness.

Core idea:
- If the gateway exposes `/v1/messages` (SSE) and `/v1/runs:stream` (SSE), non-Go SDKs can be *thin* HTTP clients.
- Without `/v1/runs:stream`, every SDK would need to re-implement the `RunStream` tool loop, which is a large and subtle surface area.

Related docs:
- `PROXY_MODE_IMPLEMENTATION_PLAN.md`
- `RUNS_API_AND_EVENT_SCHEMA.md`
- `TS_SDK_DESIGN.md`

---

## 1. Thin vs Thick SDKs

### 1.1 Recommended: thin SDKs

Thin SDKs:
- Build request objects in the canonical `pkg/core/types` JSON shapes
- Call gateway endpoints
- Provide ergonomic streaming primitives for:
  - `/v1/messages` SSE
  - `/v1/runs:stream` SSE
  - (future) Live mode WebSocket
- Do not implement provider translations or the full tool-loop state machine.

### 1.2 When a “thick” SDK is unavoidable

Only if you choose not to expose `/v1/runs:stream` and still want client-side tool-loop orchestration.

Recommendation: do not do this. It multiplies complexity across languages.

---

## 2. Endpoint Coverage Targets

Minimum viable SDK surface:
- `messages.create(request)` -> `POST /v1/messages` (non-stream)
- `messages.stream(request)` -> `POST /v1/messages` (SSE)
- `runs.create({request, run})` -> `POST /v1/runs` (blocking)
- `runs.stream({request, run})` -> `POST /v1/runs:stream` (SSE)

Future:
- `live.connect()` -> WebSocket live protocol (per `LIVE_AUDIO_MODE_DESIGN.md`)

---

## 3. Event Models

### 3.1 `/v1/messages` events

Stream events mirror `pkg/core/types.StreamEvent` (provider-shaped message streaming).

SDK responsibilities:
- parse SSE frames
- expose an idiomatic stream of typed events
- provide convenience extractors (text deltas, thinking deltas)

### 3.2 `/v1/runs:stream` events

Run events should mirror the semantics of Go `sdk/RunStream.Events()`:
- step lifecycle events
- tool call start/result events
- history deltas
- wrapped provider stream events

Design note:
- Define a server-owned JSON schema for run events (stable contract for SDKs).
- Avoid importing Go SDK types into the server. The server should emit its own JSON event types that are language-neutral.

---

## 4. Auth and Credentials in SDKs

Hosted default:
- Gateway key: `Authorization: Bearer vai_sk_...`
- BYOK headers:
  - `X-Provider-Key-Anthropic`, `X-Provider-Key-OpenAI`, etc.

Self-host:
- Support `auth_mode` at the server; SDKs should still allow setting gateway key and BYOK headers explicitly.

SDK ergonomics recommendation:
- One unified config:
  - `base_url`
  - `gateway_api_key` (optional)
  - `provider_keys` (dict/map by provider)
- SDK sets the correct headers automatically for requests.

---

## 5. Streaming Idioms by Language

TypeScript:
- Expose `AsyncIterable<Event>` for SSE
- Provide adapters for `ReadableStream` in browser environments

Python:
- Provide both sync and async clients if needed
- Async: `async for event in client.messages.stream(...): ...`

Rust:
- Expose a `Stream<Item = Result<Event, Error>>`

---

## 6. Recommended Sequencing

1. Gateway: `/v1/messages` + SSE (foundation)
2. Gateway: `/v1/runs` and `/v1/runs:stream` with gateway-managed tools (thin SDK unlock)
3. TypeScript SDK (best forcing function for a web chat app)
4. Chat app (uses TS SDK against gateway; validates end-to-end)
5. Python SDK
6. Live audio mode
7. Rust SDK
