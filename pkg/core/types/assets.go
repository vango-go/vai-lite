package types

import (
	"strings"
	"time"
)

const AssetSourceType = "asset"

type AssetUploadIntentRequest struct {
	Filename    string         `json:"filename,omitempty"`
	ContentType string         `json:"content_type"`
	SizeBytes   int64          `json:"size_bytes"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

type AssetUploadIntentResponse struct {
	IntentToken string            `json:"intent_token"`
	UploadURL   string            `json:"upload_url"`
	ContentType string            `json:"content_type"`
	Headers     map[string]string `json:"headers,omitempty"`
	ExpiresAt   time.Time         `json:"expires_at"`
}

type AssetClaimRequest struct {
	IntentToken string         `json:"intent_token"`
	Filename    string         `json:"filename,omitempty"`
	ContentType string         `json:"content_type"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

func RequestHasAssetReferences(req *MessageRequest) bool {
	if req == nil {
		return false
	}
	switch system := req.System.(type) {
	case []ContentBlock:
		if contentBlocksHaveAssetReferences(system) {
			return true
		}
	case ContentBlock:
		if contentBlockHasAssetReference(system) {
			return true
		}
	}
	for i := range req.Messages {
		if contentBlocksHaveAssetReferences(req.Messages[i].ContentBlocks()) {
			return true
		}
	}
	return false
}

func contentBlocksHaveAssetReferences(blocks []ContentBlock) bool {
	for _, block := range blocks {
		if contentBlockHasAssetReference(block) {
			return true
		}
	}
	return false
}

func contentBlockHasAssetReference(block ContentBlock) bool {
	switch b := block.(type) {
	case ImageBlock:
		return isAssetSourceType(b.Source.Type) && strings.TrimSpace(b.Source.AssetID) != ""
	case *ImageBlock:
		return b != nil && isAssetSourceType(b.Source.Type) && strings.TrimSpace(b.Source.AssetID) != ""
	case AudioBlock:
		return isAssetSourceType(b.Source.Type) && strings.TrimSpace(b.Source.AssetID) != ""
	case *AudioBlock:
		return b != nil && isAssetSourceType(b.Source.Type) && strings.TrimSpace(b.Source.AssetID) != ""
	case AudioSTTBlock:
		return isAssetSourceType(b.Source.Type) && strings.TrimSpace(b.Source.AssetID) != ""
	case *AudioSTTBlock:
		return b != nil && isAssetSourceType(b.Source.Type) && strings.TrimSpace(b.Source.AssetID) != ""
	case VideoBlock:
		return isAssetSourceType(b.Source.Type) && strings.TrimSpace(b.Source.AssetID) != ""
	case *VideoBlock:
		return b != nil && isAssetSourceType(b.Source.Type) && strings.TrimSpace(b.Source.AssetID) != ""
	case DocumentBlock:
		return isAssetSourceType(b.Source.Type) && strings.TrimSpace(b.Source.AssetID) != ""
	case *DocumentBlock:
		return b != nil && isAssetSourceType(b.Source.Type) && strings.TrimSpace(b.Source.AssetID) != ""
	case ToolResultBlock:
		return contentBlocksHaveAssetReferences(b.Content)
	case *ToolResultBlock:
		return b != nil && contentBlocksHaveAssetReferences(b.Content)
	default:
		return false
	}
}

func isAssetSourceType(sourceType string) bool {
	return strings.EqualFold(strings.TrimSpace(sourceType), AssetSourceType)
}
