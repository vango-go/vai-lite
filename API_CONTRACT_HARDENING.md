# VAI Gateway API Contract Hardening

This document defines the design decisions and implementation checklist required to make the VAI proxy’s HTTP API a correct, strict JSON contract over `pkg/core/types`, especially for BYOK proxy mode.

Why this is needed:
- Several core request fields are typed as `any` (e.g. `MessageRequest.System`, `Tool.Config`, `Message.Content`).
- Default `encoding/json` decoding will produce `map[string]any` / `[]any`, which does not match what provider translation code expects (typed structs and typed content blocks).
- `UnmarshalContentBlock` currently treats unknown types as a placeholder `TextBlock` for forward compatibility. That is good for parsing provider responses/events, but it is wrong for proxy request validation where we want to fail fast.

Goal:
- Proxy request parsing is strict and rejects malformed/unknown inputs.
- Provider response/event parsing remains forward-compatible and does not explode on unknown provider-emitted block types.

---

## 1. Contract Boundaries

### 1.1 Request bodies (strict)

All proxy endpoints that accept request JSON MUST use a strict decoding path:
- Reject unknown content block types in request bodies.
- Reject incorrect shapes for union-typed fields (`system`, message `content`, `tool.config`).
- Decode tool configs into typed config structs.

### 1.2 Provider responses and provider-emitted SSE events (lenient)

Provider parsing should remain resilient to new block/event variants:
- Unknown provider-emitted content blocks may be represented as placeholders or ignored (current behavior).
- The proxy can still reject at the boundary of *request* inputs; it should not be fragile to *provider outputs*.

---

## 2. Strict Decoding Design

### 2.1 New strict decode functions (recommended)

Do not rely on `json.Unmarshal` into `types.MessageRequest` directly for HTTP request bodies.

Add strict decode helpers in `pkg/core/types`:
- `UnmarshalContentBlockStrict(data []byte) (ContentBlock, error)`
- `UnmarshalContentBlocksStrict(data []byte) ([]ContentBlock, error)`
- `UnmarshalMessageStrict(data []byte) (Message, error)`
- `UnmarshalMessageRequestStrict(data []byte) (*MessageRequest, error)`
- `UnmarshalToolStrict(data []byte) (Tool, error)`

Rationale:
- Existing `UnmarshalContentBlock` is intentionally lenient; keep it for provider parsing.
- The proxy should opt into strictness explicitly.

### 2.2 Strict rules for `MessageRequest.System`

`system` may be:
- a JSON string, or
- a JSON array of content blocks (`[]ContentBlock`)

Strict behavior:
- If `system` is any other JSON type (object/number/bool), reject with `invalid_request_error` and `param="system"`.

Implementation approach:
- Decode `system` as `json.RawMessage` in a temporary struct, then:
  - try string unmarshal
  - else try `UnmarshalContentBlocksStrict`

### 2.3 Strict rules for `Message.Content`

`message.content` may be:
- a JSON string, or
- a JSON array of content blocks

Strict behavior:
- Reject any other shape.
- Reject unknown content block types (no placeholder blocks in requests).

Note:
- `types.Message.UnmarshalJSON` currently uses the lenient `UnmarshalContentBlocks`. Keep it for general SDK compatibility.
- The proxy strict decode path should bypass it by decoding raw messages and calling `UnmarshalContentBlocksStrict`.

### 2.4 Strict rules for `Tool.Config`

`tool.config` is tool-type specific. For proxy request bodies:
- Decode `tool.config` into the correct typed config struct based on `tool.type`.
- Store the typed pointer in `Tool.Config` so existing provider translation code sees the expected type.

Supported tool types and config decoding:
- `web_search` -> `*types.WebSearchConfig`
- `web_fetch` -> `*types.WebFetchConfig`
- `code_execution` -> `*types.CodeExecutionConfig`
- `computer_use` -> `*types.ComputerUseConfig`
- `file_search` -> `*types.FileSearchConfig`
- `text_editor` -> `*types.TextEditorConfig` (currently empty; allow `{}` or null)
- `function`:
  - `config` MUST be absent or null (reject if present) to avoid ambiguity.
  - `name`, `description`, and `input_schema` must be present as required by your request policy.

Strict behavior:
- If `tool.type` is unknown, reject with `invalid_request_error` and `param="tools[i].type"`.
- If `tool.config` exists but cannot be decoded into the correct struct, reject with a clear message.

### 2.5 Strict unknown block handling

Current behavior (`UnmarshalContentBlock`):
- Unknown `type` returns `TextBlock{Type: <unknown>, Text: "[unknown block type: ...]"}`.

Strict request behavior:
- Unknown block type is an error.

Implementation approach:
- Implement `UnmarshalContentBlockStrict` that shares the same switch but returns an error on default case.

### 2.6 Strict validation for tool history blocks (`tool_use` and `tool_result`)

Multi-turn tool flows require clients to send follow-up requests containing tool history:
- assistant emits `tool_use` blocks
- client (or gateway `/v1/runs`) responds by appending `tool_result` blocks

Strict request behavior must validate these blocks so we fail fast on malformed tool-loop state:

`tool_use` (typically in assistant messages):
- `id` must be non-empty
- `name` must be non-empty
- `input` must be a JSON object (map), not an array/string/number

`tool_result` (typically in user messages):
- `tool_use_id` must be non-empty
- `content` must be an array of content blocks, strictly decoded
- `is_error` is optional (defaults false)

Tool-use ID matching:
- Recommended proxy default: require that every `tool_result.tool_use_id` matches a preceding `tool_use.id` in the provided request history.
- If you later add advanced “history trimming” features, consider a mode that relaxes this to a warning, but keep strict as the default for correctness.

Provider-native vs gateway-managed:
- The strict decoder should still be able to decode known provider-native tool history blocks (e.g. `server_tool_use`, `web_search_tool_result`), but proxy-side provider compatibility validation may reject them for providers/models that cannot accept them.

---

## 3. Streaming Error Shape Alignment

Problem:
- Non-streaming HTTP errors use a `core.Error`-compatible envelope: `{ "error": { ... } }`.
- Streaming uses `types.ErrorEvent`, whose `Error` payload is currently only `{type,message}`.

Decision (proxy v1):
- The gateway uses **one canonical inner error object** across:
  - HTTP JSON error bodies
  - SSE terminal `error` events

Implementation:
- Expand `types.Error` in `pkg/core/types/stream.go` to match the fields used in `core.Error` (all optional except `type` and `message`):
  - `param`, `code`, `request_id`, `retry_after`, `provider_error`
- Keep non-streaming HTTP error envelope:
  - `{"error": { ... } }`
- Keep SSE terminal error event:
  - `event: error`
  - `data: {"type":"error","error":{ ...same inner error object... }}`

This keeps one canonical error model across:
- HTTP JSON error bodies
- SSE `error` terminal events

---

## 4. Voice Pre/Post Processing (If Proxy Supports Voice)

Proxy v1 decision:
- Proxy v1 **does** support `request.voice` on `/v1/messages` (see `PHASE_00_DECISIONS_AND_DEFAULTS.md`).

Implementation requirements:
- Do not copy/paste the SDK logic from `sdk/messages.go`.
- Extract shared helpers so both:
  - the Go SDK (direct-mode), and
  - the gateway proxy handlers
  can call the same voice pre/post path.

Functional requirements:
- Input voice:
  - transcribe `audio` content blocks (base64) to `text` blocks prior to provider execution
  - expose `metadata.user_transcript` in the final response
- Output voice (non-stream):
  - synthesize final assistant text into an appended `audio` block with `transcript`
- Output voice (stream):
  - emit incremental audio chunks as SSE events (recommended: `types.StreamEvent` type `audio_chunk`)
  - keep the provider’s streaming text events unchanged/passthrough

---

## 5. Test Checklist

Add unit tests for strict decoding:
- `system`:
  - string ok
  - array of blocks ok
  - object rejected
- message `content`:
  - string ok
  - array ok
  - unknown block type rejected
- tool configs:
  - `web_search` config decodes into `*types.WebSearchConfig`
  - unknown tool type rejected
  - `function` tool rejects non-null config

Add unit tests for strict tool history blocks:
- `tool_use`:
  - missing `id` rejected
  - missing `name` rejected
  - non-object `input` rejected
- `tool_result`:
  - missing `tool_use_id` rejected
  - content with unknown block type rejected
  - tool_use_id without a matching prior tool_use rejected (default strict mode)

Add proxy handler tests to prove:
- Requests that would previously “silently drop” configs/blocks are now rejected.
