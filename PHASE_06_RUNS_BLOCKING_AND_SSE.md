# Phase 06 — `/v1/runs` + `/v1/runs:stream` (Server-Side Tool Loop) (Implementation Plan)

This phase adds the hosted-gateway “thin SDK unlock”: server-side tool-loop execution matching the Go SDK `Run`/`RunStream` semantics closely enough that non-Go SDKs don’t need to re-implement the run state machine.

Primary references:
- `RUNS_API_AND_EVENT_SCHEMA.md` (v1 contract)
- `DEVELOPER_GUIDE.md` (source-of-truth semantics for Run/RunStream)
- `API_CONTRACT_HARDENING.md` (strict decode rules; must apply to run request bodies too)
- `PROXY_MODE_IMPLEMENTATION_PLAN.md` (why runs are prerequisite for Live)

---

## 0. Exit Criteria (Definition of Done)

Phase 03 is complete when:

1. Endpoints exist:
   - `POST /v1/runs` (blocking JSON)
   - `POST /v1/runs:stream` (SSE)

2. Request validation matches contract:
   - request embeds a `types.MessageRequest` (strict decoded)
   - `builtins` are allowlisted only
   - non-allowlisted `function` tools in `request.tools` are rejected (v1 safety posture)

3. Tool execution is safe:
   - v1 executes gateway-managed builtins only (start with `vai_web_search`)
   - provider-native tools remain provider-executed

4. Streaming schema matches `RUNS_API_AND_EVENT_SCHEMA.md`:
   - run lifecycle events
   - wrapped provider `types.StreamEvent` objects (`type:"stream_event"`)
   - tool call + tool result events for gateway-managed tools

5. Tests prove correctness:
   - fake provider emits tool calls; gateway executes builtin; history is appended deterministically
   - `/v1/runs:stream` event ordering matches schema

---

## 1. API Shapes (v1)

### 1.1 `POST /v1/runs` request

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

### 1.2 `POST /v1/runs` response

Must match `RUNS_API_AND_EVENT_SCHEMA.md` and include:
- final `types.MessageResponse`
- stop_reason (mirrors Go SDK’s `RunStopReason`)
- aggregated usage and bounded step trace (at least for debugging)

### 1.3 `POST /v1/runs:stream` response

SSE where:
- `event: <type>`
- `data: {"type":"<type>", ... }`

---

## 2. Implementation Approach

### 2.1 Packages

Add:
- `pkg/gateway/handlers/runs.go`
- `pkg/gateway/runloop/*` (server-side run controller)
- `pkg/gateway/tools/builtins/*` (gateway-managed builtin implementations)

### 2.2 Strict request decoding

Implement a strict decoder for the runs request body in the gateway handler:
- decode top-level fields strictly
- decode `request` via `types.UnmarshalMessageRequestStrict`

### 2.3 Builtin tool injection

Per `RUNS_API_AND_EVENT_SCHEMA.md`:
- caller lists enabled builtins by name (`builtins`)
- server injects canonical tool definitions into the model request
- server rejects client-supplied definitions for builtins (do not trust caller schema/description)

### 2.4 Run loop semantics

Reuse the Go SDK semantics, but do not import `sdk/` into gateway code.

Minimal v1 behaviors:
- deterministic message history append:
  - append assistant message content for each turn
  - append `tool_result` user message after executing gateway-managed tools
- stop conditions:
  - max_turns, max_tool_calls, max_tokens (optional), timeout
- tool execution:
  - allowlist only (gateway-managed builtins)
  - tool timeout per call
  - parallel execution optional (default true per schema)

### 2.5 SSE event model

Define server-owned event structs in `pkg/gateway/handlers` or `pkg/gateway/runloop` matching the schema:
- `run_start`, `step_start`, `stream_event`, `tool_call_start`, `tool_result`, `step_end`, `run_end`, `error`

`stream_event` wraps provider-emitted `types.StreamEvent`:

```json
{"type":"stream_event","event":{ /* types.StreamEvent */ }}
```

---

## 3. Test Plan

### 3.1 Unit tests

- strict decode for runs request top-level
- builtin allowlist enforcement
- tool execution timeout mapping into tool_result is_error

### 3.2 Handler tests (required)

Build:
- `fakeProvider` that:
  - returns a tool call (`tool_use`) in response to a prompt
  - then returns a final answer after receiving `tool_result`
- `fakeBuiltinWebSearch` that returns deterministic text blocks

Tests:
- `/v1/runs` returns final response with correct stop_reason and step trace
- `/v1/runs:stream` emits correct event ordering and includes wrapped provider stream events

---

## 4. Deferred (Explicitly Not In This Phase)

- Registered HTTP tools (webhooks) for tenant-provided tool execution
- Storing run traces durably (Postgres)
- Live WS (`/v1/live`) (next phase depends on these endpoints existing)
