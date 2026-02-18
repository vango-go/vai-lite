# vai-lite

Minimal Vango AI Go SDK focused on:
- Single-turn methods: `Messages.Create()`, `Messages.Stream()`, `Messages.CreateStream()`
- `Messages.Run()` and `Messages.RunStream()`
- Tool execution + tool helpers (native tool normalization, `MakeTool`, `FuncAsTool`, `ToolSet`, etc.)

This repo is **direct-mode only**: it runs in-process and calls providers directly. There is no proxy server or live mode.

Cartesia non-live audio mode is supported through request `Voice` config:
- Audio input blocks can be transcribed via STT before model invocation.
- Output text can be synthesized to audio chunks/final audio.

## Install

```bash
go get github.com/vango-go/vai-lite/sdk
```

## Cartesia setup

Configure Cartesia using either:
- `CARTESIA_API_KEY`
- `vai.WithProviderKey("cartesia", "...")`

## Quickstart (RunStream)

```go
package main

import (
	"context"
	"fmt"

	vai "github.com/vango-go/vai-lite/sdk"
)

func main() {
	// Direct mode reads provider keys from env:
	//   ANTHROPIC_API_KEY, OPENAI_API_KEY, GROQ_API_KEY, GEMINI_API_KEY, etc.
	client := vai.NewClient()

	stream, err := client.Messages.RunStream(context.Background(), &vai.MessageRequest{
		Model: "anthropic/claude-sonnet-4",
		Messages: []vai.Message{
			{Role: "user", Content: vai.Text("Search for the latest Go release and summarize what's new.")},
		},
		Tools: []vai.Tool{vai.WebSearch()},
	}, vai.WithMaxToolCalls(5))
	if err != nil {
		panic(err)
	}
	defer stream.Close()

	for event := range stream.Events() {
		if text, ok := vai.TextDeltaFrom(event); ok {
			fmt.Print(text)
		}
	}
	fmt.Println()
}
```

## Single-turn calls

```go
resp, err := client.Messages.Create(ctx, &vai.MessageRequest{
	Model: "anthropic/claude-sonnet-4",
	Messages: []vai.Message{
		{Role: "user", Content: vai.Text("Summarize this in one sentence.")},
	},
})
if err != nil {
	panic(err)
}
fmt.Println(resp.TextContent())
```

```go
stream, err := client.Messages.CreateStream(ctx, &vai.MessageRequest{
	Model: "anthropic/claude-sonnet-4",
	Messages: []vai.Message{
		{Role: "user", Content: vai.Text("Stream a short haiku.")},
	},
})
if err != nil {
	panic(err)
}
defer stream.Close()
for ev := range stream.Events() {
	if delta, ok := vai.TextDeltaFrom(ev); ok {
		fmt.Print(delta)
	}
}
```

## Cartesia Audio Mode (non-live)

### `Create` with audio input + final audio output

```go
req := &vai.MessageRequest{
	Model: "openai/gpt-4o",
	Messages: []vai.Message{
		{
			Role: "user",
			Content: vai.ContentBlocks(
				vai.Audio(wavBytes, "audio/wav"),
			),
		},
	},
	Voice: vai.VoiceFull(
		"a0e99841-438c-4a64-b679-ae501e7d6091",
		vai.WithLanguage("en"),
		vai.WithAudioFormat(vai.AudioFormatWAV),
	),
}

resp, err := client.Messages.Create(ctx, req)
if err != nil {
	panic(err)
}
fmt.Println("Transcript:", resp.UserTranscript())
if audio := resp.AudioContent(); audio != nil {
	fmt.Println("Synthesized media type:", audio.Source.MediaType)
}
```

### `Stream` with text events + audio side channel

```go
stream, err := client.Messages.Stream(ctx, &vai.MessageRequest{
	Model: "anthropic/claude-sonnet-4",
	Messages: []vai.Message{
		{Role: "user", Content: vai.Text("Tell me a short story.")},
	},
	Voice: vai.VoiceOutput("a0e99841-438c-4a64-b679-ae501e7d6091"),
})
if err != nil {
	panic(err)
}
defer stream.Close()

go func() {
	for chunk := range stream.AudioEvents() {
		play(chunk.Data, chunk.Format)
	}
}()

for ev := range stream.Events() {
	if delta, ok := ev.(vai.ContentBlockDeltaEvent); ok {
		if t, ok := delta.Delta.(vai.TextDelta); ok {
			fmt.Print(t.Text)
		}
	}
}
```

`Run`/`RunStream` remain the primary agent APIs; voice mode is additive when `req.Voice` is set.

## Structured output

```go
type Contact struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

var out Contact
_, err = client.Messages.Extract(ctx, &vai.MessageRequest{
	Model: "openai/gpt-4o",
	Messages: []vai.Message{
		{Role: "user", Content: vai.Text("Jane Doe <jane@example.com>")},
	},
}, &out)
if err != nil {
	panic(err)
}
```

```go
contact, _, err := vai.ExtractTyped[Contact](ctx, client.Messages, &vai.MessageRequest{
	Model: "openai/gpt-4o",
	Messages: []vai.Message{
		{Role: "user", Content: vai.Text("Jane Doe <jane@example.com>")},
	},
})
```

## History + context management

`RunStream` emits `HistoryDeltaEvent` events that describe exactly which messages to append to a caller-owned history slice:

```go
ctx := context.Background()
history := []vai.Message{{Role: "user", Content: vai.Text("Hello!")}}

stream, err := client.Messages.RunStream(ctx, &vai.MessageRequest{
	Model:    "anthropic/claude-sonnet-4",
	Messages: history,
})
if err != nil {
	panic(err)
}
defer stream.Close()

apply := vai.DefaultHistoryHandler(&history)
for ev := range stream.Events() {
	apply(ev)
}
```

If you want mismatch detection (e.g. to catch accidental history divergence), use `DefaultHistoryHandlerStrict`.

For advanced context management (pinned memory, trimming, reordering), build per-turn messages at turn boundaries:

```go
req := &vai.MessageRequest{
	Model:    "anthropic/claude-sonnet-4",
	Messages: history,
}

stream, _ := client.Messages.RunStream(ctx, req,
	vai.WithBuildTurnMessages(func(info vai.TurnInfo) []vai.Message {
		// info.History is an append-only history snapshot for this turn boundary.
		return info.History
	}),
)
```

## Function tools (client-executed)

```go
type WeatherInput struct {
	Location string `json:"location" desc:"City name"`
}

tool := vai.MakeTool("get_weather", "Get current weather for a location.",
	func(ctx context.Context, in WeatherInput) (string, error) {
		return "72F and sunny in " + in.Location, nil
	},
)

// WithTools(tool) registers the handler and attaches the tool definition for this run.
result, err := client.Messages.Run(ctx, &vai.MessageRequest{
	Model: "openai/gpt-4o",
	Messages: []vai.Message{{Role: "user", Content: vai.Text("What's the weather in Austin?")}},
}, vai.WithTools(tool))
```
