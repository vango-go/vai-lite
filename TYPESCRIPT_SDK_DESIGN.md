# TypeScript SDK Design

This document defines the TypeScript SDK for `vai-lite`.

It is grounded in the current repository shape:

- The gateway already exposes `/v1/messages`, `/v1/runs`, `/v1/runs:stream`, `/v1/models`, and `/v1/live`.
- The Go SDK already establishes the product primitives that matter: `Messages.Create`, `Messages.Stream`, `Messages.Run`, `Messages.RunStream`, `Runs.Create`, `Runs.Stream`, and `Live.Connect`.
- The wire format is already canonicalized around the shared request/response/event types in `pkg/core/types`.

The goal is not to generate a thin OpenAPI client and stop there. The goal is to ship a real SDK that feels like the Go SDK, fits browser and Node runtimes, and is opinionated about streaming, tool execution, and live sessions.

## 1. Product Scope

### 1.1 v1 scope

The TypeScript SDK should be **proxy-first** and **gateway-centric** in v1.

That means:

- It talks to VAI Gateway over HTTP, SSE, and WebSocket.
- It supports:
  - `messages.create`
  - `messages.stream`
  - `messages.run`
  - `messages.runStream`
  - `messages.extract`
  - `runs.create`
  - `runs.stream`
  - `models.list`
  - `live.connect`
- It supports client-side function tools for `messages.run` and `messages.runStream`.
- It supports gateway-managed server tools for `runs.*` and `live.connect`, and should include optional helper adapters for gateway-native tools used from client-side loops.

### 1.2 Explicit non-goals for v1

The v1 TypeScript SDK should **not**:

- Call Anthropic, OpenAI, Gemini, or other providers directly.
- Ask frontend applications to place provider keys in browsers.
- Pretend SSE, client-side tool loops, and live audio are ordinary REST calls.
- Be a codegen-only package with no runtime helpers.
- Collapse live mode into `messages.runStream`.

Direct-provider TypeScript clients can be added later if there is a concrete product need. They should not distort the gateway SDK shape now.

### 1.3 Runtime targets

The core package should support:

- Node.js 20+
- Modern browsers
- Bun
- Deno
- Edge runtimes that provide `fetch`, `ReadableStream`, `TextDecoder`, and `WebSocket`

The core package should not depend on Node-only modules.

## 2. Design Summary

The SDK should expose four top-level services:

```ts
const vai = new VAI({
  baseURL: "https://api.vai.example.com",
  gatewayApiKey: "vai_sk_...",
  providerKeys: {
    anthropic: "sk-ant-...",
    tavily: "tvly-...",
  },
});

vai.messages;
vai.runs;
vai.live;
vai.models;
```

The product model is:

1. `messages.create`
   - one request, one JSON response
2. `messages.stream`
   - one request, one SSE stream, one final message
3. `messages.run`
   - SDK-owned tool loop using repeated `/v1/messages` calls
4. `messages.runStream`
   - SDK-owned streaming tool loop with deterministic history deltas and interrupts
5. `runs.create`
   - gateway-owned tool loop over `/v1/runs`
6. `runs.stream`
   - gateway-owned tool loop over `/v1/runs:stream`
7. `live.connect`
   - long-lived proxy WebSocket session for `/v1/live`

The main design principle is:

> Share the message history, content model, and callback helpers across modes, but keep the transport shapes honest.

That means:

- `messages.runStream` and `live.connect` should share helper extractors and output callbacks where it makes sense.
- Their raw event unions should remain separate.
- The canonical cross-mode history type should remain `Message[]`.

## 3. Package Layout

### 3.1 Packages

```text
packages/
  vai/
    src/
      client.ts
      messages.ts
      runs.ts
      live.ts
      models.ts
      errors.ts
      types.ts
      content.ts
      tools.ts
      history.ts
      stream.ts
      live-helpers.ts
      stream-helpers.ts
      transport/
        http.ts
        sse.ts
        websocket.ts
        headers.ts
  vai-node-audio/      optional live audio capture/playback helpers
  vai-web-audio/       optional browser live audio capture/playback helpers
```

### 3.2 Export surface

`@vango-ai/vai` should export:

- `VAI`
- `VAIError`
- `VAITransportError`
- `content` helpers
- tool helpers:
  - `tool`
  - `toolSet`
  - native tool constructors
  - optional gateway-native tool helpers
- stream/live helper extractors
- all public request/response/event types

Optional subpath exports are appropriate for schema adapters:

- `@vango-ai/vai/zod`
- `@vango-ai/vai/valibot`

Those adapters should be separate from core so the base package does not force a validation library.

## 4. Client Construction

```ts
export interface VAIOptions {
  baseURL: string;
  gatewayApiKey?: string;
  providerKeys?: Partial<Record<ProviderKeyName, string>>;
  timeoutMs?: number;
  headers?: HeadersInit | (() => HeadersInit | Promise<HeadersInit>);
  fetch?: typeof globalThis.fetch;
  webSocket?: WebSocketFactory;
  userAgent?: string;
}
```

```ts
const vai = new VAI({
  baseURL: "https://api.vai.example.com",
  gatewayApiKey: process.env.VAI_GATEWAY_API_KEY,
  providerKeys: {
    anthropic: process.env.ANTHROPIC_API_KEY,
    tavily: process.env.TAVILY_API_KEY,
  },
  timeoutMs: 60_000,
});
```

### 4.1 Required behavior

- `baseURL` is required in v1.
- `gatewayApiKey` maps to `Authorization: Bearer ...`.
- `providerKeys` map to `X-Provider-Key-*` headers using the same provider/header mapping as the Go SDK.
- The client should accept injected `fetch` and `WebSocket` implementations for runtime portability and testing.

### 4.2 Security rule

`providerKeys` are intended for trusted runtimes.

The SDK docs should explicitly say:

- Browser apps should not embed upstream provider keys.
- Browser apps should normally use gateway auth only and let the gateway own upstream credentials or issue scoped auth for the session.

The SDK should not silently normalize this into a safe pattern. The docs need to say it plainly.

## 5. Public API

```ts
class VAI {
  readonly messages: MessagesService;
  readonly runs: RunsService;
  readonly live: LiveService;
  readonly models: ModelsService;
}
```

### 5.1 `messages`

```ts
interface MessagesService {
  create(request: MessageRequest, options?: RequestOptions): Promise<MessageResponse>;
  stream(request: MessageRequest, options?: RequestOptions): Promise<MessageStream>;
  run(request: MessageRequest, options?: RunOptions): Promise<RunResult>;
  runStream(request: MessageRequest, options?: RunOptions): Promise<RunStream>;
  extract<T>(request: MessageRequest, codec: StructuredOutputCodec<T>, options?: RequestOptions): Promise<ExtractResult<T>>;
}
```

`createStream` should not exist in TypeScript. It is redundant with `stream`.

### 5.2 `runs`

```ts
interface RunsService {
  create(request: RunRequest, options?: RequestOptions): Promise<RunResultEnvelope>;
  stream(request: RunRequest, options?: RequestOptions): Promise<GatewayRunStream>;
}
```

### 5.3 `models`

```ts
interface ModelsService {
  list(options?: RequestOptions): Promise<ModelsResponse>;
}
```

`models.list()` matters for model pickers, capability gating, and BYOK UX.

### 5.4 `live`

```ts
interface LiveService {
  connect(request: LiveConnectRequest, options?: LiveConnectOptions): Promise<LiveSession>;
}
```

## 6. Core Type Model

The public types should mirror the gateway wire model closely:

- `MessageRequest`
- `MessageResponse`
- `RunRequest`
- `RunResultEnvelope`
- `RunResult`
- `Message`
- `ContentBlock`
- `Tool`
- `ToolChoice`
- `OutputFormat`
- `VoiceConfig`
- `Usage`

This is a better fit than inventing a separate TypeScript-only schema language.

### 6.1 Discriminated unions

All content blocks and stream events should be discriminated unions on `type`.

Examples:

- content blocks:
  - `text`
  - `image`
  - `audio`
  - `audio_stt`
  - `video`
  - `document`
  - `tool_use`
  - `tool_result`
  - `thinking`
  - `server_tool_use`
- stream events:
  - `message_start`
  - `content_block_start`
  - `content_block_delta`
  - `content_block_stop`
  - `message_delta`
  - `message_stop`
  - `ping`
  - `audio_chunk`
  - `audio_unavailable`
  - `error`
- run stream events:
  - `run_start`
  - `step_start`
  - `stream_event`
  - `tool_call_start`
  - `tool_result`
  - `step_complete`
  - `history_delta`
  - `run_complete`
  - `ping`
  - `error`
- live events:
  - `session_started`
  - `assistant_text_delta`
  - `audio_chunk`
  - `tool_call`
  - `user_turn_committed`
  - `input_state`
  - `turn_complete`
  - `audio_unavailable`
  - `audio_reset`
  - `turn_cancelled`
  - `error`

### 6.2 Forward compatibility

The gateway is strict on ingress and lenient on egress. The SDK must preserve that.

So the TypeScript SDK should include explicit unknown fallbacks:

- `UnknownMessageStreamEvent`
- `UnknownRunStreamEvent`
- `UnknownLiveEvent`
- `UnknownDelta`
- `UnknownContentBlock`

Rules:

- Unknown outgoing fields should not be manufactured.
- Unknown incoming event types should not crash parsing.
- Unknown incoming event types should be surfaced as opaque values with the raw payload attached.

This is mandatory for `/v1` evolution.

## 7. Content Helpers

The SDK should ship ergonomic content constructors similar to the Go SDK:

```ts
content.text("Hello");
content.imageBase64(bytes, "image/png");
content.imageURL("https://example.com/cat.png");
content.audioBase64(bytes, "audio/wav");
content.audioSTT(bytes, "audio/wav", { language: "en" });
content.documentBase64(bytes, "application/pdf", "spec.pdf");
content.toolResult(toolUseId, [content.text("done")]);
content.toolResultError(toolUseId, "request failed");
```

These helpers should accept `Uint8Array`, `ArrayBuffer`, and `Blob` where practical, then emit wire-compatible base64 blocks.

## 8. Tools

The TypeScript SDK needs the same conceptual split as the Go SDK:

1. provider-native tools
2. client-executed function tools
3. gateway-managed server tools

### 8.1 Provider-native tools

These are plain tool descriptors:

```ts
webSearch(config?)
webFetch(config?)
codeExecution(config?)
computerUse({ displayWidth, displayHeight })
textEditor()
fileSearch(config?)
```

No handler is attached because the provider executes them.

### 8.2 Client function tools

Because TypeScript cannot reflect JSON Schema from static types, the SDK should use an explicit codec boundary.

```ts
interface ToolInputCodec<T> {
  jsonSchema: JSONSchema;
  parse(input: unknown): T;
}

interface ToolDefinition<TInput = unknown, TResult = unknown> {
  tool: Tool;
  execute(ctx: ToolContext, input: TInput): Promise<TResult> | TResult;
}

function tool<TInput, TResult>(config: {
  name: string;
  description: string;
  input: ToolInputCodec<TInput>;
  execute: (ctx: ToolContext, input: TInput) => Promise<TResult> | TResult;
}): ToolDefinition<TInput, TResult>;
```

`ToolContext` should include:

- `signal`
- `requestID?`
- `stepIndex`
- `toolCallID`
- `client`
- `history`

### 8.3 Tool sets

```ts
const tools = toolSet([
  getWeather,
  lookupDocs,
]);
```

`toolSet` should expose:

- `tools`: `Tool[]`
- `handlers`: `Map<string, ToolHandler>`

### 8.4 Structured result shaping

Tool results should accept:

- strings
- plain JSON objects
- arrays
- `ContentBlock[]`

The SDK should normalize them into `tool_result` content the same way the Go SDK does.

### 8.5 Gateway-native tool helpers

The Go SDK already exposes helpers such as `VAIWebSearch`, `VAIWebFetch`, and `VAIImage` that use `/v1/server-tools:execute` from a client-side tool loop.

The TypeScript SDK should include equivalent optional helpers:

```ts
gatewayTools.webSearch({ provider: "tavily" })
gatewayTools.webFetch({ provider: "firecrawl" })
gatewayTools.image({ provider: "gem-dev" })
```

These helpers should:

- only work in gateway mode
- require the matching provider key to be configured
- execute as function tools within `messages.run` or `messages.runStream`

This preserves the same product affordance as the Go SDK.

## 9. `messages.create` and `messages.stream`

### 9.1 `messages.create`

```ts
const response = await vai.messages.create({
  model: "anthropic/claude-sonnet-4",
  max_tokens: 512,
  messages: [{ role: "user", content: "Hello" }],
});
```

The response should be the decoded `MessageResponse` plus small SDK helpers:

```ts
response.text();
response.toolUses();
```

Those helpers can live either on a wrapper class or as standalone utilities. A small wrapper class is preferable because it matches the Go SDK ergonomics.

### 9.2 `messages.stream`

```ts
const stream = await vai.messages.stream({
  model: "anthropic/claude-sonnet-4",
  stream: true,
  messages: [{ role: "user", content: "Stream please" }],
});

for await (const event of stream) {
  if (event.type === "content_block_delta" && event.delta.type === "text_delta") {
    process.stdout.write(event.delta.text);
  }
}

const final = await stream.finalResponse();
```

`MessageStream` should be:

```ts
interface MessageStream extends AsyncIterable<MessageStreamEvent> {
  finalResponse(): Promise<MessageResponse>;
  text(): Promise<string>;
  close(): Promise<void>;
}
```

Design rules:

- The SDK should parse SSE itself instead of depending on `EventSource`.
- `finalResponse()` should reconstruct the terminal message from the streamed events.
- Gateway `audio_chunk` events should be surfaced as part of the same stream.

## 10. `messages.run`

`messages.run` is a client-owned tool loop built on repeated `/v1/messages` calls.

This is essential because:

- it mirrors the Go SDK's primary agent primitive
- it enables tool execution in the caller runtime
- it works in browser, Node, and edge environments

```ts
const getWeather = tool({
  name: "get_weather",
  description: "Get the weather for a city",
  input: weatherCodec,
  execute: async (_ctx, input) => {
    return { forecast: `Sunny in ${input.city}` };
  },
});

const result = await vai.messages.run({
  model: "anthropic/claude-sonnet-4",
  messages: [{ role: "user", content: "What is the weather in Denver?" }],
  tools: [getWeather.tool],
}, {
  tools: [getWeather],
  maxTurns: 8,
  maxToolCalls: 20,
});
```

`RunOptions` should include:

- `tools?: ToolDefinition[]`
- `toolHandlers?: Record<string, ToolHandler>`
- `maxTurns?: number`
- `maxToolCalls?: number`
- `maxTokens?: number`
- `timeoutMs?: number`
- `parallelTools?: boolean`
- `toolTimeoutMs?: number`
- `stopWhen?: (response: MessageResponse) => boolean`
- `onToolCall?`
- `onStepComplete?`
- `buildTurnMessages?`

The result shape should mirror the Go SDK:

```ts
interface RunResult {
  response?: MessageResponse;
  steps: RunStep[];
  tool_call_count: number;
  turn_count: number;
  usage: Usage;
  stop_reason: RunStopReason;
  messages?: Message[];
}
```

## 11. `messages.runStream`

`messages.runStream` should be the TypeScript equivalent of the Go SDK's main streaming loop.

```ts
const stream = await vai.messages.runStream(request, {
  tools: [getWeather],
  maxTurns: 8,
});

for await (const event of stream) {
  switch (event.type) {
    case "stream_event":
      break;
    case "tool_call_start":
      break;
    case "tool_result":
      break;
    case "history_delta":
      break;
    case "run_complete":
      break;
  }
}
```

`RunStream` should be:

```ts
interface RunStream extends AsyncIterable<RunStreamEvent> {
  result(): Promise<RunResult>;
  cancel(): Promise<void>;
  interrupt(message: Message, options?: { behavior?: InterruptBehavior }): Promise<void>;
  interruptWithText(text: string): Promise<void>;
  close(): Promise<void>;
}
```

`InterruptBehavior` should match the Go SDK semantics:

- `"discard"`
- `"save_partial"`
- `"save_marked"`

The run stream event union should include:

- `step_start`
- `stream_event`
- `tool_call_start`
- `tool_result`
- `step_complete`
- `history_delta`
- `audio_chunk`
- `audio_unavailable`
- `interrupted`
- `run_complete`

### 11.1 Deterministic history

The TypeScript SDK should emit `history_delta` exactly because it is the cleanest synchronization seam between:

- client text UIs
- persisted chat state
- live session handoff
- interrupt/resume flows

The SDK should also export a small helper:

```ts
applyHistoryDelta(history, event);
```

## 12. `messages.extract`

Structured output should use a codec abstraction instead of hardwiring Zod into core.

```ts
interface StructuredOutputCodec<T> {
  jsonSchema: JSONSchema;
  parse(text: string): T;
}

const result = await vai.messages.extract(request, recipeCodec);
result.value;
result.response;
```

This mirrors the Go SDK's `Extract`, but uses an explicit parser instead of reflection.

## 13. `runs.create` and `runs.stream`

These are gateway-owned loops and should stay thin.

### 13.1 `runs.create`

```ts
const result = await vai.runs.create({
  request: {
    model: "anthropic/claude-sonnet-4",
    max_tokens: 1024,
    messages: [{ role: "user", content: "Find the latest Go release." }],
  },
  run: {
    max_turns: 8,
    max_tool_calls: 20,
    parallel_tools: true,
  },
  server_tools: ["vai_web_search"],
  server_tool_config: {
    vai_web_search: { provider: "tavily" },
  },
});
```

### 13.2 `runs.stream`

```ts
const stream = await vai.runs.stream(runRequest);

for await (const event of stream) {
  if (event.type === "stream_event") {
    // Wrapped /v1/messages SSE event.
  }
}

const result = await stream.result();
```

`GatewayRunStream` should mirror `RunStream` where practical:

```ts
interface GatewayRunStream extends AsyncIterable<GatewayRunEvent> {
  result(): Promise<RunResult>;
  close(): Promise<void>;
}
```

The gateway stream should also expose the same high-level callback helper API used by `messages.runStream`.

## 14. `live.connect`

Live mode should remain a separate session transport.

```ts
const session = await vai.live.connect({
  request: {
    model: "anthropic/claude-sonnet-4",
    messages: history,
    voice: {
      output: {
        voice: "helpful",
        format: "pcm_s16le",
        sample_rate_hz: 24000,
      },
    },
  },
  run: { max_turns: 8 },
  server_tools: ["vai_web_search"],
}, {
  tools: [clientTool],
});
```

The request should mirror the Go SDK's `LiveConnectRequest`:

```ts
interface LiveConnectRequest {
  request: MessageRequest;
  run?: RunConfig;
  server_tools?: string[];
  server_tool_config?: Record<string, unknown>;
  builtins?: string[];
}
```

`LiveConnectOptions` should be local-only:

```ts
interface LiveConnectOptions {
  tools?: ToolDefinition[];
  toolHandlers?: Record<string, ToolHandler>;
}
```

### 14.1 `LiveSession`

```ts
interface LiveSession {
  events(): AsyncIterable<LiveEvent>;
  sendFrame(frame: LiveClientFrame): Promise<void>;
  sendAudio(pcm: Uint8Array): Promise<void>;
  appendInputBlocks(blocks: ContentBlock[]): Promise<void>;
  commitInputBlocks(blocks?: ContentBlock[]): Promise<void>;
  clearInput(): Promise<void>;
  sendToolResult(executionId: string, content: ContentBlock[], options?: { isError?: boolean; error?: unknown }): Promise<void>;
  reportPlaybackMark(turnId: string, playedMs: number): Promise<void>;
  reportPlaybackState(turnId: string, state: "playing" | "finished" | "stopped", playedMs?: number): Promise<void>;
  history(): Message[];
  close(): Promise<void>;
}
```

### 14.2 Shared helper layer

The SDK should ship live helpers analogous to the Go SDK:

- turn tracker for superseded-turn suppression
- playback reporter
- event extractor helpers

These helpers matter because live audio UIs are stateful and easy to get wrong.

## 15. Error Model

The error model should separate:

1. canonical API errors returned by the gateway
2. transport/protocol/runtime failures inside the SDK

### 15.1 API error

```ts
class VAIError extends Error {
  readonly type: string;
  readonly message: string;
  readonly param?: string;
  readonly code?: string;
  readonly requestId?: string;
  readonly retryAfter?: number;
  readonly providerError?: unknown;
  readonly compatIssues?: CompatibilityIssue[];
  readonly httpStatus: number;
}
```

### 15.2 Transport error

```ts
class VAITransportError extends Error {
  readonly operation: "GET" | "POST" | "WS_CONNECT" | "WS_SEND" | "WS_RECV";
  readonly url: string;
  readonly cause: unknown;
}
```

### 15.3 Parsing/protocol error

A third internal/public error class is appropriate for malformed SSE or invalid live frames:

```ts
class VAIProtocolError extends Error {
  readonly phase: "sse_decode" | "ws_decode" | "response_decode";
  readonly payload?: string;
}
```

### 15.4 Retry policy

Default retry policy should be conservative:

- no automatic retries for `messages.*`, `runs.*`, or `live.connect`
- optional safe retries for `models.list`

Anything that can duplicate model output or duplicate side-effecting tool execution should remain opt-in.

## 16. Transport Design

### 16.1 HTTP

Use `fetch`.

The SDK should:

- honor caller `AbortSignal`
- apply default timeouts only when the caller did not provide one
- merge static and dynamic headers
- centralize header construction for gateway auth and BYOK provider headers

### 16.2 SSE

Do not use `EventSource`.

Reasons:

- POST streaming is required for `/v1/messages` and `/v1/runs:stream`
- custom headers are required
- body payloads are required
- parity with Node/browser requires a custom parser anyway

The SDK should parse `ReadableStream<Uint8Array>` frames directly.

### 16.3 WebSocket

Use runtime `WebSocket` with an injectable factory.

The live client should:

- convert `http` to `ws` and `https` to `wss`
- send the `start` frame immediately after connect
- handle JSON text frames and binary audio frames separately
- support auto tool execution for live `tool_call` events

## 17. Packaging and Build

The package should be:

- ESM-first
- fully typed
- tree-shakeable
- free of Node polyfills in core

Suggested output:

- ESM build
- type declarations

CommonJS output is optional. It should only be added if there is a real compatibility requirement.

## 18. Testing and Conformance

The implementation should have four test layers.

### 18.1 Pure unit tests

- header construction
- SSE parser
- event decoding
- message reconstruction from stream events
- run loop tool handling
- live frame encode/decode

### 18.2 Contract tests against the repo schemas

- validate example payloads from `api/openapi.yaml`
- validate unknown-event fallbacks
- ensure event unions stay aligned with `pkg/core/types`

### 18.3 Mock transport tests

- POST `/v1/messages` JSON
- POST `/v1/messages` SSE
- POST `/v1/runs`
- POST `/v1/runs:stream`
- `GET /v1/models`
- `/v1/live` WebSocket session startup and tool handling

### 18.4 End-to-end tests against the local gateway

- `messages.create`
- `messages.stream`
- `messages.run` with local tool
- `runs.stream` with `vai_web_search`
- `live.connect` startup and event processing

## 19. Implementation Order

Recommended execution order:

1. `types`, `errors`, `content`, `history`
2. HTTP transport and header builder
3. SSE parser and `messages.stream`
4. `messages.create`
5. `models.list`
6. `runs.create` and `runs.stream`
7. tool codec boundary and `messages.run`
8. `messages.runStream`
9. `live.connect`
10. optional gateway-native tool helpers
11. optional Node/browser audio helper packages

## 20. Key Decisions Locked By This Design

These decisions should be treated as intentional, not provisional:

1. The TypeScript SDK is proxy-first in v1.
2. `messages.run` and `messages.runStream` are first-class APIs, not omitted in favor of `runs.*`.
3. Live mode is a separate service, not a flag on `runStream`.
4. Core types stay close to the gateway wire model.
5. Unknown events are preserved instead of rejected.
6. Core does not depend on Zod or another schema library.
7. SSE is implemented with `fetch` streaming, not `EventSource`.
8. The SDK includes ergonomic helpers, not just raw endpoint wrappers.
