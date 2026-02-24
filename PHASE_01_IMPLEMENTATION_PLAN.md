# Phase 01 — API Contract Hardening + Shared Voice Helpers (Implementation Plan)

This phase makes the statement “HTTP contract = JSON of `pkg/core/types`” true for gateway request bodies, and it lays the shared helper foundation required to implement proxy voice correctly (without copy/paste from the SDK).

Primary references:
- `API_CONTRACT_HARDENING.md` (source of truth for strict decoding rules)
- `PHASE_00_DECISIONS_AND_DEFAULTS.md` (locked defaults + voice/live decisions)
- `PROXY_MODE_IMPLEMENTATION_PLAN.md` (gateway milestone sequencing)
- `RUNS_API_AND_EVENT_SCHEMA.md` (server-side run endpoints; strict decoding must support them)

Non-goal of Phase 01:
- Do **not** implement the gateway HTTP server in this phase.
- Do **not** implement `/v1/messages` handlers yet.
- Do **not** implement Live WS (`/v1/live`) yet.

---

## Current Status (as of 2026-02-24)

Implemented:
- Strict request decoding helpers exist and are unit tested:
  - `pkg/core/types/strict_decode.go`
  - `pkg/core/types/strict_decode_test.go`
- The proxy gateway uses strict decoding at the `/v1/messages` boundary:
  - `pkg/gateway/handlers/messages.go`

Shared voice helpers are now implemented and used by both SDK and gateway:
- Shared helper APIs:
  - `pkg/core/voice/helpers_messages.go`
  - `pkg/core/voice/streaming_tts.go`
- SDK refactors:
  - `sdk/messages.go` uses shared STT preprocess + non-stream TTS append helpers.
  - `sdk/stream.go` uses shared streaming TTS helper for sentence buffering + audio forwarding.
- Gateway refactors:
  - `pkg/gateway/handlers/messages.go` uses shared STT preprocess + non-stream TTS append helpers.
  - `pkg/gateway/handlers/messages.go` uses shared streaming TTS helper for SSE `audio_chunk` emission.

Unit tests added:
- `pkg/core/voice/helpers_messages_test.go`
- `pkg/core/voice/streaming_tts_test.go`

Phase 01 is now considered complete. The remaining gateway work is Phase 02+ hardening and new endpoints (runs/live).

## 0. Exit Criteria (Definition of Done)

Phase 01 is complete when:

1. Gateway-grade strict decoders exist and are tested:
   - strict union decoding for:
     - `MessageRequest.System`
     - `Message.Content`
   - strict decoding for `tools[].config` into typed config structs based on `tool.type`
   - strict rejection of unknown content block types in request bodies
   - strict validation of tool-loop history blocks:
     - `tool_use` shape (id/name/input)
     - `tool_result` shape (tool_use_id/content/is_error)
     - tool_use_id matching against prior tool_use blocks (default strict)

2. Provider parsing remains lenient:
   - existing `UnmarshalContentBlock` (lenient) behavior remains unchanged for provider outputs.
   - strictness is opt-in through new explicit APIs.

3. Shared voice helper APIs exist (no copy/paste later):
   - a gateway can call a shared helper to:
     - STT preprocess a `*types.MessageRequest` (`voice.input`)
     - append TTS output to a `*types.MessageResponse` (`voice.output`, non-stream)
   - streaming voice output helper is *scaffolded* or implemented if it’s required by Phase 03/04 approach.

4. Tests provide real coverage:
   - unit tests for strict decoding and validation cover the “silent drop” failure modes explicitly.
   - unit tests verify typed tool config decoding results in concrete config pointer types (not `map[string]any`).

---

## 1. Package and File Layout (Proposed)

### 1.1 Strict request decoding (core/types)

Add the strict decoding entrypoints under `pkg/core/types`:
- Implemented as a single focused file:
  - `pkg/core/types/strict_decode.go`
    - `func UnmarshalMessageRequestStrict(data []byte) (*MessageRequest, error)`
    - `func UnmarshalContentBlockStrict(data []byte) (ContentBlock, error)`
    - `func UnmarshalContentBlocksStrict(data []byte) ([]ContentBlock, error)`
    - strict tool config typing + tool history validation
  - Tests:
    - `pkg/core/types/strict_decode_test.go`

Design rule:
- Do not modify `Message.UnmarshalJSON` or the existing lenient unmarshaling paths used by direct-mode SDK and provider outputs.

### 1.2 Shared voice helpers (core/voice)

Add shared helper APIs under `pkg/core/voice` (or a subpackage) so gateway and SDK can share semantics:
- `pkg/core/voice/helpers_messages.go`
  - `func PreprocessMessageRequestInputAudio(ctx context.Context, pipeline *voice.Pipeline, req *types.MessageRequest) (processed *types.MessageRequest, userTranscript string, err error)`
  - `func AppendVoiceOutputToMessageResponse(ctx context.Context, pipeline *voice.Pipeline, req *types.MessageRequest, resp *types.MessageResponse) error`

Notes:
- These helpers are intentionally “pure-ish”: they should not depend on the SDK package.
- They should not mutate the caller’s `req` in-place (copy-on-write).
- They should only execute when `req.Voice != nil` and relevant subfields are present.

Optional (if desired now, but can be deferred to Phase 03/04):
- a small “streaming voice output” helper that:
  - consumes text deltas
  - batches them to sentence boundaries
  - feeds a streaming TTS context
  - emits `types.AudioChunkEvent` objects for the gateway SSE writer

---

## 2. Strict Decoding: Detailed Requirements

### 2.1 Request body decoding must be strict

Strict mode is used for gateway **request bodies only**.

General JSON parsing requirements:
- Reject unknown top-level fields (recommended for proxy endpoints) OR at minimum reject unknown fields for the key union fields where ambiguity causes silent drops.
  - If you choose strict unknown-field rejection, implement it in gateway handlers using `json.Decoder.DisallowUnknownFields()`.
  - The core strict helpers should focus on union decoding and typed config decoding.

### 2.2 `MessageRequest.System` union decoding

Allowed shapes:
- JSON string → `req.System = string`
- JSON array of content blocks → `req.System = []ContentBlock` (strict blocks)

Reject:
- object / number / bool / null when `system` is present

Error reporting:
- return an error compatible with gateway error mapping (later), including `param="system"`.

### 2.3 `Message.Content` union decoding

Allowed shapes:
- JSON string → `Message.Content = string`
- JSON array of content blocks → `Message.Content = []ContentBlock` (strict blocks)

Reject:
- object / number / bool / null when `content` is present

### 2.4 Strict content block decoding rules

Strict content blocks for request bodies:
- must have `type`
- must match one of the known request block types
- must have required fields for that type
- **unknown block types are errors** (no placeholder fallback)

Important: keep the existing lenient `UnmarshalContentBlock` as-is for provider outputs.

### 2.5 Strict tool config decoding

For each `tools[i]`:
- decode `type`
- then decode `config` into a typed struct based on tool type:
  - `web_search` → `*types.WebSearchConfig`
  - `web_fetch` → `*types.WebFetchConfig`
  - `code_execution` → `*types.CodeExecutionConfig`
  - `computer_use` → `*types.ComputerUseConfig`
  - `file_search` → `*types.FileSearchConfig`
  - `text_editor` → `*types.TextEditorConfig` (allow `{}` or null)
  - `function`:
    - `config` must be absent or null (reject if present)

Reject:
- unknown `tool.type`
- config decoding errors (wrong shape)

### 2.6 Strict tool history validation (`tool_use` / `tool_result`)

Validate within the provided request history:

`tool_use` blocks:
- `id` non-empty
- `name` non-empty
- `input` must decode as a JSON object (map), not scalar/array

`tool_result` blocks:
- `tool_use_id` non-empty
- `content` must be an array of strictly decoded content blocks
- `is_error` optional

ID matching:
- Every `tool_result.tool_use_id` must match a prior `tool_use.id` present in the same request history.
- Default behavior is strict error (not warning).

Provider-native tool blocks:
- Strict decoding may still parse known provider-native blocks (e.g. `server_tool_use`, `web_search_tool_result`) because they exist in `pkg/core/types/content.go`.
- Provider compatibility validation (later phase) may reject them depending on provider/model.

---

## 3. Voice Helpers: Detailed Requirements

### 3.1 Input audio preprocessing (STT)

Behavior (when `req.Voice.Input != nil`):
- Scan `req.Messages[*].content` for `audio` blocks.
- Decode base64 audio bytes.
- Transcribe via `pipeline.ProcessInputAudio`.
- Replace audio blocks with text blocks containing the transcript (preserve role and non-audio blocks).
- Return:
  - processed request copy
  - `userTranscript` string (concatenation of audio transcripts)

Error behavior:
- Return an error if:
  - base64 decoding fails
  - STT provider errors

### 3.2 Output voice append (TTS) for non-streaming

Behavior (when `req.Voice.Output != nil`):
- Extract final assistant text from `resp.TextContent()`.
- If empty, no-op.
- Synthesize voice output via `pipeline.SynthesizeResponse`.
- Append a `types.AudioBlock` to `resp.Content`:
  - `source.type="base64"`
  - `source.media_type` derived from requested output format
  - `source.data` base64-encoded audio bytes
  - `transcript` set to assistant text (trimmed)

### 3.3 Streaming voice output integration (scaffolding)

Phase 03/04 will need a stable integration strategy for `/v1/messages` SSE:
- The upstream provider emits `types.ContentBlockDeltaEvent` with `types.TextDelta`.
- The gateway must:
  - forward upstream events unchanged, and
  - synthesize TTS incrementally and emit `types.AudioChunkEvent` as additional SSE events.

Two viable integration designs (pick one during Phase 03/04 implementation; Phase 01 prepares the building blocks):

Option A (gateway-owned streaming synthesizer):
- The SSE handler maintains a `SentenceBuffer`.
- When a `TextDelta` arrives, it pushes completed sentences into a streaming TTS context.
- A goroutine reads TTS audio chunks and emits `AudioChunkEvent` to the SSE writer.

Option B (shared helper object):
- Implement a reusable `voice.StreamingTTSMux` that:
  - exposes `OnTextDelta(text string)` and `Close()`
  - emits `AudioChunkEvent` via a channel or callback

Phase 01 should at least define which option is intended so Phase 03/04 doesn’t have to invent APIs mid-flight.

---

## 4. Test Plan (Phase 01)

### 4.1 Strict decoding unit tests (`pkg/core/types`)

Add a new test file: `pkg/core/types/request_strict_test.go` covering:

System union:
- string ok
- array ok
- object rejected (`param="system"`)

Message content union:
- string ok
- array ok
- object rejected (`param="messages[i].content"`)

Unknown content blocks:
- request contains `{"type":"new_future_block"}` → rejected

Tool configs:
- `web_search` config decodes to `*WebSearchConfig`
- `text_editor` accepts `{}` and null
- `function` rejects non-null config
- unknown tool type rejected

Tool history:
- `tool_use` missing id/name rejected
- `tool_use.input` non-object rejected
- `tool_result` missing tool_use_id rejected
- `tool_result.content` unknown block rejected
- tool_use_id mismatch rejected

Status:
- Covered by `pkg/core/types/strict_decode_test.go` (system union, content union, unknown blocks, tool config typing, tool history validation).

### 4.2 Voice helper unit tests (`pkg/core/voice`)

Add tests under `pkg/core/voice` using fake STT/TTS providers:
- Input:
  - request with one user message containing `audio` block base64
  - ensures returned messages have `text` block
  - ensures `userTranscript` is returned
- Output:
  - response with text content
  - ensures `audio` block appended with correct `media_type` and transcript

### 4.3 Ensure no behavior regression in existing tests

- Run `go test ./...`
- Verify provider packages unaffected (strict decoders are additive)

---

## 5. Risks and Mitigations

1. **Strict decoding accidentally changes direct-mode behavior**
   - Mitigation: strict code paths are new functions; do not change existing `UnmarshalJSON` methods.

2. **Tool config strict typing breaks valid requests**
   - Mitigation: keep configs permissive within each struct (unknown fields can be allowed unless gateway decides to disallow unknowns at top-level).

3. **Voice helper extraction causes circular deps**
   - Mitigation: keep helpers in `pkg/core/voice` and depend only on `pkg/core/types` + `pkg/core/voice/*`.

4. **Streaming voice output becomes hard to test later**
   - Mitigation: define a narrow helper interface now (Option A/B), with unit tests around sentence buffering and event emission.
