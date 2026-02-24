# VAI Gateway `/v1/runs` API and Event Schema (v1)

This document defines the hosted-gateway API for server-side tool-loop execution, so non-Go SDKs can remain thin clients.

Related docs:
- `PROXY_MODE_IMPLEMENTATION_PLAN.md` (milestones and scope)
- `API_CONTRACT_HARDENING.md` (strict request decoding and validation)
- `DEVELOPER_GUIDE.md` (reference semantics for `Run` / `RunStream`)
- `VAI_SDK_STRATEGY.md` (thin SDK approach)

---

## 1. Goals

1. Provide server-side equivalents of Go `Messages.Run()` and `Messages.RunStream()`:
   - blocking endpoint (`/v1/runs`)
   - streaming endpoint (`/v1/runs:stream` via SSE)
2. Reuse the same core types for message content:
   - requests embed `pkg/core/types.MessageRequest`
   - responses embed `pkg/core/types.MessageResponse`
3. Make non-Go SDKs thin:
   - they do not re-implement tool-loop state machines, history deltas, or streaming edge cases
4. Keep tool execution safe:
   - v1 supports **gateway-managed tools only** (no arbitrary customer code execution)

---

## 2. Tool Execution Contract (v1)

### 2.1 Supported tool categories

1. Provider-native tools:
   - `tool.type` is a native tool (e.g. `web_search`, `code_execution`)
   - The provider executes it; gateway does not run any local handler.

2. Gateway-managed function tools (allowlisted builtins):
   - `tool.type: "function"`
   - The gateway executes a small allowlisted set of builtins.
   - Initial builtin target:
     - `vai_web_search` (VAI-native web search)
   - Optional (if needed soon):
     - `vai_web_fetch`

3. Client-executed function tools:
   - Not supported in `/v1/runs` v1. If the request references function tools outside the allowlist, reject with a clear error directing callers to:
     - use `POST /v1/messages` and execute tools in application code, or
     - wait for future “registered HTTP tools”.

### 2.2 Builtins are referenced, not defined

The gateway should not trust caller-supplied schemas/descriptions for builtin tools. Instead:
- the request declares which builtins are enabled (see `run.builtins`)
- the gateway injects the canonical tool definition(s) into the model request

This keeps builtin behavior stable and prevents accidental schema weakening.

---

## 3. Endpoints

### 3.1 `POST /v1/runs`

Blocking server-side tool loop. Returns a final result object.

Request (JSON):
```json
{
  "request": { /* types.MessageRequest */ },
  "run": {
    "max_turns": 8,
    "max_tool_calls": 20,
    "max_tokens": 0,
    "timeout_ms": 60000,
    "parallel_tools": true,
    "tool_timeout_ms": 30000
  },
  "builtins": ["vai_web_search"]
}
```

Notes:
- `request.stream` must be false/omitted for `/v1/runs` (the server controls streaming internally).
- The gateway uses `request.model` to route to the provider.
- The gateway may augment `request.tools` by injecting builtin tool definitions (based on `builtins`) and by filtering out unsupported tool types.

Response (JSON):
```json
{
  "result": {
    "response": { /* types.MessageResponse */ },
    "steps": [
      {
        "index": 0,
        "response": { /* types.MessageResponse */ },
        "tool_calls": [
          {"id":"call_1","name":"vai_web_search","input":{"query":"..."}}
        ],
        "tool_results": [
          {"tool_use_id":"call_1","content":[{"type":"text","text":"..."}],"error":null}
        ],
        "duration_ms": 1234
      }
    ],
    "tool_call_count": 1,
    "turn_count": 2,
    "usage": {"input_tokens":0,"output_tokens":0,"total_tokens":0},
    "stop_reason": "end_turn",
    "messages": [ /* optional final history */ ]
  }
}
```

`stop_reason` mirrors Go SDK `RunStopReason` semantics:
- `end_turn`, `max_tool_calls`, `max_turns`, `max_tokens`, `timeout`, `cancelled`, `error`, `custom` (if you later add server-side custom stop conditions)

### 3.2 `POST /v1/runs:stream` (SSE)

Streaming server-side tool loop. Emits run lifecycle events and nested provider stream events.

Response:
- `200 text/event-stream; charset=utf-8`
- Each SSE event:
  - `event:` equals the run event `type`
  - `data:` is the JSON encoding of the event object

---

## 4. Run Event Schema (`/v1/runs:stream`)

### 4.1 Event framing

SSE event name:
- `event: <type>`

JSON payload:
- must include `"type": "<type>"`

Example:
```
event: step_start
data: {"type":"step_start","index":0}

```

### 4.2 Event types

#### `run_start`
```json
{
  "type": "run_start",
  "request_id": "req_...",
  "model": "anthropic/claude-sonnet-4"
}
```

#### `step_start`
```json
{"type":"step_start","index":0}
```

#### `stream_event` (wrapped provider stream events)

Wrap `pkg/core/types.StreamEvent` objects so clients can reuse existing SSE parsing logic for message streaming.

```json
{
  "type": "stream_event",
  "event": { /* types.StreamEvent */ }
}
```

#### `tool_call_start`
Emitted when the gateway begins executing a function tool handler (gateway-managed builtin).

```json
{
  "type":"tool_call_start",
  "id":"call_1",
  "name":"vai_web_search",
  "input": {"query":"..."}
}
```

#### `tool_result`
Emitted after tool execution finishes.

```json
{
  "type":"tool_result",
  "id":"call_1",
  "name":"vai_web_search",
  "content":[{"type":"text","text":"..."}],
  "is_error": false,
  "error": null
}
```

If a tool fails:
- `is_error=true`
- `error` is the canonical error inner object (same fields as HTTP error inner object).

#### `step_complete`
```json
{
  "type":"step_complete",
  "index":0,
  "response": { /* types.MessageResponse */ }
}
```

#### `history_delta`
Emitted after `step_complete`. Mirrors Go SDK history delta semantics.

```json
{
  "type":"history_delta",
  "expected_len": 4,
  "append": [ /* []types.Message */ ]
}
```

#### `run_complete`
Terminal event for successful completion (including stop reasons like max turns/tool calls).

```json
{
  "type":"run_complete",
  "result": { /* run result object, same shape as /v1/runs response.result */ }
}
```

#### `error`
Terminal event for failures.

```json
{
  "type":"error",
  "error": { /* canonical error inner object */ }
}
```

#### `ping`
Optional keepalive.

```json
{"type":"ping"}
```

---

## 5. Request Validation Requirements

`/v1/runs` and `/v1/runs:stream` MUST apply strict request decoding and validation (per `API_CONTRACT_HARDENING.md`):
- strict decode `request.system`
- strict decode message `content`
- strict decode `request.tools[].config` into typed structs
- strict tool history validation (`tool_use` / `tool_result`) if present
- reject unknown request content blocks (do not use placeholder blocks)

Additional `/v1/runs`-specific validation:
- `builtins` may only contain allowlisted builtin names
- reject non-allowlisted `function` tools in `request.tools` (unless you explicitly choose to ignore and overwrite them)

---

## 6. Auth and Headers

Same as `/v1/messages`:
- Gateway auth (hosted): `Authorization: Bearer vai_sk_...` (configurable in self-host)
- BYOK upstream headers (required for the selected provider):
  - `X-Provider-Key-Anthropic`, `X-Provider-Key-OpenAI`, etc.

The gateway MUST redact BYOK headers in logs.

---

## 7. Compatibility Notes

The server-side run loop will only be as capable as the chosen provider/model + enabled tools:
- if a provider/model does not support tools, `/v1/runs` can still work in the “no tool calls” case, but you should reject `builtins` and `function` tools.
- if you require web search for the workload, prefer `vai_web_search` builtin and keep provider-native web search optional.

---

## 8. Versioning

This schema is v1. Changes should be:
- additive whenever possible
- version-gated when breaking changes are needed (e.g. new endpoint version prefix or explicit `protocol_version` field in requests)

