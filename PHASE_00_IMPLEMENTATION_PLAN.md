# Phase 00 — Decisions, Defaults, and Cross-Cutting Contracts (Implementation Plan)

This phase exists to lock the “small but expensive to change later” decisions before significant gateway code is written. It is intentionally heavy on explicit defaults, wire contracts, and invariants.

Source of truth for locked decisions:
- `PHASE_00_DECISIONS_AND_DEFAULTS.md`

Primary downstream plans:
- `PROXY_MODE_IMPLEMENTATION_PLAN.md`
- `API_CONTRACT_HARDENING.md`
- `RUNS_API_AND_EVENT_SCHEMA.md`
- `LIVE_AUDIO_MODE_DESIGN.md`

---

## Current Status (as of 2026-02-24)

Phase 00 is primarily about *locking decisions*; those are now locked and reflected in code and docs.

Implemented artifacts that depend on Phase 00 decisions:
- Gateway binary exists: `cmd/vai-proxy/main.go`
- Gateway skeleton + middleware exists: `pkg/gateway/*`
- `/v1/messages` exists (JSON + SSE): `pkg/gateway/handlers/messages.go`
- Strict request decode exists (Phase 01 deliverable, but it operationalizes Phase 00’s “strict contract” intent): `pkg/core/types/strict_decode.go`

Decisions alignment notes:
- Voice is **BYOK** (not gateway-managed):
  - `/v1/messages` voice requires `X-Provider-Key-Cartesia` when `request.voice` is set.
  - Live mode may additionally require `X-Provider-Key-ElevenLabs` depending on negotiated provider.
- SSE error parity is already supported by the `types.Error` struct fields (see `pkg/core/types/stream.go`).

Remaining Phase-00-adjacent work (tracked in later phases):
- Centralized defaults table in *operator-facing config docs* and enforcement of all limits (CORS, concurrency, SSE ping, base64 budgets, etc.).

## 0. Exit Criteria (Definition of Done)

Phase 00 is complete when all of the following are true:

1. **Defaults are explicit and centralized**
   - A single “defaults table” exists (numbers, units, rationale) and is referenced by gateway config docs.
   - Every default is overridable by config (env/file) without code changes.

2. **Voice is unambiguous**
   - Proxy v1 supports `voice` for:
     - `/v1/messages` non-stream (STT in, TTS out)
     - `/v1/messages` SSE (STT in, TTS audio chunks out + final audio block)
   - Live Audio Mode is in v1 scope and has a fixed endpoint: `wss://<gateway>/v1/live`.

3. **Streaming errors are contractually identical**
   - Non-stream HTTP error inner object == SSE terminal `error` inner object (same fields).

4. **SSE voice chunk contract is nailed down**
   - The gateway emits a stable, documented SSE event for voice audio chunks (type, fields, encoding, sample rate semantics).

5. **Readiness semantics match BYOK reality**
   - `/readyz` does not require “managed provider exists” in BYOK-first mode.

6. **Docs are consistent**
   - No doc claims proxy v1 rejects `voice` or excludes Live WS.
   - Any plan section that depends on Phase 00 decisions points at `PHASE_00_DECISIONS_AND_DEFAULTS.md`.

---

## 1. Locked Decisions (Recap)

These are locked by `PHASE_00_DECISIONS_AND_DEFAULTS.md` and should be treated as constraints by later phases:

### 1.1 `/v1/messages` voice support

- `voice.input`: gateway performs STT (Cartesia) on `audio` blocks and replaces them with `text` blocks for provider execution.
- `voice.output`:
  - non-stream: append final `audio` block (base64) to response
  - stream: emit incremental `audio_chunk` SSE events (base64 PCM), and still append a final `audio` block in the final response.

### 1.2 Live Audio Mode (`/v1/live`)

- Endpoint is WebSocket: `wss://<gateway>/v1/live`.
- Wire protocol and semantics follow `LIVE_AUDIO_MODE_DESIGN.md` (hello/hello_ack, endpointing, barge-in, playback marks, etc).

### 1.3 SSE error parity

- SSE terminal `error` event contains the same inner fields as non-streaming HTTP errors (compatible with `core.Error`).

### 1.4 BYOK provider keys vs gateway-managed voice keys

- LLM upstream provider keys: BYOK via request headers (per `PROXY_MODE_IMPLEMENTATION_PLAN.md`).
- Voice provider keys:
  - `/v1/messages` voice uses **BYOK** via `X-Provider-Key-Cartesia` when `request.voice` is set.
  - Live mode may use **BYOK** via `X-Provider-Key-ElevenLabs` (primary) and/or `X-Provider-Key-Cartesia` (fallback).

---

## 2. Defaults Table (Must Pick Numbers)

This section defines the exact defaults that should be committed for proxy v1. If any value is controversial, set it conservatively and expose as config.

### 2.1 Request limits (HTTP JSON)

Recommended initial defaults:
- `http.max_body_bytes`: **8 MiB**
  - Rationale: supports moderate multimodal (including base64) without inviting abuse.
- `http.max_messages`: **64**
- `http.max_total_text_bytes`: **512 KiB**
  - Computed as UTF-8 bytes across all `text` blocks + string content.
- `http.max_tools`: **64**

### 2.2 Base64 payload limits (multimodal)

These must be enforced before expensive decode work.

Recommended initial defaults:
- `multimodal.max_b64_bytes_per_block`: **4 MiB decoded**
- `multimodal.max_b64_bytes_total`: **12 MiB decoded**

Implementation note (later phases):
- Enforce via base64 length estimation (`decoded ≈ len(b64)*3/4 - padding`) without decoding when possible.
- Enforce per-block and total across `image/audio/video/document` blocks.

### 2.3 SSE limits (`/v1/messages?stream=true`, `/v1/runs:stream`)

Recommended initial defaults:
- `sse.max_stream_duration`: **5 minutes**
- `sse.ping_interval`: **15 seconds**
- `sse.max_concurrent_streams_per_principal`: **4**

### 2.4 WebSocket limits (`/v1/live`)

Recommended initial defaults:
- `ws.max_session_duration`: **2 hours** (configurable; may be shorter for hosted)
- `ws.max_concurrent_sessions_per_principal`: **2**
- `ws.max_inbound_frame_bytes`: **256 KiB** (JSON frames)
- `ws.max_inbound_audio_bytes_per_second`: **(choose)** (start with ~128–256 KiB/s, depending on PCM format)
- `ws.audio_in_required`: `pcm_s16le@16000Hz mono` (recommended by `LIVE_AUDIO_MODE_DESIGN.md`)
- `ws.audio_out_default`: `pcm_s16le@24000Hz mono` (recommended by `LIVE_AUDIO_MODE_DESIGN.md`)

### 2.5 Timeouts (outbound)

Recommended initial defaults:
- `upstream.connect_timeout`: **5s**
- `upstream.response_header_timeout`: **30s**
- `upstream.total_request_timeout` (non-stream): **2 minutes**
- `upstream.stream_idle_timeout` (stream): **60s** (paired with SSE ping)

### 2.6 Redaction and logging defaults

Required invariants:
- Never log:
  - `Authorization` bearer tokens
  - BYOK provider key headers
  - request/response audio base64 payloads
  - WebSocket binary audio frames (or base64 audio fields)
- Logs should include:
  - `request_id`
  - `principal_id`/`project_id` (or “anonymous” for `auth_mode=optional|disabled`)
  - route, provider, model, status, duration
  - stream termination reason (`completed`, `client_disconnect`, `upstream_error`)

---

## 3. Wire Contracts to Freeze

### 3.1 Voice SSE event: `audio_chunk`

Canonical event (SSE):
- `event: audio_chunk`
- `data: { ... }` where payload is `pkg/core/types.AudioChunkEvent`:
  - `type`: `"audio_chunk"`
  - `format`: `"pcm"` (proxy v1 emits PCM for streaming)
  - `audio`: base64 string
  - `sample_rate_hz`: (recommended always set for PCM)
  - `is_final`: optional; true only for the terminal chunk if the gateway chooses to mark it

Notes:
- This event is gateway-emitted; providers will not emit it.
- Clients that ignore `audio_chunk` should still function by:
  - rendering streamed text deltas, and
  - using the final appended `audio` content block in the response when the stream ends.

### 3.2 Error inner object parity (HTTP vs SSE)

Freeze the inner object fields to match `core.Error`:
- required: `type`, `message`
- optional: `param`, `code`, `request_id`, `retry_after`, `provider_error`

### 3.3 `/readyz` semantics (BYOK-first)

Freeze readiness definition:
- Must validate gateway config and router construction.
- Must validate optional dependencies only when configured/enabled.
- Must not require managed providers in BYOK-only mode.

---

## 4. Documentation Work Items (Phase 00 Tasks)

1. Ensure `PHASE_00_DECISIONS_AND_DEFAULTS.md` includes:
   - the defaults table (numbers) and references to where they are enforced in code (once implemented).
   - explicit voice keying model (gateway-managed Cartesia).
   - explicit Live endpoint path (`/v1/live`).

2. Ensure plan docs are consistent:
   - `PROXY_MODE_IMPLEMENTATION_PLAN.md` scope and milestones include:
     - `/v1/messages` voice STT/TTS
     - SSE `audio_chunk`
     - Live WS milestone
   - `API_CONTRACT_HARDENING.md` reflects “voice supported” and describes the shared-helper extraction goal.

3. Add a short “operator-facing” paragraph (later, in gateway README/docs) describing:
   - required secrets (`CARTESIA_API_KEY` + BYOK headers for LLM providers)
   - CORS enablement strategy (allowlist)

Update for current decisions:
- Replace `CARTESIA_API_KEY` mention with BYOK headers:
  - `X-Provider-Key-<LLM provider>` per request (LLMs)
  - `X-Provider-Key-Cartesia` per request when using `/v1/messages` voice (and for Live STT/TTS fallback)
  - `X-Provider-Key-ElevenLabs` per request when using Live mode with ElevenLabs TTS

---

## 5. Engineering Work Items That Must Exist Before Phase 02+

Even if the gateway code is not written yet, Phase 00 should ensure these foundational “types/contracts” are in place:

1. `pkg/core/types` includes:
   - `AudioChunkEvent` as a `StreamEvent`
   - expanded streaming error inner object fields

2. `UnmarshalStreamEvent` supports:
   - `type=audio_chunk`

3. Tests cover:
   - `AudioChunkEvent` roundtrip decode
   - `ErrorEvent` decode with extended fields

Status:
- `AudioChunkEvent` exists and `UnmarshalStreamEvent` supports it (see `pkg/core/types/stream.go`).
- Strict decoding tests exist for request bodies (see `pkg/core/types/strict_decode_test.go`).

---

## 6. Phase 00 Test Plan

Unit tests (must pass):
- `go test ./...`

Manual contract checks (doc-level):
- Verify no remaining docs claim “proxy v1 rejects voice” or “live WS is post-proxy”.
- Verify all sample payloads in docs match the frozen SSE and error shapes.

---

## 7. Phase 00 Risks and Mitigations

1. **Underspecified limits become production outages**
   - Mitigation: choose conservative defaults + make everything configurable + document recommended hosted settings.

2. **Voice streaming contract conflicts with future Live protocol**
   - Mitigation: treat `/v1/messages` voice SSE as “simple voice” (base64 PCM chunks) and keep Live protocol as the higher-fidelity interactive experience (binary frames, playback marks, resets).

3. **Operator confusion about which keys are BYOK vs managed**
   - Mitigation: explicitly document:
     - BYOK is for upstream LLM providers per request headers
     - voice providers are also BYOK via request headers (Cartesia for `/v1/messages` voice; ElevenLabs for Live TTS)
