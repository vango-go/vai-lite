package limits

import (
	"errors"
	"testing"

	"github.com/vango-go/vai-lite/pkg/core"
	"github.com/vango-go/vai-lite/pkg/core/types"
	"github.com/vango-go/vai-lite/pkg/gateway/config"
)

func TestValidateMessageRequest_TooManyMessages(t *testing.T) {
	req := &types.MessageRequest{
		Model: "anthropic/test",
		Messages: []types.Message{
			{Role: "user", Content: "a"},
			{Role: "user", Content: "b"},
		},
	}

	err := ValidateMessageRequest(req, config.Config{MaxMessages: 1})
	var coreErr *core.Error
	if !errors.As(err, &coreErr) {
		t.Fatalf("expected core.Error, got %T (%v)", err, err)
	}
	if coreErr.Param != "messages" {
		t.Fatalf("param=%q", coreErr.Param)
	}
}

func TestValidateMessageRequest_TotalTextBytesParamPointsToOffendingField(t *testing.T) {
	req := &types.MessageRequest{
		Model: "anthropic/test",
		Messages: []types.Message{
			{Role: "user", Content: "hello"},
			{Role: "user", Content: "a"},
		},
	}

	err := ValidateMessageRequest(req, config.Config{MaxMessages: 64, MaxTotalTextBytes: 5})
	var coreErr *core.Error
	if !errors.As(err, &coreErr) {
		t.Fatalf("expected core.Error, got %T (%v)", err, err)
	}
	if coreErr.Param != "messages[1].content" {
		t.Fatalf("param=%q", coreErr.Param)
	}
}

func TestValidateMessageRequest_Base64PerBlockBudget(t *testing.T) {
	req := &types.MessageRequest{
		Model: "anthropic/test",
		Messages: []types.Message{
			{
				Role: "user",
				Content: []types.ContentBlock{
					types.AudioBlock{
						Type: "audio",
						Source: types.AudioSource{
							Type:      "base64",
							MediaType: "audio/wav",
							Data:      "AAAA",
						},
					},
				},
			},
		},
	}

	err := ValidateMessageRequest(req, config.Config{
		MaxMessages:         64,
		MaxTotalTextBytes:   1 << 20,
		MaxB64BytesPerBlock: 1,
		MaxB64BytesTotal:    100,
	})
	var coreErr *core.Error
	if !errors.As(err, &coreErr) {
		t.Fatalf("expected core.Error, got %T (%v)", err, err)
	}
	if coreErr.Param != "messages[0].content[0].source.data" {
		t.Fatalf("param=%q", coreErr.Param)
	}
}

func TestValidateMessageRequest_Base64TotalBudget(t *testing.T) {
	req := &types.MessageRequest{
		Model: "anthropic/test",
		Messages: []types.Message{
			{
				Role: "user",
				Content: []types.ContentBlock{
					types.AudioBlock{
						Type: "audio",
						Source: types.AudioSource{
							Type:      "base64",
							MediaType: "audio/wav",
							Data:      "AAAA",
						},
					},
					types.AudioBlock{
						Type: "audio",
						Source: types.AudioSource{
							Type:      "base64",
							MediaType: "audio/wav",
							Data:      "AAAA",
						},
					},
				},
			},
		},
	}

	err := ValidateMessageRequest(req, config.Config{
		MaxMessages:         64,
		MaxTotalTextBytes:   1 << 20,
		MaxB64BytesPerBlock: 100,
		MaxB64BytesTotal:    5, // "AAAA" decodes to 3 bytes, so the second block trips the total budget.
	})
	var coreErr *core.Error
	if !errors.As(err, &coreErr) {
		t.Fatalf("expected core.Error, got %T (%v)", err, err)
	}
	if coreErr.Param != "messages[0].content[1].source.data" {
		t.Fatalf("param=%q", coreErr.Param)
	}
}
