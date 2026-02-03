package types

import (
	"encoding/json"
	"testing"
)

func TestMessageRequest_MarshalJSON(t *testing.T) {
	temp := 0.7
	req := &MessageRequest{
		Model: "anthropic/claude-sonnet-4",
		Messages: []Message{
			{Role: "user", Content: "Hello!"},
		},
		MaxTokens:   1024,
		Temperature: &temp,
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}

	var unmarshaled MessageRequest
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	if unmarshaled.Model != req.Model {
		t.Errorf("Model mismatch: got %q, want %q", unmarshaled.Model, req.Model)
	}
	if unmarshaled.MaxTokens != req.MaxTokens {
		t.Errorf("MaxTokens mismatch: got %d, want %d", unmarshaled.MaxTokens, req.MaxTokens)
	}
}

func TestMessageRequest_WithTools(t *testing.T) {
	req := &MessageRequest{
		Model: "anthropic/claude-sonnet-4",
		Messages: []Message{
			{Role: "user", Content: "What's the weather?"},
		},
		Tools: []Tool{
			{
				Type:        ToolTypeFunction,
				Name:        "get_weather",
				Description: "Get weather for a location",
				InputSchema: &JSONSchema{
					Type: "object",
					Properties: map[string]JSONSchema{
						"location": {Type: "string", Description: "City name"},
					},
					Required: []string{"location"},
				},
			},
		},
		ToolChoice: ToolChoiceAuto(),
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}

	var unmarshaled MessageRequest
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	if len(unmarshaled.Tools) != 1 {
		t.Errorf("Tools count mismatch: got %d, want 1", len(unmarshaled.Tools))
	}
	if unmarshaled.Tools[0].Name != "get_weather" {
		t.Errorf("Tool name mismatch: got %q, want %q", unmarshaled.Tools[0].Name, "get_weather")
	}
}

func TestMessageRequest_WithOutputFormat(t *testing.T) {
	req := &MessageRequest{
		Model: "openai/gpt-4o",
		Messages: []Message{
			{Role: "user", Content: "Extract the data"},
		},
		OutputFormat: &OutputFormat{
			Type: "json_schema",
			JSONSchema: &JSONSchema{
				Type: "object",
				Properties: map[string]JSONSchema{
					"name": {Type: "string"},
					"age":  {Type: "integer"},
				},
				Required: []string{"name", "age"},
			},
		},
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}

	var unmarshaled MessageRequest
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	if unmarshaled.OutputFormat == nil {
		t.Fatal("OutputFormat is nil")
	}
	if unmarshaled.OutputFormat.Type != "json_schema" {
		t.Errorf("OutputFormat type mismatch: got %q, want %q",
			unmarshaled.OutputFormat.Type, "json_schema")
	}
}
