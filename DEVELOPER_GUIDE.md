# Developer Guide (vai-lite)

`vai-lite` is a trimmed-down, **direct-mode only** Go SDK for running tool-using LLM agents.

Most examples use `panic(err)` for brevity. In production code, handle errors explicitly and consider retries/backoff for transient provider failures.

The only “high-level” primitives are:

- `Messages.Run()` — blocking tool-loop execution
- `Messages.RunStream()` — streaming tool-loop execution with interrupts/cancel and deterministic history deltas

Everything else is in service of those primitives:

- Multi-provider routing via model strings like `anthropic/claude-sonnet-4`
- A canonical request/response format based on **Anthropic Messages API** (messages + typed content blocks)
- A tools system with:
  - “native tools” normalization (e.g. `web_search`, `code_execution`) that providers execute
  - “function tools” that **your process executes** (via `MakeTool`, `FuncAsTool`, `ToolSet`, handlers)

This repo intentionally **does not** include:

- Proxy/HTTP server mode
- Live/WebSocket mode
- Voice/STT/TTS pipeline orchestration
- Extra endpoints (`/v1/messages`, `/v1/audio`, `/v1/models`, etc.)

---

## Table of Contents

- [1. Repository Layout](#1-repository-layout)
- [2. Installation and Requirements](#2-installation-and-requirements)
- [3. Provider Authentication](#3-provider-authentication)
- [4. Mental Model: Request → Provider → Tool Loop](#4-mental-model-request--provider--tool-loop)
- [5. Core Types](#5-core-types)
  - [5.1 Model strings](#51-model-strings)
  - [5.2 Messages + content blocks](#52-messages--content-blocks)
  - [5.3 Tools and tool choice](#53-tools-and-tool-choice)
  - [5.4 OutputFormat (structured output)](#54-outputformat-structured-output)
- [6. The Tool Loop](#6-the-tool-loop)
  - [6.1 Run](#61-run)
  - [6.2 RunStream](#62-runstream)
  - [6.3 Stop conditions](#63-stop-conditions)
  - [6.4 Tool execution details](#64-tool-execution-details)
  - [6.5 Timeouts, cancellation, and interrupts](#65-timeouts-cancellation-and-interrupts)
  - [6.6 Deterministic history](#66-deterministic-history)
- [7. Defining Tools (Function Tools)](#7-defining-tools-function-tools)
  - [7.1 `MakeTool` (recommended)](#71-maketool-recommended)
  - [7.2 `FuncAsTool` (returns tool + handler)](#72-funcastool-returns-tool--handler)
  - [7.3 `ToolSet` (group tools + handlers)](#73-toolset-group-tools--handlers)
  - [7.4 Manual `WithToolHandler`](#74-manual-withtoolhandler)
  - [7.5 Output shaping: returning content blocks](#75-output-shaping-returning-content-blocks)
- [8. Native Tools (Provider-Executed)](#8-native-tools-provider-executed)
- [9. Streaming Events (RunStream)](#9-streaming-events-runstream)
  - [9.1 Event model](#91-event-model)
  - [9.2 Helper extractors](#92-helper-extractors)
  - [9.3 `RunStream.Process` convenience](#93-runstreamprocess-convenience)
- [10. Errors and Observability](#10-errors-and-observability)
- [11. Testing and Local Dev](#11-testing-and-local-dev)
- [12. Gotchas and Design Notes](#12-gotchas-and-design-notes)

---

## 1. Repository Layout

Key directories you’ll touch:

- `sdk/` — The public SDK you import (`github.com/vango-go/vai-lite/sdk`)
  - `sdk/client.go` — direct-mode client + provider registration
  - `sdk/messages.go` — `Run` / `RunStream`
  - `sdk/run.go` — the tool loop implementation
  - `sdk/tools.go` — tool builders (`MakeTool`, `ToolSet`, native tool constructors)
  - `sdk/stream.go` — stream wrapper that accumulates final responses
  - `sdk/stream_helpers.go` — helper callbacks + extractor utilities for RunStream events
  - `sdk/content.go` — content-block constructors (text/image/video/document/tool_result)
  - `sdk/schema.go` — schema generation helpers for function tools / structured output
- `pkg/core/` — shared core types + providers (SDK uses these directly)
  - `pkg/core/engine.go` — provider registry + `provider/model` routing
  - `pkg/core/types/*` — canonical API types (requests, responses, blocks, tools, streaming events)
  - `pkg/core/providers/*` — provider implementations (anthropic/openai/gemini/groq/etc.)

---

## 2. Installation and Requirements

### Go version

`go.mod` currently targets `go 1.24.7`.

### Add to your project

```bash
go get github.com/vango-go/vai-lite/sdk
```

### Import path

```go
import vai "github.com/vango-go/vai-lite/sdk"
```

---

## 3. Provider Authentication

`vai-lite` is direct-mode only. It calls providers directly, so **your process** must have the provider keys available.

### Environment variables

The core engine loads keys from environment variables in the form:

- `ANTHROPIC_API_KEY`
- `OPENAI_API_KEY`
- `GROQ_API_KEY`
- `CEREBRAS_API_KEY`
- `GEMINI_API_KEY` (also accepts `GOOGLE_API_KEY` as a fallback)

Example:

```bash
export ANTHROPIC_API_KEY="sk-ant-..."
export OPENAI_API_KEY="sk-..."
```

### Overriding keys programmatically

You can override environment keys by passing `WithProviderKey`:

```go
client := vai.NewClient(
	vai.WithProviderKey("anthropic", "sk-ant-..."),
)
```

### Gemini OAuth (optional)

The SDK will attempt to initialize the `gemini_oauth` provider if credentials exist at:

- `~/.config/vango/gemini-oauth-credentials.json`

Optional env var:

- `GEMINI_OAUTH_PROJECT_ID`

If OAuth credentials aren’t present or initialization fails, the SDK just logs a debug message and continues.

---

## 4. Mental Model: Request → Provider → Tool Loop

At a high level:

1. You create a `Client`.
2. You build a `MessageRequest` (model + messages + tools + options).
3. You run an agent loop using:
   - `Messages.Run()` (blocking) or
   - `Messages.RunStream()` (streaming)
4. The model responds with:
   - text/thinking, and possibly
   - tool calls (`tool_use` blocks)
5. `vai-lite` executes tool calls *that have registered handlers* (client-side “function tools”).
6. Tool results are injected back into the conversation as `tool_result` blocks and the loop continues until a stop condition is met.

Native tools (like `web_search`) are **executed by the provider**. You still see tool-ish blocks/events, but there is no local handler to register.

---

## 5. Core Types

Most public types are re-exported aliases around `pkg/core/types`.

### 5.1 Model strings

All model IDs are of the form:

```
provider/model-name
```

Examples:

- `anthropic/claude-sonnet-4`
- `openai/gpt-4o`
- `oai-resp/gpt-4o` (OpenAI Responses API provider in this repo; name is provider-specific)
- `groq/llama-3.3-70b`
- `gemini/gemini-2.0-flash`
- `gemini-oauth/gemini-2.0-flash`
- `cerebras/llama-3.1-8b`

Routing happens in `pkg/core/engine.go` by splitting on the first `/`.

### 5.2 Messages + content blocks

The canonical request format is Anthropic-style:

```go
req := &vai.MessageRequest{
	Model: "anthropic/claude-sonnet-4",
	Messages: []vai.Message{
		{
			Role: "user",
			Content: []vai.ContentBlock{
				vai.Text("What's in this image?"),
				vai.ImageURL("https://example.com/cat.jpg"),
			},
		},
	},
}
```

Notes:

- `Message.Content` can be:
  - a `string`, or
  - a `[]ContentBlock`
- `vai.Text(...)` returns a `ContentBlock` (a typed block).
- `vai-lite` does **not** run a voice pipeline. If you include raw audio blocks (via core types), they are just passed through to providers that support audio input.

### 5.3 Tools and tool choice

Tools live on `MessageRequest.Tools`:

```go
req.Tools = []vai.Tool{
	vai.WebSearch(),
	vai.CodeExecution(),
}
```

For function tools created via `MakeTool(...)`, the recommended pattern is to enable them via `WithTools(tool)`, which:

- registers the handler, and
- automatically attaches the tool definition for that run/stream.

```go
	tool := vai.MakeTool("get_weather", "Get weather for a location", func(ctx context.Context, in struct {
		Location string `json:"location"`
	}) (string, error) {
		return "72F and sunny in " + in.Location, nil
	})

	result, err := client.Messages.Run(ctx, &vai.MessageRequest{
		Model: "openai/gpt-4o",
		Messages: []vai.Message{
			{Role: "user", Content: vai.Text("What's the weather in Austin?")},
		},
		Tools: []vai.Tool{
			vai.WebSearch(),
			vai.CodeExecution(),
		},
	}, vai.WithTools(tool))
	if err != nil {
		panic(err)
	}

fmt.Println(result.Response.TextContent())
```

Tool selection policy:

```go
req.ToolChoice = vai.ToolChoiceAuto() // model decides
// req.ToolChoice = vai.ToolChoiceAny()
// req.ToolChoice = vai.ToolChoiceNone()
// req.ToolChoice = vai.ToolChoiceTool("some_tool_name")
```

### 5.4 OutputFormat (structured output)

You can request JSON schema structured output by setting:

```go
schema := vai.SchemaFromStruct[struct {
	Person  string `json:"person" desc:"Person name"`
	Role    string `json:"role"`
	Company string `json:"company"`
}]()

req.OutputFormat = &vai.OutputFormat{
	Type:       "json_schema",
	JSONSchema: schema,
}
```

Important:

- `vai-lite` does not include a dedicated `Extract()` helper; you parse the response text yourself.
- Structured-output behavior is provider/model-specific.

---

## 6. The Tool Loop

### 6.1 Run

`Run` is a blocking loop that keeps calling the model and executing tools until it reaches a terminal condition.

```go
client := vai.NewClient()

tool := vai.MakeTool("increment", "Increment a counter",
	func(ctx context.Context, in struct{}) (string, error) {
		return "ok", nil
	},
)

result, err := client.Messages.Run(ctx, &vai.MessageRequest{
	Model: "anthropic/claude-sonnet-4",
	Messages: []vai.Message{
		{Role: "user", Content: vai.Text("Call increment once and then summarize.")},
	},
}, vai.WithTools(tool), vai.WithMaxToolCalls(5))
if err != nil {
	// err can be provider errors, context cancellation, etc.
	panic(err)
}

fmt.Println(result.Response.TextContent())
fmt.Println("Tool calls:", result.ToolCallCount)
fmt.Println("Turns:", result.TurnCount)
```

What you get back (`RunResult`):

- `Response`: final assistant response (the terminal response)
- `Steps`: per-turn steps (responses + tool calls + tool results)
- `ToolCallCount`: total tool calls across the loop
- `TurnCount`: total model turns
- `Usage`: aggregated usage across turns (provider-reported)
- `StopReason`: why the loop ended
- `Messages` (optional): snapshot of final message history

### 6.2 RunStream

`RunStream` runs the same loop but emits events while it happens.

```go
stream, err := client.Messages.RunStream(ctx, req,
	vai.WithTools(tool),
	vai.WithMaxToolCalls(5),
)
if err != nil {
	panic(err)
}
defer stream.Close()

for event := range stream.Events() {
	if text, ok := vai.TextDeltaFrom(event); ok {
		fmt.Print(text)
	}
}

if err := stream.Err(); err != nil {
	panic(err)
}

final := stream.Result()
fmt.Println("\nStopReason:", final.StopReason)
```

### 6.3 Stop conditions

`RunOption`s:

- Safety limits:
  - `WithMaxToolCalls(n)`
  - `WithMaxTurns(n)`
  - `WithMaxTokensRun(n)` (uses aggregated `Usage.TotalTokens`)
  - `WithRunTimeout(d)`
- Tool execution:
  - `WithParallelTools(true|false)` (default: `true`)
  - `WithToolTimeout(d)` (default: `30s`)
- Custom stop:
  - `WithStopWhen(func(*Response) bool { ... })`

Examples:

```go
result, err := client.Messages.Run(ctx, req,
	vai.WithMaxTurns(8),
	vai.WithMaxToolCalls(20),
	vai.WithRunTimeout(60*time.Second),
	vai.WithStopWhen(func(r *vai.Response) bool {
		return strings.Contains(r.TextContent(), "DONE")
	}),
)
```

### 6.4 Tool execution details

When the model requests tools (via `tool_use` blocks), the SDK:

1. Looks up a registered handler by tool name.
2. Reconstructs streamed tool inputs (`input_json_delta`) into the final tool `input` object.
3. Marshals the tool `input` object to JSON.
4. Calls your handler with `context.Context` + `json.RawMessage`.
5. Converts the returned value into `[]ContentBlock`:
   - `string` → `[{type:"text", text:"..."}]`
   - `ContentBlock` → wrapped into a slice
   - `[]ContentBlock` → used as-is
   - other types → JSON-marshaled into a `text` block
6. Injects a `tool_result` block back into the conversation.

For `RunStream` specifically, `ToolCallStartEvent` is emitted when local SDK tool execution begins (once per tool call) and includes the complete parsed input.

If you need earliest provider-side tool detection, inspect wrapped provider events (`StreamEventWrapper`) such as
`content_block_start` + `input_json_delta`.

If no handler exists for a tool name, the SDK returns a tool result like:

> Tool 'X' was called but no handler is registered.

This is intentional: it lets the model see that the tool is unavailable and continue.

### 6.5 Timeouts, cancellation, and interrupts

There are three layers:

1. **Whole-run timeout** (`WithRunTimeout`)
2. **Per-tool timeout** (`WithToolTimeout`)
3. **Context cancellation** (your `ctx`)

Additionally, `RunStream` supports:

- `stream.Cancel()` — abort the current stream and terminate the run loop (StopReason becomes `cancelled`)
- `stream.Interrupt(msg, behavior)` — stop the current stream, optionally save partial output, inject a new user message, and continue

Interrupt behavior controls what happens to partial assistant output:

- `InterruptDiscard` — do not save partial output to history
- `InterruptSavePartial` — save partial output as-is
- `InterruptSaveMarked` — save partial output with a marker (`[interrupted]`)

### 6.6 Deterministic history

`RunStream` emits `HistoryDeltaEvent` events that describe exactly which messages should be appended to a caller-owned history.

This is useful when:

- you want to keep the SDK stateless and own your own history slice
- you want deterministic history updates even across interrupts

`HistoryDeltaEvent` also includes an `ExpectedLen` field. If you want mismatch detection (e.g. when you rewrite history between turns),
use `DefaultHistoryHandlerStrict` instead of `DefaultHistoryHandler`.

In general, `HistoryDeltaEvent` is emitted once per completed step (after tools run, if any) with the assistant message and (if applicable)
the `tool_result` message. If you interrupt a stream with a “save partial” behavior, the saved partial assistant message is emitted as a delta too.

Basic pattern:

```go
history := append([]vai.Message(nil), req.Messages...)

apply := vai.DefaultHistoryHandler(&history)

for ev := range stream.Events() {
	apply(ev)
}
```

Strict variant (mismatch detection):

```go
history := append([]vai.Message(nil), req.Messages...)

apply := vai.DefaultHistoryHandlerStrict(&history)
defer func() {
	if r := recover(); r != nil {
		// Strict handler panics if your history diverges from the stream's ExpectedLen.
		panic(r)
	}
}()

for ev := range stream.Events() {
	apply(ev)
}
```

For advanced context management (pinned memory, trimming, reordering), build per-turn messages at turn boundaries:

```go
stream, err := client.Messages.RunStream(ctx, req,
	vai.WithBuildTurnMessages(func(info vai.TurnInfo) []vai.Message {
		return info.History
	}),
)
if err != nil {
	panic(err)
}
defer stream.Close()
```

`WithBuildTurnMessages` affects only the messages sent to the model on the next turn. It does not change the append-only history represented by `HistoryDeltaEvent`.

---

## 7. Defining Tools (Function Tools)

Function tools are executed by **your process** during the tool loop.

### 7.1 `MakeTool` (recommended)

`MakeTool` gives you:

- a `ToolWithHandler` value containing both:
  - a `Tool` definition (`tool.Tool`)
  - a handler embedded in the returned value
- automatic JSON schema generation from the input struct type

Example:

```go
type SearchInput struct {
	Query string `json:"query" desc:"Search query"`
}

search := vai.MakeTool("search_internal", "Search an internal index.",
	func(ctx context.Context, in SearchInput) (string, error) {
		return "results for: " + in.Query, nil
	},
)

req := &vai.MessageRequest{
	Model: "anthropic/claude-sonnet-4",
	Messages: []vai.Message{
		{Role: "user", Content: vai.Text("Search internal for 'incident 123'")},
	},
}

// WithTools registers the handler and attaches the tool definition for this run.
result, err := client.Messages.Run(ctx, req, vai.WithTools(search))
if err != nil {
	panic(err)
}
```

### 7.2 `FuncAsTool` (returns tool + handler)

`FuncAsTool` is a lightweight helper returning `(Tool, ToolHandler)`:

```go
tool, handler := vai.FuncAsTool("get_weather", "Get weather.",
	func(ctx context.Context, in struct {
		Location string `json:"location"`
	}) (string, error) {
		return "sunny in " + in.Location, nil
	},
)

result, err := client.Messages.Run(ctx, req,
	vai.WithToolHandler(tool.Name, handler),
)
```

### 7.3 `ToolSet` (group tools + handlers)

`ToolSet` is useful for bundling many tools:

```go
ts := vai.NewToolSet()

ts.AddNative(vai.WebSearch())

echoTool, echoHandler := vai.FuncAsTool("echo", "Echo input.",
	func(ctx context.Context, in struct {
		Text string `json:"text"`
	}) (string, error) {
		return in.Text, nil
	},
)
ts.Add(echoTool, echoHandler)

stream, err := client.Messages.RunStream(ctx, req,
	vai.WithToolSet(ts),
)
```

### 7.4 Manual `WithToolHandler`

If you want full control:

```go
client.Messages.Run(ctx, req,
	vai.WithToolHandler("tool_name", func(ctx context.Context, raw json.RawMessage) (any, error) {
		// parse raw, do work, return value
		return map[string]any{"ok": true}, nil
	}),
)
```

### 7.5 Output shaping: returning content blocks

Your handler can return:

- `string` (becomes a single text block)
- `vai.ContentBlock`
- `[]vai.ContentBlock`
- any other JSON-marshalable object (encoded into a text block)

Example returning rich blocks:

```go
return []vai.ContentBlock{
	vai.Text("Found 3 results."),
	vai.Text("1) ..."),
}, nil
```

---

## 8. Native Tools (Provider-Executed)

Native tools are represented as `Tool{Type: "...", Config: ...}` and are executed by the provider if the provider/model supports them.

SDK constructors:

- `vai.WebSearch(...)`
- `vai.CodeExecution(...)`
- `vai.ComputerUse(width, height)`
- `vai.TextEditor()`
- `vai.FileSearch(...)` (provider-specific)

Important:

- Native tool support is **provider/model dependent**.
- The providers in `pkg/core/providers/*` map these normalized tool types to provider-specific tool names and request formats.
- Since providers execute these tools, you generally do **not** register handlers for them.

---

## 9. Streaming Events (RunStream)

### 9.1 Event model

`RunStream.Events()` yields a mix of:

1. **Wrapped provider stream events** (`StreamEventWrapper`)
   - these are `pkg/core/types.StreamEvent` (e.g. `content_block_delta`, `message_delta`, `error`)
2. **Tool-loop lifecycle events**
   - `StepStartEvent`
   - `ToolCallStartEvent`
   - `ToolResultEvent`
   - `StepCompleteEvent`
   - `HistoryDeltaEvent`
   - `InterruptedEvent`
   - `RunCompleteEvent`

`ToolCallStartEvent` corresponds to SDK local tool execution start (not raw provider detection), and its `Input` reflects the full parsed tool input object.
Use wrapped provider events if you need lower-level "tool call is being streamed now" signals.

### 9.2 Helper extractors

Helpers in `sdk/stream_helpers.go`:

- `TextDeltaFrom(event)` — extracts text chunks from wrapped stream deltas
- `ThinkingDeltaFrom(event)` — extracts thinking chunks if present

Example:

```go
for ev := range stream.Events() {
	if t, ok := vai.TextDeltaFrom(ev); ok {
		fmt.Print(t)
	}
}
```

### 9.3 `RunStream.Process` convenience

`Process` consumes events and calls your callbacks:

```go
text, err := stream.Process(vai.StreamCallbacks{
	OnTextDelta: func(t string) { fmt.Print(t) },
	OnToolCallStart: func(id, name string, input map[string]any) {
		log.Printf("tool %s(%v)", name, input)
	},
	OnToolResult: func(id, name string, content []vai.ContentBlock, err error) {
		log.Printf("tool %s done (err=%v)", name, err)
	},
})
```

It returns the accumulated output text and any final error.

---

## 10. Errors and Observability

### Provider errors

Provider errors are returned from underlying provider calls. The core also defines canonical error wrappers in `pkg/core/errors.go`.

Common cases:

- auth errors (missing/invalid API key)
- provider overloads / rate limits
- invalid request shapes (bad model string, invalid tool schema, etc.)

### Handling errors (recommended)

General guidance:

- Use `context.Context` deadlines (`WithRunTimeout`, `WithToolTimeout`, or your `ctx`) to bound retries and prevent stuck runs.
- Retry **transient** provider errors (rate limits / overload / generic API errors) with exponential backoff + jitter.
- Avoid retrying invalid requests (bad model string, malformed tool schema, etc.) unless you modify the request.
- If you retry a whole `Run`/`RunStream`, treat tool execution as potentially non-idempotent (design tools to be idempotent if possible, or persist tool outcomes and replay them carefully).

The SDK may return `*core.Error` for provider failures, which provides a retry signal and optional `RetryAfter`:

```go
import (
	"errors"

	core "github.com/vango-go/vai-lite/pkg/core"
)

var apiErr *core.Error
if errors.As(err, &apiErr) && apiErr.IsRetryable() {
	// consider sleeping based on apiErr.RetryAfter (if set), then retry
}
```

Minimal retry loop for `Run` (illustrative):

```go
import (
	"errors"
	"math/rand"
	"time"

	core "github.com/vango-go/vai-lite/pkg/core"
)

backoff := func(attempt int) time.Duration {
	base := 200 * time.Millisecond
	max := 5 * time.Second
	d := base << attempt
	if d > max {
		d = max
	}
	// jitter in [0.5, 1.5)
	jitter := 0.5 + rand.Float64()
	return time.Duration(float64(d) * jitter)
}

for attempt := 0; attempt < 4; attempt++ {
	result, err := client.Messages.Run(ctx, req, opts...)
	if err == nil {
		_ = result
		break
	}

	var apiErr *core.Error
	if errors.As(err, &apiErr) && apiErr.IsRetryable() && attempt < 3 {
		sleepFor := backoff(attempt)
		if apiErr.RetryAfter != nil {
			sleepFor = time.Duration(*apiErr.RetryAfter) * time.Second
		}
		time.Sleep(sleepFor)
		continue
	}

	// non-retryable (or out of attempts)
	panic(err)
}
```

### Tool handler errors

If a tool handler returns an error, the SDK:

- emits a `ToolResultEvent` with `Error != nil` (RunStream)
- injects a `tool_result` block with `is_error: true`
- continues the loop (the model can respond to the tool failure)

### Logging

The SDK itself is intentionally minimal about logging. You can provide a logger with:

```go
client := vai.NewClient(vai.WithLogger(myLogger))
```

Providers may log debug messages in some cases (e.g. gemini oauth init failure).

---

## 11. Testing and Local Dev

### Running tests in restricted environments

Some environments restrict Go’s default cache dir. If `go test ./...` fails with a cache permission error, run with repo-local caches:

```bash
GOCACHE=$PWD/.gocache GOMODCACHE=$PWD/.gomodcache go test ./...
```

### Formatting

```bash
gofmt -w $(find pkg sdk -name '*.go')
```

---

## 12. Gotchas and Design Notes

### 12.1 “Only Run / RunStream” does not mean “no streaming”

`RunStream` still relies on provider streaming internally, because it’s how it:

- prints tokens as they arrive
- detects tool calls early
- supports interruption while a turn is in progress

### 12.2 Tool definitions vs handler registration

If you manually attach a function tool definition to `req.Tools`, you must also register its handler:

- `WithTools(tool)` for `MakeTool(...)`
- or `WithToolHandler(name, handler)` / `WithToolSet(...)`

If you don’t, the model will call the tool and receive the “no handler registered” result.

Recommended: prefer `WithTools(...)` / `WithToolSet(...)` for function tools, since they both register handlers and attach tool definitions for the run/stream.

### 12.3 Structured output requires you to parse

`OutputFormat` is supported at the request type level, but `vai-lite` does not include an “Extract into struct” helper. Parse `result.Response.TextContent()` yourself.

### 12.4 Native tool behavior depends on the provider

Even though `vai-lite` normalizes tool *types*, providers still differ in:

- whether the model can call that tool
- whether tool execution happens on the provider side
- what events/blocks appear in the stream

Plan for graceful degradation.
