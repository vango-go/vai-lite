package vai

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/vango-go/vai-lite/pkg/core"
	"github.com/vango-go/vai-lite/pkg/core/types"
)

func TestMessagesCreate_UsesCoreCreateMessage(t *testing.T) {
	provider := newScriptedProvider("test").withCreateResponses(
		textResponse(`{"name":"alice"}`),
	)
	svc := newMessagesServiceForMessagesTest(provider)

	resp, err := svc.Create(context.Background(), &MessageRequest{
		Model: "test/model",
		Messages: []Message{
			{Role: "user", Content: Text("hello")},
		},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if resp == nil {
		t.Fatal("Create() returned nil response")
	}
	if len(provider.createRequests) != 1 {
		t.Fatalf("len(createRequests) = %d, want 1", len(provider.createRequests))
	}
	if provider.createRequests[0].Stream {
		t.Fatalf("Create() request stream flag = true, want false")
	}
}

func TestMessagesStream_SetsStreamFlagAndAccumulates(t *testing.T) {
	events := singleTurnTextStream("streamed text")
	provider := newScriptedProvider("test", events)
	svc := newMessagesServiceForMessagesTest(provider)

	stream, err := svc.Stream(context.Background(), &MessageRequest{
		Model: "test/model",
		Messages: []Message{
			{Role: "user", Content: Text("hello")},
		},
	})
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	for range stream.Events() {
	}

	if err := stream.Err(); err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("stream.Err() = %v, want nil or EOF", err)
	}
	if got := stream.TextContent(); got != "streamed text" {
		t.Fatalf("stream.TextContent() = %q, want %q", got, "streamed text")
	}
	if len(provider.streamRequests) != 1 {
		t.Fatalf("len(streamRequests) = %d, want 1", len(provider.streamRequests))
	}
	if !provider.streamRequests[0].Stream {
		t.Fatalf("Stream() request stream flag = false, want true")
	}
}

func TestMessagesCreateStream_AliasParityWithStream(t *testing.T) {
	events := singleTurnTextStream("aliased stream")
	provider := newScriptedProvider("test", events)
	svc := newMessagesServiceForMessagesTest(provider)

	stream, err := svc.CreateStream(context.Background(), &MessageRequest{
		Model: "test/model",
		Messages: []Message{
			{Role: "user", Content: Text("hello")},
		},
	})
	if err != nil {
		t.Fatalf("CreateStream() error = %v", err)
	}
	defer stream.Close()

	for range stream.Events() {
	}

	if got := stream.TextContent(); got != "aliased stream" {
		t.Fatalf("stream.TextContent() = %q, want %q", got, "aliased stream")
	}
	if len(provider.streamRequests) != 1 {
		t.Fatalf("len(streamRequests) = %d, want 1", len(provider.streamRequests))
	}
}

func TestExtract_ValidatesPointerInputs(t *testing.T) {
	provider := newScriptedProvider("test").withCreateResponses(textResponse(`{"name":"alice"}`))
	svc := newMessagesServiceForMessagesTest(provider)

	req := &MessageRequest{
		Model:    "test/model",
		Messages: []Message{{Role: "user", Content: Text("parse")}},
	}

	if _, err := svc.Extract(context.Background(), req, nil); err == nil {
		t.Fatalf("Extract(nil) expected error")
	}

	var notPtr struct {
		Name string `json:"name"`
	}
	if _, err := svc.Extract(context.Background(), req, notPtr); err == nil {
		t.Fatalf("Extract(non-pointer) expected error")
	}

	var notStruct int
	if _, err := svc.Extract(context.Background(), req, &notStruct); err == nil {
		t.Fatalf("Extract(pointer to non-struct) expected error")
	}

	type out struct {
		Name string `json:"name"`
	}
	var nilPtr *out
	if _, err := svc.Extract(context.Background(), req, nilPtr); err == nil {
		t.Fatalf("Extract(nil struct pointer) expected error")
	}
}

func TestExtract_AutoInjectsJSONSchemaWhenMissing(t *testing.T) {
	provider := newScriptedProvider("test").withCreateResponses(
		textResponse(`{"name":"alice"}`),
	)
	svc := newMessagesServiceForMessagesTest(provider)

	req := &MessageRequest{
		Model: "test/model",
		Messages: []Message{
			{Role: "user", Content: Text("extract name")},
		},
	}

	var out struct {
		Name string `json:"name" desc:"Person name"`
	}

	_, err := svc.Extract(context.Background(), req, &out)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if out.Name != "alice" {
		t.Fatalf("out.Name = %q, want %q", out.Name, "alice")
	}
	if req.OutputFormat != nil {
		t.Fatalf("original request OutputFormat was mutated")
	}
	if len(provider.createRequests) != 1 {
		t.Fatalf("len(createRequests) = %d, want 1", len(provider.createRequests))
	}
	seen := provider.createRequests[0]
	if seen.OutputFormat == nil {
		t.Fatalf("injected OutputFormat is nil")
	}
	if seen.OutputFormat.Type != "json_schema" {
		t.Fatalf("OutputFormat.Type = %q, want %q", seen.OutputFormat.Type, "json_schema")
	}
	if seen.OutputFormat.JSONSchema == nil {
		t.Fatalf("OutputFormat.JSONSchema is nil")
	}
	if _, ok := seen.OutputFormat.JSONSchema.Properties["name"]; !ok {
		t.Fatalf("schema missing expected property 'name'")
	}
}

func TestExtract_RespectsCallerOutputFormatWhenProvided(t *testing.T) {
	provider := newScriptedProvider("test").withCreateResponses(
		textResponse(`{"name":"alice"}`),
	)
	svc := newMessagesServiceForMessagesTest(provider)

	custom := &types.OutputFormat{
		Type: "json_schema",
		JSONSchema: &types.JSONSchema{
			Type:        "object",
			Description: "preconfigured schema",
		},
	}

	req := &MessageRequest{
		Model:        "test/model",
		Messages:     []Message{{Role: "user", Content: Text("extract")}},
		OutputFormat: custom,
	}

	var out struct {
		Name string `json:"name"`
	}

	_, err := svc.Extract(context.Background(), req, &out)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	seen := provider.createRequests[0]
	if seen.OutputFormat != custom {
		t.Fatalf("OutputFormat pointer was replaced; expected existing value to be preserved")
	}
}

func TestExtract_ParsesDirectJSON(t *testing.T) {
	provider := newScriptedProvider("test").withCreateResponses(
		textResponse(`{"name":"alice","age":30}`),
	)
	svc := newMessagesServiceForMessagesTest(provider)

	req := &MessageRequest{
		Model:    "test/model",
		Messages: []Message{{Role: "user", Content: Text("extract")}},
	}

	var out struct {
		Name string `json:"name"`
		Age  int    `json:"age"`
	}

	_, err := svc.Extract(context.Background(), req, &out)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if out.Name != "alice" || out.Age != 30 {
		t.Fatalf("parsed output = %+v, want {Name:alice Age:30}", out)
	}
}

func TestExtract_ParsesFencedJSONFallback(t *testing.T) {
	provider := newScriptedProvider("test").withCreateResponses(
		textResponse("Result:\n```json\n{\"name\":\"alice\",\"age\":30}\n```"),
	)
	svc := newMessagesServiceForMessagesTest(provider)

	req := &MessageRequest{
		Model:    "test/model",
		Messages: []Message{{Role: "user", Content: Text("extract")}},
	}

	var out struct {
		Name string `json:"name"`
		Age  int    `json:"age"`
	}

	_, err := svc.Extract(context.Background(), req, &out)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if out.Name != "alice" || out.Age != 30 {
		t.Fatalf("parsed output = %+v, want {Name:alice Age:30}", out)
	}
}

func TestExtractTyped_ReturnsTypedValueAndResponse(t *testing.T) {
	provider := newScriptedProvider("test").withCreateResponses(
		textResponse(`{"name":"alice","role":"engineer"}`),
	)
	svc := newMessagesServiceForMessagesTest(provider)

	req := &MessageRequest{
		Model:    "test/model",
		Messages: []Message{{Role: "user", Content: Text("extract")}},
	}

	type contact struct {
		Name string `json:"name"`
		Role string `json:"role"`
	}

	value, resp, err := ExtractTyped[contact](context.Background(), svc, req)
	if err != nil {
		t.Fatalf("ExtractTyped() error = %v", err)
	}
	if resp == nil {
		t.Fatalf("ExtractTyped() returned nil response")
	}
	if value.Name != "alice" || value.Role != "engineer" {
		t.Fatalf("value = %+v, want {Name:alice Role:engineer}", value)
	}
}

func newMessagesServiceForMessagesTest(provider core.Provider) *MessagesService {
	engine := core.NewEngine(nil)
	engine.RegisterProvider(provider)
	client := &Client{core: engine}
	return &MessagesService{client: client}
}

func textResponse(text string) *types.MessageResponse {
	return &types.MessageResponse{
		Type:  "message",
		Role:  "assistant",
		Model: "test-model",
		Content: []types.ContentBlock{
			types.TextBlock{
				Type: "text",
				Text: text,
			},
		},
		StopReason: types.StopReasonEndTurn,
	}
}

func singleTurnTextStream(text string) []types.StreamEvent {
	delta := types.MessageDeltaEvent{Type: "message_delta"}
	delta.Delta.StopReason = types.StopReasonEndTurn

	return []types.StreamEvent{
		types.MessageStartEvent{
			Type: "message_start",
			Message: types.MessageResponse{
				Type:  "message",
				Role:  "assistant",
				Model: "test-model",
			},
		},
		types.ContentBlockStartEvent{
			Type:  "content_block_start",
			Index: 0,
			ContentBlock: types.TextBlock{
				Type: "text",
				Text: "",
			},
		},
		types.ContentBlockDeltaEvent{
			Type:  "content_block_delta",
			Index: 0,
			Delta: types.TextDelta{
				Type: "text_delta",
				Text: text,
			},
		},
		types.ContentBlockStopEvent{
			Type:  "content_block_stop",
			Index: 0,
		},
		delta,
		types.MessageStopEvent{Type: "message_stop"},
	}
}
