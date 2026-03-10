package assets

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	neon "github.com/vango-go/vango-neon"
	s3store "github.com/vango-go/vango-s3"

	"github.com/vango-go/vai-lite/pkg/core"
	"github.com/vango-go/vai-lite/pkg/core/types"
)

const (
	tableAssets      = "vai_assets"
	tableIdempotency = "vai_idempotency_records"

	defaultSignTTL      = 30 * time.Minute
	defaultResolveTTL   = 10 * time.Minute
	defaultFetchTimeout = 30 * time.Second

	maxImageAssetBytes    = 20 << 20
	maxAudioAssetBytes    = 25 << 20
	maxVideoAssetBytes    = 50 << 20
	maxDocumentAssetBytes = 25 << 20
)

type Principal struct {
	OrgID       string
	PrincipalID string
}

type Config struct {
	StorageProvider string
	SignTTL         time.Duration
	ResolveURLTTL   time.Duration
	FetchTimeout    time.Duration
}

type Service struct {
	db         neon.DB
	blobStore  s3store.Store
	httpClient *http.Client
	cfg        Config
}

type Record struct {
	ID                 string
	OrgID              string
	StorageProvider    string
	Bucket             string
	ObjectKey          string
	MediaType          string
	ValidatedMediaType string
	SizeBytes          int64
	SHA256             string
	ScanStatus         string
	Metadata           map[string]any
	CreatedAt          time.Time
	ExpiresAt          *time.Time
}

type idempotencyRecord struct {
	RequestHash string
	ResultRef   map[string]any
	ExpiresAt   time.Time
}

func NewService(db neon.DB, blobStore s3store.Store, httpClient *http.Client, cfg Config) *Service {
	if db == nil || blobStore == nil {
		return nil
	}
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	if strings.TrimSpace(cfg.StorageProvider) == "" {
		cfg.StorageProvider = "s3"
	}
	if cfg.SignTTL <= 0 {
		cfg.SignTTL = defaultSignTTL
	}
	if cfg.ResolveURLTTL <= 0 {
		cfg.ResolveURLTTL = defaultResolveTTL
	}
	if cfg.FetchTimeout <= 0 {
		cfg.FetchTimeout = defaultFetchTimeout
	}
	return &Service{
		db:         db,
		blobStore:  blobStore,
		httpClient: httpClient,
		cfg:        cfg,
	}
}

func (s *Service) CreateUploadIntent(ctx context.Context, principal Principal, req types.AssetUploadIntentRequest, idempotencyKey string) (*types.AssetUploadIntentResponse, error) {
	if err := s.requireConfigured(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(idempotencyKey) == "" {
		return nil, core.NewInvalidRequestError("Idempotency-Key header is required")
	}
	contentType, assetClass, maxBytes, err := normalizeAssetContentType(req.ContentType)
	if err != nil {
		return nil, err
	}
	if principal.OrgID == "" || principal.PrincipalID == "" {
		return nil, core.NewPermissionError("asset uploads require an authenticated principal")
	}
	if req.SizeBytes <= 0 {
		return nil, core.NewInvalidRequestErrorWithParam("size_bytes must be greater than 0", "size_bytes")
	}
	if req.SizeBytes > maxBytes {
		return nil, &core.Error{
			Type:    core.ErrInvalidRequest,
			Message: fmt.Sprintf("asset size %d exceeds limit %d for %s uploads", req.SizeBytes, maxBytes, assetClass),
			Param:   "size_bytes",
			Code:    string(types.ErrorCodeAssetPayloadTooLarge),
		}
	}
	requestHash := payloadHash(map[string]any{
		"filename":     strings.TrimSpace(req.Filename),
		"content_type": contentType,
		"size_bytes":   req.SizeBytes,
		"metadata":     req.Metadata,
	})
	if existing, err := s.getIdempotency(ctx, principal, "asset.upload_intent", idempotencyKey); err == nil && existing != nil {
		if existing.RequestHash != requestHash {
			return nil, core.NewInvalidRequestError("idempotent request payload conflict")
		}
		resp, decodeErr := decodeUploadIntent(existing.ResultRef)
		if decodeErr == nil {
			return resp, nil
		}
	}

	prefix, err := s3store.Prefix(principal.OrgID)
	if err != nil {
		return nil, err
	}
	intent, err := s.blobStore.CreateUploadIntent(ctx, s3store.IntentOptions{
		TenantPrefix: prefix,
		Filename:     req.Filename,
		ContentType:  contentType,
		Size:         req.SizeBytes,
	})
	if err != nil {
		return nil, err
	}
	resp := &types.AssetUploadIntentResponse{
		IntentToken: intent.Token,
		UploadURL:   intent.PresignedURL,
		ContentType: intent.BoundContentType,
		Headers:     intent.RequiredHeaders,
		ExpiresAt:   intent.ExpiresAt,
	}
	if err := s.saveIdempotency(ctx, principal, "asset.upload_intent", idempotencyKey, requestHash, map[string]any{
		"intent_token": resp.IntentToken,
		"upload_url":   resp.UploadURL,
		"content_type": resp.ContentType,
		"headers":      resp.Headers,
		"expires_at":   resp.ExpiresAt,
	}, resp.ExpiresAt); err != nil {
		return nil, err
	}
	return resp, nil
}

func (s *Service) ClaimAsset(ctx context.Context, principal Principal, req types.AssetClaimRequest, idempotencyKey string) (*types.AssetRecord, error) {
	if err := s.requireConfigured(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(idempotencyKey) == "" {
		return nil, core.NewInvalidRequestError("Idempotency-Key header is required")
	}
	contentType, assetClass, _, err := normalizeAssetContentType(req.ContentType)
	if err != nil {
		return nil, err
	}
	if principal.OrgID == "" || principal.PrincipalID == "" {
		return nil, core.NewPermissionError("asset claims require an authenticated principal")
	}
	if strings.TrimSpace(req.IntentToken) == "" {
		return nil, core.NewInvalidRequestErrorWithParam("intent_token is required", "intent_token")
	}
	requestHash := payloadHash(map[string]any{
		"intent_token":  req.IntentToken,
		"filename":      strings.TrimSpace(req.Filename),
		"content_type":  contentType,
		"metadata":      req.Metadata,
		"asset_class":   assetClass,
		"principal_org": principal.OrgID,
	})
	if existing, err := s.getIdempotency(ctx, principal, "asset.claim", idempotencyKey); err == nil && existing != nil {
		if existing.RequestHash != requestHash {
			return nil, core.NewInvalidRequestError("idempotent request payload conflict")
		}
		if assetID, _ := existing.ResultRef["asset_id"].(string); assetID != "" {
			record, getErr := s.getRecord(ctx, principal.OrgID, assetID)
			if getErr == nil {
				return record.toPublic(), nil
			}
		}
	}

	prefix, err := s3store.Prefix(principal.OrgID)
	if err != nil {
		return nil, err
	}
	finalKey, err := s3store.FinalKeyChecked(prefix, finalKeySegment(assetClass))
	if err != nil {
		return nil, err
	}
	ref, err := s.blobStore.ClaimUpload(ctx, req.IntentToken, prefix, finalKey, s3store.WithClaimContentSniff(true))
	if err != nil {
		return nil, err
	}

	bytes, sniffedType, sha, err := s.fetchAssetBytes(ctx, ref.Key, ref.Size)
	if err != nil {
		return nil, err
	}
	if sniffedType == "" {
		sniffedType = contentType
	}
	now := time.Now().UTC()
	record := &Record{
		ID:                 newID("asset"),
		OrgID:              principal.OrgID,
		StorageProvider:    s.cfg.StorageProvider,
		Bucket:             ref.Bucket,
		ObjectKey:          ref.Key,
		MediaType:          contentType,
		ValidatedMediaType: sniffedType,
		SizeBytes:          int64(len(bytes)),
		SHA256:             sha,
		ScanStatus:         "trusted",
		Metadata:           cloneMap(req.Metadata),
		CreatedAt:          now,
	}
	if err := s.insertRecord(ctx, record); err != nil {
		return nil, err
	}
	if err := s.saveIdempotency(ctx, principal, "asset.claim", idempotencyKey, requestHash, map[string]any{
		"asset_id": record.ID,
	}, now.Add(24*time.Hour)); err != nil {
		return nil, err
	}
	return record.toPublic(), nil
}

func (s *Service) GetAsset(ctx context.Context, orgID, assetID string) (*types.AssetRecord, error) {
	record, err := s.getRecord(ctx, orgID, assetID)
	if err != nil {
		return nil, err
	}
	return record.toPublic(), nil
}

func (s *Service) SignAsset(ctx context.Context, orgID, assetID string) (*types.AssetSignResponse, error) {
	record, err := s.getRecord(ctx, orgID, assetID)
	if err != nil {
		return nil, err
	}
	url, err := s.blobStore.PresignGet(ctx, record.ObjectKey, s.cfg.SignTTL)
	if err != nil {
		return nil, err
	}
	return &types.AssetSignResponse{
		AssetID:   record.ID,
		URL:       url,
		ExpiresAt: time.Now().UTC().Add(s.cfg.SignTTL),
	}, nil
}

func (s *Service) ResolveMessageRequest(ctx context.Context, orgID string, req *types.MessageRequest) (*types.MessageRequest, error) {
	if req == nil {
		return nil, core.NewInvalidRequestError("request is required")
	}
	if !types.RequestHasAssetReferences(req) {
		return req, nil
	}
	if err := s.requireConfigured(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(orgID) == "" {
		return nil, core.NewPermissionError("asset references require an authenticated principal")
	}
	out := *req
	out.Messages = cloneMessages(req.Messages)
	out.System = cloneSystem(req.System)

	provider := providerFromModel(req.Model)
	var err error
	if blocks, ok := out.System.([]types.ContentBlock); ok {
		blocks, err = s.resolveBlocks(ctx, orgID, provider, "system", blocks)
		if err != nil {
			return nil, err
		}
		out.System = blocks
	}
	for i := range out.Messages {
		blocks, blockErr := s.resolveBlocks(ctx, orgID, provider, fmt.Sprintf("messages[%d].content", i), out.Messages[i].ContentBlocks())
		if blockErr != nil {
			return nil, blockErr
		}
		out.Messages[i].Content = blocks
	}
	return &out, nil
}

func (s *Service) resolveBlocks(ctx context.Context, orgID, provider, prefix string, blocks []types.ContentBlock) ([]types.ContentBlock, error) {
	if len(blocks) == 0 {
		return nil, nil
	}
	out := make([]types.ContentBlock, 0, len(blocks))
	for i := range blocks {
		path := fmt.Sprintf("%s[%d]", prefix, i)
		resolved, err := s.resolveBlock(ctx, orgID, provider, path, blocks[i])
		if err != nil {
			return nil, err
		}
		out = append(out, resolved)
	}
	return out, nil
}

func (s *Service) resolveBlock(ctx context.Context, orgID, provider, param string, block types.ContentBlock) (types.ContentBlock, error) {
	switch b := block.(type) {
	case types.ImageBlock:
		if !strings.EqualFold(strings.TrimSpace(b.Source.Type), types.AssetSourceType) {
			return b, nil
		}
		resolved, err := s.resolveBinarySource(ctx, orgID, provider, param+".source", b.Source.AssetID)
		if err != nil {
			return nil, err
		}
		b.Source = types.ImageSource{
			Type:      "base64",
			MediaType: resolved.mediaType,
			Data:      resolved.base64Data,
		}
		return b, nil
	case *types.ImageBlock:
		if b == nil {
			return nil, nil
		}
		resolved, err := s.resolveBlock(ctx, orgID, provider, param, *b)
		if err != nil {
			return nil, err
		}
		return resolved, nil
	case types.AudioBlock:
		if !strings.EqualFold(strings.TrimSpace(b.Source.Type), types.AssetSourceType) {
			return b, nil
		}
		resolved, err := s.resolveBinarySource(ctx, orgID, provider, param+".source", b.Source.AssetID)
		if err != nil {
			return nil, err
		}
		b.Source = types.AudioSource{
			Type:      "base64",
			MediaType: resolved.mediaType,
			Data:      resolved.base64Data,
		}
		return b, nil
	case *types.AudioBlock:
		if b == nil {
			return nil, nil
		}
		return s.resolveBlock(ctx, orgID, provider, param, *b)
	case types.AudioSTTBlock:
		if !strings.EqualFold(strings.TrimSpace(b.Source.Type), types.AssetSourceType) {
			return b, nil
		}
		resolved, err := s.resolveBinarySource(ctx, orgID, provider, param+".source", b.Source.AssetID)
		if err != nil {
			return nil, err
		}
		b.Source = types.AudioSource{
			Type:      "base64",
			MediaType: resolved.mediaType,
			Data:      resolved.base64Data,
		}
		return b, nil
	case *types.AudioSTTBlock:
		if b == nil {
			return nil, nil
		}
		return s.resolveBlock(ctx, orgID, provider, param, *b)
	case types.VideoBlock:
		if !strings.EqualFold(strings.TrimSpace(b.Source.Type), types.AssetSourceType) {
			return b, nil
		}
		resolved, err := s.resolveBinarySource(ctx, orgID, provider, param+".source", b.Source.AssetID)
		if err != nil {
			return nil, err
		}
		b.Source = types.VideoSource{
			Type:      "base64",
			MediaType: resolved.mediaType,
			Data:      resolved.base64Data,
		}
		return b, nil
	case *types.VideoBlock:
		if b == nil {
			return nil, nil
		}
		return s.resolveBlock(ctx, orgID, provider, param, *b)
	case types.DocumentBlock:
		if !strings.EqualFold(strings.TrimSpace(b.Source.Type), types.AssetSourceType) {
			return b, nil
		}
		resolved, err := s.resolveBinarySource(ctx, orgID, provider, param+".source", b.Source.AssetID)
		if err != nil {
			return nil, err
		}
		if provider == "oai-resp" {
			b.Source = types.DocumentSource{
				Type:      "url",
				MediaType: resolved.mediaType,
				URL:       resolved.url,
			}
			return b, nil
		}
		b.Source = types.DocumentSource{
			Type:      "base64",
			MediaType: resolved.mediaType,
			Data:      resolved.base64Data,
		}
		return b, nil
	case *types.DocumentBlock:
		if b == nil {
			return nil, nil
		}
		return s.resolveBlock(ctx, orgID, provider, param, *b)
	case types.ToolResultBlock:
		resolved, err := s.resolveBlocks(ctx, orgID, provider, param+".content", b.Content)
		if err != nil {
			return nil, err
		}
		b.Content = resolved
		return b, nil
	case *types.ToolResultBlock:
		if b == nil {
			return nil, nil
		}
		return s.resolveBlock(ctx, orgID, provider, param, *b)
	default:
		return block, nil
	}
}

type resolvedAsset struct {
	mediaType  string
	base64Data string
	url        string
}

func (s *Service) resolveBinarySource(ctx context.Context, orgID, provider, param, assetID string) (*resolvedAsset, error) {
	if strings.TrimSpace(assetID) == "" {
		return nil, core.NewInvalidRequestErrorWithParam("asset_id is required for asset sources", param+".asset_id")
	}
	record, err := s.getRecord(ctx, orgID, assetID)
	if err != nil {
		if coreErr := newAssetLookupError(err); coreErr != nil {
			coreErr.Param = param + ".asset_id"
			return nil, coreErr
		}
		return nil, err
	}
	if !isTrustedScanStatus(record.ScanStatus) {
		return nil, core.NewInvalidRequestErrorWithParam("asset is not trusted for provider execution", param+".asset_id")
	}
	url, err := s.blobStore.PresignGet(ctx, record.ObjectKey, s.cfg.ResolveURLTTL)
	if err != nil {
		return nil, err
	}
	mediaType := effectiveMediaType(record)
	resolved := &resolvedAsset{mediaType: mediaType, url: url}
	if provider == "oai-resp" && isDocumentMediaType(mediaType) {
		return resolved, nil
	}
	data, _, sha, err := s.fetchAssetBytes(ctx, record.ObjectKey, record.SizeBytes)
	if err != nil {
		return nil, err
	}
	if record.SHA256 != "" && sha != "" && !strings.EqualFold(record.SHA256, sha) {
		return nil, core.NewAPIError("asset content hash mismatch during resolution")
	}
	resolved.base64Data = base64.StdEncoding.EncodeToString(data)
	return resolved, nil
}

func (s *Service) getRecord(ctx context.Context, orgID, assetID string) (*Record, error) {
	if err := s.requireConfigured(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(orgID) == "" {
		return nil, core.NewPermissionError("asset access requires an authenticated principal")
	}
	var (
		record       Record
		metadataJSON []byte
		expiresAt    *time.Time
	)
	err := s.db.QueryRow(ctx, `
SELECT
	id,
	org_id,
	storage_provider,
	bucket,
	object_key,
	media_type,
	COALESCE(validated_media_type, ''),
	size_bytes,
	sha256,
	scan_status,
	metadata_json,
	created_at,
	expires_at
FROM `+tableAssets+`
WHERE id = $1 AND org_id = $2
LIMIT 1`, assetID, orgID).Scan(
		&record.ID,
		&record.OrgID,
		&record.StorageProvider,
		&record.Bucket,
		&record.ObjectKey,
		&record.MediaType,
		&record.ValidatedMediaType,
		&record.SizeBytes,
		&record.SHA256,
		&record.ScanStatus,
		&metadataJSON,
		&record.CreatedAt,
		&expiresAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, core.NewNotFoundError("asset not found")
		}
		return nil, err
	}
	record.ExpiresAt = expiresAt
	if len(metadataJSON) > 0 {
		_ = json.Unmarshal(metadataJSON, &record.Metadata)
	}
	return &record, nil
}

func (s *Service) insertRecord(ctx context.Context, record *Record) error {
	if record == nil {
		return nil
	}
	metadataJSON, err := json.Marshal(record.Metadata)
	if err != nil {
		return err
	}
	if len(metadataJSON) == 0 {
		metadataJSON = []byte("{}")
	}
	_, err = s.db.Exec(ctx, `
INSERT INTO `+tableAssets+` (
	id,
	org_id,
	storage_provider,
	bucket,
	object_key,
	media_type,
	validated_media_type,
	size_bytes,
	sha256,
	scan_status,
	metadata_json,
	created_at,
	expires_at
)
VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, ''), $8, $9, $10, $11::jsonb, $12, $13)`,
		record.ID,
		record.OrgID,
		record.StorageProvider,
		record.Bucket,
		record.ObjectKey,
		record.MediaType,
		record.ValidatedMediaType,
		record.SizeBytes,
		record.SHA256,
		record.ScanStatus,
		string(metadataJSON),
		record.CreatedAt.UTC(),
		record.ExpiresAt,
	)
	return err
}

func (s *Service) getIdempotency(ctx context.Context, principal Principal, operation, idempotencyKey string) (*idempotencyRecord, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	if strings.TrimSpace(idempotencyKey) == "" || principal.OrgID == "" || principal.PrincipalID == "" {
		return nil, nil
	}
	var (
		record     idempotencyRecord
		resultJSON []byte
	)
	err := s.db.QueryRow(ctx, `
SELECT request_hash, result_ref_json, expires_at
FROM `+tableIdempotency+`
WHERE org_id = $1
	AND principal_id = $2
	AND chain_id = ''
	AND operation = $3
	AND idempotency_key = $4
	AND expires_at > now()
LIMIT 1`, principal.OrgID, principal.PrincipalID, operation, idempotencyKey).Scan(
		&record.RequestHash,
		&resultJSON,
		&record.ExpiresAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if len(resultJSON) > 0 {
		if err := json.Unmarshal(resultJSON, &record.ResultRef); err != nil {
			return nil, err
		}
	}
	return &record, nil
}

func (s *Service) saveIdempotency(ctx context.Context, principal Principal, operation, idempotencyKey, requestHash string, resultRef map[string]any, expiresAt time.Time) error {
	if s == nil || s.db == nil {
		return nil
	}
	resultJSON, err := json.Marshal(resultRef)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, `
INSERT INTO `+tableIdempotency+` (
	id,
	org_id,
	principal_id,
	chain_id,
	operation,
	idempotency_key,
	request_hash,
	result_ref_json,
	created_at,
	expires_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb, $9, $10)
ON CONFLICT (org_id, principal_id, chain_id, operation, idempotency_key) DO UPDATE
SET request_hash = EXCLUDED.request_hash,
	result_ref_json = EXCLUDED.result_ref_json,
	expires_at = EXCLUDED.expires_at`,
		newID("idem"),
		principal.OrgID,
		principal.PrincipalID,
		"",
		operation,
		idempotencyKey,
		requestHash,
		string(resultJSON),
		time.Now().UTC(),
		expiresAt.UTC(),
	)
	return err
}

func (s *Service) fetchAssetBytes(ctx context.Context, objectKey string, sizeHint int64) ([]byte, string, string, error) {
	reqCtx, cancel := context.WithTimeout(ctx, s.cfg.FetchTimeout)
	defer cancel()
	url, err := s.blobStore.PresignGet(reqCtx, objectKey, s.cfg.ResolveURLTTL)
	if err != nil {
		return nil, "", "", err
	}
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", "", err
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", "", core.NewAPIError(fmt.Sprintf("asset fetch failed with status %d", resp.StatusCode))
	}

	limit := sizeHint
	if limit <= 0 {
		limit = maxVideoAssetBytes
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, "", "", err
	}
	if int64(len(body)) > limit {
		return nil, "", "", &core.Error{
			Type:    core.ErrInvalidRequest,
			Message: fmt.Sprintf("asset payload exceeds allowed limit %d", limit),
			Code:    string(types.ErrorCodeAssetPayloadTooLarge),
		}
	}
	sum := sha256.Sum256(body)
	sniffed := normalizeMediaType(http.DetectContentType(first512(body)))
	return body, sniffed, hex.EncodeToString(sum[:]), nil
}

func (s *Service) requireConfigured() error {
	if s == nil || s.db == nil || s.blobStore == nil {
		return core.NewAPIError("gateway asset storage is not configured")
	}
	return nil
}

func normalizeAssetContentType(raw string) (string, string, int64, error) {
	parsed, err := normalizeMediaTypeWithClass(raw)
	if err != nil {
		return "", "", 0, core.NewInvalidRequestErrorWithParam(err.Error(), "content_type")
	}
	switch parsed.class {
	case "image":
		return parsed.mediaType, parsed.class, maxImageAssetBytes, nil
	case "audio":
		return parsed.mediaType, parsed.class, maxAudioAssetBytes, nil
	case "video":
		return parsed.mediaType, parsed.class, maxVideoAssetBytes, nil
	case "document":
		return parsed.mediaType, parsed.class, maxDocumentAssetBytes, nil
	default:
		return "", "", 0, core.NewInvalidRequestErrorWithParam("unsupported content_type", "content_type")
	}
}

type mediaTypeInfo struct {
	mediaType string
	class     string
}

func normalizeMediaTypeWithClass(raw string) (mediaTypeInfo, error) {
	typ := normalizeMediaType(raw)
	if typ == "" {
		return mediaTypeInfo{}, fmt.Errorf("content_type is required")
	}
	switch {
	case strings.HasPrefix(typ, "image/"):
		return mediaTypeInfo{mediaType: typ, class: "image"}, nil
	case strings.HasPrefix(typ, "audio/"):
		return mediaTypeInfo{mediaType: typ, class: "audio"}, nil
	case strings.HasPrefix(typ, "video/"):
		return mediaTypeInfo{mediaType: typ, class: "video"}, nil
	case isDocumentMediaType(typ):
		return mediaTypeInfo{mediaType: typ, class: "document"}, nil
	default:
		return mediaTypeInfo{}, fmt.Errorf("unsupported content_type %q", typ)
	}
}

func normalizeMediaType(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	mediaType, _, err := mime.ParseMediaType(raw)
	if err != nil {
		return strings.ToLower(raw)
	}
	return strings.ToLower(strings.TrimSpace(mediaType))
}

func isDocumentMediaType(mediaType string) bool {
	switch mediaType {
	case "application/pdf",
		"text/plain",
		"text/markdown",
		"text/csv",
		"application/json",
		"application/xml",
		"text/xml",
		"application/msword",
		"application/vnd.ms-excel",
		"application/vnd.ms-powerpoint",
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		"application/vnd.openxmlformats-officedocument.presentationml.presentation",
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":
		return true
	default:
		return false
	}
}

func finalKeySegment(assetClass string) string {
	switch assetClass {
	case "image":
		return "vai-images"
	case "audio":
		return "vai-audio"
	case "video":
		return "vai-video"
	default:
		return "vai-documents"
	}
}

func effectiveMediaType(record *Record) string {
	if record == nil {
		return ""
	}
	if strings.TrimSpace(record.ValidatedMediaType) != "" {
		return record.ValidatedMediaType
	}
	return record.MediaType
}

func isTrustedScanStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "trusted", "clean", "not_required":
		return true
	default:
		return false
	}
}

func cloneMessages(in []types.Message) []types.Message {
	if len(in) == 0 {
		return nil
	}
	out := make([]types.Message, len(in))
	for i := range in {
		data, err := json.Marshal(in[i])
		if err != nil {
			out[i] = in[i]
			continue
		}
		var cloned types.Message
		if err := json.Unmarshal(data, &cloned); err != nil {
			out[i] = in[i]
			continue
		}
		out[i] = cloned
	}
	return out
}

func cloneSystem(value any) any {
	switch typed := value.(type) {
	case string:
		return typed
	case []types.ContentBlock:
		return cloneContentBlocks(typed)
	case types.ContentBlock:
		blocks := cloneContentBlocks([]types.ContentBlock{typed})
		if len(blocks) == 1 {
			return blocks[0]
		}
		return nil
	default:
		return value
	}
}

func cloneContentBlocks(in []types.ContentBlock) []types.ContentBlock {
	if len(in) == 0 {
		return nil
	}
	data, err := json.Marshal(in)
	if err != nil {
		return append([]types.ContentBlock(nil), in...)
	}
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return append([]types.ContentBlock(nil), in...)
	}
	out := make([]types.ContentBlock, 0, len(raw))
	for i := range raw {
		block, err := types.UnmarshalContentBlock(raw[i])
		if err != nil {
			continue
		}
		out = append(out, block)
	}
	return out
}

func cloneMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	data, err := json.Marshal(in)
	if err != nil {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil
	}
	return out
}

func decodeUploadIntent(result map[string]any) (*types.AssetUploadIntentResponse, error) {
	data, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	var resp types.AssetUploadIntentResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func newAssetLookupError(err error) *core.Error {
	var coreErr *core.Error
	if errors.As(err, &coreErr) {
		switch coreErr.Type {
		case core.ErrNotFound, core.ErrPermission:
			return coreErr
		}
	}
	return nil
}

func (r *Record) toPublic() *types.AssetRecord {
	if r == nil {
		return nil
	}
	return &types.AssetRecord{
		ID:        r.ID,
		MediaType: effectiveMediaType(r),
		SizeBytes: r.SizeBytes,
		CreatedAt: r.CreatedAt,
	}
}

func providerFromModel(model string) string {
	model = strings.TrimSpace(model)
	parts := strings.SplitN(model, "/", 2)
	if len(parts) != 2 {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(parts[0]))
}

func payloadHash(value any) string {
	body, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("marshal_error:%T", value)
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func first512(body []byte) []byte {
	if len(body) <= 512 {
		return body
	}
	return body[:512]
}

func newID(prefix string) string {
	var b [10]byte
	if _, err := rand.Read(b[:]); err != nil {
		now := time.Now().UTC().UnixNano()
		sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%d", prefix, now)))
		return prefix + "_" + hex.EncodeToString(sum[:8])
	}
	return prefix + "_" + hex.EncodeToString(b[:])
}
