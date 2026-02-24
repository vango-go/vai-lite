package limits

import (
	"fmt"
	"strings"

	"github.com/vango-go/vai-lite/pkg/core"
	"github.com/vango-go/vai-lite/pkg/core/types"
	"github.com/vango-go/vai-lite/pkg/gateway/config"
)

func ValidateMessageRequest(req *types.MessageRequest, cfg config.Config) error {
	if req == nil {
		return core.NewInvalidRequestError("request is required")
	}

	if cfg.MaxMessages > 0 && len(req.Messages) > cfg.MaxMessages {
		return core.NewInvalidRequestErrorWithParam(
			fmt.Sprintf("too many messages (max %d)", cfg.MaxMessages),
			"messages",
		)
	}
	if cfg.MaxTools > 0 && len(req.Tools) > cfg.MaxTools {
		return core.NewInvalidRequestErrorWithParam(
			fmt.Sprintf("too many tools (max %d)", cfg.MaxTools),
			"tools",
		)
	}

	var totalTextBytes int64
	var totalDecodedB64 int64

	addText := func(param, s string) error {
		if cfg.MaxTotalTextBytes <= 0 {
			return nil
		}
		n := int64(len(s))
		if totalTextBytes+n > cfg.MaxTotalTextBytes {
			return core.NewInvalidRequestErrorWithParam(
				fmt.Sprintf("total text bytes %d exceeds limit %d", totalTextBytes+n, cfg.MaxTotalTextBytes),
				param,
			)
		}
		totalTextBytes += n
		return nil
	}

	addB64 := func(param, b64 string) error {
		if cfg.MaxB64BytesPerBlock <= 0 && cfg.MaxB64BytesTotal <= 0 {
			return nil
		}

		decoded := estimateDecodedB64Bytes(b64)
		if cfg.MaxB64BytesPerBlock > 0 && decoded > cfg.MaxB64BytesPerBlock {
			return core.NewInvalidRequestErrorWithParam(
				fmt.Sprintf("decoded base64 payload %d exceeds per-block limit %d", decoded, cfg.MaxB64BytesPerBlock),
				param,
			)
		}
		if cfg.MaxB64BytesTotal > 0 && totalDecodedB64+decoded > cfg.MaxB64BytesTotal {
			return core.NewInvalidRequestErrorWithParam(
				fmt.Sprintf("decoded base64 payload total %d exceeds limit %d", totalDecodedB64+decoded, cfg.MaxB64BytesTotal),
				param,
			)
		}
		totalDecodedB64 += decoded
		return nil
	}

	// system: string or []ContentBlock
	switch sys := req.System.(type) {
	case string:
		if err := addText("system", sys); err != nil {
			return err
		}
	case []types.ContentBlock:
		if err := validateBlocks(sys, "system", addText, addB64); err != nil {
			return err
		}
	case nil:
	default:
		// Strict decode should prevent this, but keep behavior safe.
		return core.NewInvalidRequestErrorWithParam("system must be a string or an array of content blocks", "system")
	}

	// messages
	for i := range req.Messages {
		msg := req.Messages[i]
		switch c := msg.Content.(type) {
		case string:
			if err := addText(fmt.Sprintf("messages[%d].content", i), c); err != nil {
				return err
			}
		case []types.ContentBlock:
			if err := validateBlocks(c, fmt.Sprintf("messages[%d].content", i), addText, addB64); err != nil {
				return err
			}
		case nil:
			return core.NewInvalidRequestErrorWithParam("content is required", fmt.Sprintf("messages[%d].content", i))
		default:
			return core.NewInvalidRequestErrorWithParam("content must be a string or an array of content blocks", fmt.Sprintf("messages[%d].content", i))
		}
	}

	return nil
}

func validateBlocks(
	blocks []types.ContentBlock,
	prefix string,
	addText func(param, s string) error,
	addB64 func(param, b64 string) error,
) error {
	for i, block := range blocks {
		path := fmt.Sprintf("%s[%d]", prefix, i)

		switch b := block.(type) {
		case types.TextBlock:
			if err := addText(path+".text", b.Text); err != nil {
				return err
			}
		case *types.TextBlock:
			if b != nil {
				if err := addText(path+".text", b.Text); err != nil {
					return err
				}
			}

		case types.ImageBlock:
			if isBase64Source(b.Source.Type) {
				if err := addB64(path+".source.data", b.Source.Data); err != nil {
					return err
				}
			}
		case *types.ImageBlock:
			if b != nil && isBase64Source(b.Source.Type) {
				if err := addB64(path+".source.data", b.Source.Data); err != nil {
					return err
				}
			}

		case types.AudioBlock:
			if isBase64Source(b.Source.Type) {
				if err := addB64(path+".source.data", b.Source.Data); err != nil {
					return err
				}
			}
		case *types.AudioBlock:
			if b != nil && isBase64Source(b.Source.Type) {
				if err := addB64(path+".source.data", b.Source.Data); err != nil {
					return err
				}
			}

		case types.VideoBlock:
			if isBase64Source(b.Source.Type) {
				if err := addB64(path+".source.data", b.Source.Data); err != nil {
					return err
				}
			}
		case *types.VideoBlock:
			if b != nil && isBase64Source(b.Source.Type) {
				if err := addB64(path+".source.data", b.Source.Data); err != nil {
					return err
				}
			}

		case types.DocumentBlock:
			if isBase64Source(b.Source.Type) {
				if err := addB64(path+".source.data", b.Source.Data); err != nil {
					return err
				}
			}
		case *types.DocumentBlock:
			if b != nil && isBase64Source(b.Source.Type) {
				if err := addB64(path+".source.data", b.Source.Data); err != nil {
					return err
				}
			}

		case types.ToolResultBlock:
			if err := validateBlocks(b.Content, path+".content", addText, addB64); err != nil {
				return err
			}
		case *types.ToolResultBlock:
			if b != nil {
				if err := validateBlocks(b.Content, path+".content", addText, addB64); err != nil {
					return err
				}
			}
		default:
			// All other blocks (tool_use, thinking, etc.) are ignored for budgets.
		}
	}
	return nil
}

func isBase64Source(typ string) bool {
	return strings.EqualFold(strings.TrimSpace(typ), "base64")
}

func estimateDecodedB64Bytes(b64 string) int64 {
	// Conservative decoded length estimate without decoding:
	// decoded ~= floor(len(b64) * 3/4) - padding.
	n := int64(len(b64))
	if n <= 0 {
		return 0
	}
	pad := int64(0)
	if strings.HasSuffix(b64, "==") {
		pad = 2
	} else if strings.HasSuffix(b64, "=") {
		pad = 1
	}
	decoded := (n * 3 / 4) - pad
	if decoded < 0 {
		return 0
	}
	return decoded
}
