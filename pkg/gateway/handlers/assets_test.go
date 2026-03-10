package handlers

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	neon "github.com/vango-go/vango-neon"
	s3store "github.com/vango-go/vango-s3"

	"github.com/vango-go/vai-lite/pkg/core"
	"github.com/vango-go/vai-lite/pkg/core/types"
	assetsvc "github.com/vango-go/vai-lite/pkg/gateway/assets"
	chainrt "github.com/vango-go/vai-lite/pkg/gateway/chains"
	"github.com/vango-go/vai-lite/pkg/gateway/config"
)

func TestAssetsHandler_Lifecycle(t *testing.T) {
	svc, store := newTestAssetService(t)
	cfg := baseRunsConfig()
	h := AssetsHandler{Config: cfg, Assets: svc}
	pngBytes := testPNGBytes()

	uploadReq := httptest.NewRequest(http.MethodPost, "/v1/assets:upload-intent", bytes.NewReader([]byte(`{
		"filename":"cat.png",
		"content_type":"image/png",
		"size_bytes":16
	}`)))
	uploadReq.Header.Set(idempotencyKeyHeader, "asset_upload_1")
	uploadReq.RemoteAddr = "203.0.113.10:1234"
	uploadRR := httptest.NewRecorder()
	h.ServeHTTP(uploadRR, uploadReq)
	if uploadRR.Code != http.StatusOK {
		t.Fatalf("upload intent status=%d body=%s", uploadRR.Code, uploadRR.Body.String())
	}

	var uploadResp types.AssetUploadIntentResponse
	if err := json.Unmarshal(uploadRR.Body.Bytes(), &uploadResp); err != nil {
		t.Fatalf("decode upload intent: %v", err)
	}
	if uploadResp.IntentToken == "" || uploadResp.UploadURL == "" {
		t.Fatalf("upload intent response=%+v", uploadResp)
	}

	store.Upload(uploadResp.IntentToken, pngBytes, "image/png")

	claimReq := httptest.NewRequest(http.MethodPost, "/v1/assets:claim", bytes.NewReader([]byte(fmt.Sprintf(`{
		"intent_token":%q,
		"filename":"cat.png",
		"content_type":"image/png"
	}`, uploadResp.IntentToken))))
	claimReq.Header.Set(idempotencyKeyHeader, "asset_claim_1")
	claimReq.RemoteAddr = uploadReq.RemoteAddr
	claimRR := httptest.NewRecorder()
	h.ServeHTTP(claimRR, claimReq)
	if claimRR.Code != http.StatusOK {
		t.Fatalf("claim status=%d body=%s", claimRR.Code, claimRR.Body.String())
	}

	var asset types.AssetRecord
	if err := json.Unmarshal(claimRR.Body.Bytes(), &asset); err != nil {
		t.Fatalf("decode asset claim: %v", err)
	}
	if asset.ID == "" || asset.MediaType != "image/png" || asset.SizeBytes != int64(len(pngBytes)) {
		t.Fatalf("asset=%+v", asset)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/v1/assets/"+asset.ID, nil)
	getReq.RemoteAddr = uploadReq.RemoteAddr
	getRR := httptest.NewRecorder()
	h.ServeHTTP(getRR, getReq)
	if getRR.Code != http.StatusOK {
		t.Fatalf("get asset status=%d body=%s", getRR.Code, getRR.Body.String())
	}

	signReq := httptest.NewRequest(http.MethodPost, "/v1/assets/"+asset.ID+":sign", nil)
	signReq.RemoteAddr = uploadReq.RemoteAddr
	signRR := httptest.NewRecorder()
	h.ServeHTTP(signRR, signReq)
	if signRR.Code != http.StatusOK {
		t.Fatalf("sign asset status=%d body=%s", signRR.Code, signRR.Body.String())
	}
	var signed types.AssetSignResponse
	if err := json.Unmarshal(signRR.Body.Bytes(), &signed); err != nil {
		t.Fatalf("decode sign response: %v", err)
	}
	if signed.AssetID != asset.ID || signed.URL == "" {
		t.Fatalf("signed response=%+v", signed)
	}
}

func TestMessagesHandler_ResolvesAssetBackedImage(t *testing.T) {
	svc, store := newTestAssetService(t)
	cfg := baseRunsConfig()
	provider := &capturingAssetProvider{streamEvents: chainTestStreamEvents("ok")}
	h := MessagesHandler{
		Config:    cfg,
		Upstreams: fakeFactory{p: provider},
		Assets:    svc,
	}

	remoteAddr := "203.0.113.20:1234"
	principal := principalForRemote(cfg, remoteAddr)
	pngBytes := testPNGBytes()
	assetID := claimTestAsset(t, svc, store, principal, "image/png", pngBytes)

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader([]byte(fmt.Sprintf(`{
		"model":"anthropic/claude-haiku-4-5",
		"messages":[{"role":"user","content":[{"type":"image","source":{"type":"asset","asset_id":%q}}]}]
	}`, assetID))))
	req.Header.Set("X-Provider-Key-Anthropic", "sk-test")
	req.RemoteAddr = remoteAddr
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("messages status=%d body=%s", rr.Code, rr.Body.String())
	}
	if provider.lastReq == nil {
		t.Fatal("expected provider request")
	}
	image := findImageBlock(t, provider.lastReq.Messages[0].ContentBlocks())
	if image.Source.Type != "base64" || image.Source.MediaType != "image/png" {
		t.Fatalf("image source=%+v", image.Source)
	}
	if image.Source.Data != base64.StdEncoding.EncodeToString(pngBytes) {
		t.Fatalf("image data=%q", image.Source.Data)
	}
}

func TestChainsHandler_RunResolvesAssetBackedImage(t *testing.T) {
	svc, store := newTestAssetService(t)
	cfg := baseRunsConfig()
	provider := &capturingAssetProvider{streamEvents: chainTestStreamEvents("ok")}
	h := ChainsHandler{
		Config:    cfg,
		Upstreams: fakeFactory{p: provider},
		Chains:    chainManagerForTests(),
		Assets:    svc,
	}

	remoteAddr := "203.0.113.30:1234"
	principal := principalForRemote(cfg, remoteAddr)
	pngBytes := testPNGBytes()
	assetID := claimTestAsset(t, svc, store, principal, "image/png", pngBytes)

	createReq := httptest.NewRequest(http.MethodPost, "/v1/chains", bytes.NewReader([]byte(`{
		"defaults":{"model":"anthropic/claude-haiku-4-5"}
	}`)))
	createReq.Header.Set(idempotencyKeyHeader, "chain_asset_create")
	createReq.Header.Set("X-Provider-Key-Anthropic", "sk-test")
	createReq.RemoteAddr = remoteAddr
	createRR := httptest.NewRecorder()
	h.ServeHTTP(createRR, createReq)
	if createRR.Code != http.StatusOK {
		t.Fatalf("create chain status=%d body=%s", createRR.Code, createRR.Body.String())
	}
	var started types.ChainStartedEvent
	if err := json.Unmarshal(createRR.Body.Bytes(), &started); err != nil {
		t.Fatalf("decode chain start: %v", err)
	}

	runReq := httptest.NewRequest(http.MethodPost, "/v1/chains/"+started.ChainID+"/runs", bytes.NewReader([]byte(fmt.Sprintf(`{
		"input":[{"role":"user","content":[{"type":"image","source":{"type":"asset","asset_id":%q}}]}]
	}`, assetID))))
	runReq.Header.Set(idempotencyKeyHeader, "chain_asset_run")
	runReq.Header.Set("X-Provider-Key-Anthropic", "sk-test")
	runReq.RemoteAddr = remoteAddr
	runRR := httptest.NewRecorder()
	h.ServeHTTP(runRR, runReq)
	if runRR.Code != http.StatusOK {
		t.Fatalf("chain run status=%d body=%s", runRR.Code, runRR.Body.String())
	}
	if provider.lastReq == nil {
		t.Fatal("expected provider request")
	}
	image := findImageBlock(t, provider.lastReq.Messages[0].ContentBlocks())
	if image.Source.Type != "base64" || image.Source.Data != base64.StdEncoding.EncodeToString(pngBytes) {
		t.Fatalf("resolved image source=%+v", image.Source)
	}
}

func TestMessagesHandler_ResolvesOAIRespDocumentAssetToURL(t *testing.T) {
	svc, store := newTestAssetService(t)
	cfg := baseRunsConfig()
	provider := &capturingAssetProvider{streamEvents: chainTestStreamEvents("ok")}
	h := MessagesHandler{
		Config:    cfg,
		Upstreams: fakeFactory{p: provider},
		Assets:    svc,
	}

	remoteAddr := "203.0.113.40:1234"
	principal := principalForRemote(cfg, remoteAddr)
	docBytes := []byte("%PDF-1.4\n%fake\n")
	assetID := claimTestAsset(t, svc, store, principal, "application/pdf", docBytes)

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader([]byte(fmt.Sprintf(`{
		"model":"oai-resp/gpt-5-mini",
		"messages":[{"role":"user","content":[{"type":"document","filename":"a.pdf","source":{"type":"asset","asset_id":%q}}]}]
	}`, assetID))))
	req.Header.Set("X-Provider-Key-OpenAI", "sk-test")
	req.RemoteAddr = remoteAddr
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("messages status=%d body=%s", rr.Code, rr.Body.String())
	}
	if provider.lastReq == nil {
		t.Fatal("expected provider request")
	}
	blocks := provider.lastReq.Messages[0].ContentBlocks()
	document, ok := blocks[0].(types.DocumentBlock)
	if !ok {
		t.Fatalf("block=%T, want DocumentBlock", blocks[0])
	}
	if document.Source.Type != "url" || document.Source.URL == "" || document.Source.MediaType != "application/pdf" {
		t.Fatalf("document source=%+v", document.Source)
	}
}

type capturingAssetProvider struct {
	lastReq      *types.MessageRequest
	streamEvents []types.StreamEvent
}

func (p *capturingAssetProvider) Name() string { return "anthropic" }
func (p *capturingAssetProvider) Capabilities() core.ProviderCapabilities {
	return core.ProviderCapabilities{Tools: true}
}
func (p *capturingAssetProvider) CreateMessage(ctx context.Context, req *types.MessageRequest) (*types.MessageResponse, error) {
	p.lastReq = cloneMessageRequestForTest(req)
	return &types.MessageResponse{
		Type:       "message",
		Role:       "assistant",
		Model:      req.Model,
		StopReason: types.StopReasonEndTurn,
		Content:    []types.ContentBlock{types.TextBlock{Type: "text", Text: "ok"}},
	}, nil
}
func (p *capturingAssetProvider) StreamMessage(ctx context.Context, req *types.MessageRequest) (core.EventStream, error) {
	p.lastReq = cloneMessageRequestForTest(req)
	return &fakeEventStream{events: p.streamEvents}, nil
}

func cloneMessageRequestForTest(req *types.MessageRequest) *types.MessageRequest {
	if req == nil {
		return nil
	}
	body, err := json.Marshal(req)
	if err != nil {
		cloned := *req
		return &cloned
	}
	var cloned types.MessageRequest
	if err := json.Unmarshal(body, &cloned); err != nil {
		clone := *req
		return &clone
	}
	return &cloned
}

func chainManagerForTests() *chainrt.Manager {
	return chainrt.NewManager(nil, chainrt.DefaultManagerConfig())
}

func principalForRemote(cfg config.Config, remoteAddr string) assetsvc.Principal {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = remoteAddr
	pr := chainPrincipalFromRequest(req, cfg)
	return assetsvc.Principal{OrgID: pr.OrgID, PrincipalID: pr.PrincipalID}
}

func claimTestAsset(t *testing.T, svc *assetsvc.Service, store *assetTestStore, principal assetsvc.Principal, contentType string, body []byte) string {
	t.Helper()
	intent, err := svc.CreateUploadIntent(context.Background(), principal, types.AssetUploadIntentRequest{
		Filename:    "asset",
		ContentType: contentType,
		SizeBytes:   int64(len(body)),
	}, "intent_"+contentType)
	if err != nil {
		t.Fatalf("create upload intent: %v", err)
	}
	store.Upload(intent.IntentToken, body, contentType)
	asset, err := svc.ClaimAsset(context.Background(), principal, types.AssetClaimRequest{
		IntentToken: intent.IntentToken,
		Filename:    "asset",
		ContentType: contentType,
	}, "claim_"+contentType+"_"+base64.StdEncoding.EncodeToString(body))
	if err != nil {
		t.Fatalf("claim asset: %v", err)
	}
	return asset.ID
}

func newTestAssetService(t *testing.T) (*assetsvc.Service, *assetTestStore) {
	t.Helper()
	db := newAssetTestDB()
	store := newAssetTestStore()
	client := &http.Client{Transport: store}
	svc := assetsvc.NewService(db, store, client, assetsvc.Config{StorageProvider: "s3"})
	if svc == nil {
		t.Fatal("expected asset service")
	}
	return svc, store
}

func testPNGBytes() []byte {
	return []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0x00, 0x00, 0x00, 0x0d, 'I', 'H', 'D', 'R'}
}

func findImageBlock(t *testing.T, blocks []types.ContentBlock) types.ImageBlock {
	t.Helper()
	for _, block := range blocks {
		if image, ok := block.(types.ImageBlock); ok {
			return image
		}
	}
	t.Fatalf("no ImageBlock found in %#v", blocks)
	return types.ImageBlock{}
}

type assetTestStore struct {
	mu      sync.Mutex
	next    int
	intents map[string]*assetTestIntent
	objects map[string]*assetTestObject
}

type assetTestIntent struct {
	tenantPrefix string
	contentType  string
	size         int64
	body         []byte
}

type assetTestObject struct {
	contentType string
	body        []byte
}

func newAssetTestStore() *assetTestStore {
	return &assetTestStore{
		intents: make(map[string]*assetTestIntent),
		objects: make(map[string]*assetTestObject),
	}
}

func (s *assetTestStore) CreateUploadIntent(ctx context.Context, opts s3store.IntentOptions) (*s3store.UploadIntent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.next++
	token := fmt.Sprintf("intent_%d", s.next)
	s.intents[token] = &assetTestIntent{
		tenantPrefix: opts.TenantPrefix,
		contentType:  strings.ToLower(strings.TrimSpace(opts.ContentType)),
		size:         opts.Size,
	}
	return &s3store.UploadIntent{
		Token:            token,
		PresignedURL:     "https://upload.test/" + token,
		BoundContentType: opts.ContentType,
		RequiredHeaders:  map[string]string{"Content-Type": opts.ContentType},
		ExpiresAt:        time.Now().UTC().Add(10 * time.Minute),
	}, nil
}

func (s *assetTestStore) Upload(token string, body []byte, contentType string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	intent := s.intents[token]
	if intent == nil {
		panic("unknown upload intent token")
	}
	intent.body = append([]byte(nil), body...)
	intent.contentType = strings.ToLower(strings.TrimSpace(contentType))
}

func (s *assetTestStore) ClaimUpload(ctx context.Context, intentToken string, tenantPrefix string, finalKey string, opts ...s3store.ClaimOption) (*s3store.BlobRef, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	intent := s.intents[intentToken]
	if intent == nil {
		return nil, fmt.Errorf("unknown intent token")
	}
	if intent.tenantPrefix != tenantPrefix {
		return nil, fmt.Errorf("tenant mismatch")
	}
	if int64(len(intent.body)) > intent.size {
		return nil, fmt.Errorf("size exceeded")
	}
	s.objects[finalKey] = &assetTestObject{
		contentType: intent.contentType,
		body:        append([]byte(nil), intent.body...),
	}
	delete(s.intents, intentToken)
	return &s3store.BlobRef{
		Bucket: "test-bucket",
		Key:    finalKey,
		Size:   int64(len(intent.body)),
	}, nil
}

func (s *assetTestStore) PresignGet(ctx context.Context, key string, expiry time.Duration, opts ...s3store.PresignGetOption) (string, error) {
	return "https://assets.test/" + url.PathEscape(key), nil
}

func (s *assetTestStore) PresignPut(ctx context.Context, key string, expiry time.Duration, opts ...s3store.PresignPutOption) (*s3store.PresignPutResult, error) {
	return &s3store.PresignPutResult{URL: "https://upload.test/" + url.PathEscape(key), ExpiresAt: time.Now().UTC().Add(expiry)}, nil
}

func (s *assetTestStore) S3Client() *s3.Client { return nil }
func (s *assetTestStore) Close() error         { return nil }

func (s *assetTestStore) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host != "assets.test" {
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Body:       io.NopCloser(strings.NewReader("unexpected host")),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	}
	key, _ := url.PathUnescape(strings.TrimPrefix(req.URL.Path, "/"))
	s.mu.Lock()
	object := s.objects[key]
	s.mu.Unlock()
	if object == nil {
		return &http.Response{
			StatusCode: http.StatusNotFound,
			Body:       io.NopCloser(strings.NewReader("missing")),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	}
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader(object.body)),
		Header:     make(http.Header),
		Request:    req,
	}
	resp.Header.Set("Content-Type", object.contentType)
	return resp, nil
}

type assetTestDBState struct {
	mu          sync.Mutex
	assets      map[string]assetDBRecord
	idempotency map[string]assetDBIdempotency
}

type assetDBRecord struct {
	values []any
}

type assetDBIdempotency struct {
	requestHash string
	resultJSON  []byte
	expiresAt   time.Time
}

func newAssetTestDB() *neon.TestDB {
	state := &assetTestDBState{
		assets:      make(map[string]assetDBRecord),
		idempotency: make(map[string]assetDBIdempotency),
	}
	return &neon.TestDB{
		ExecFunc: func(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
			state.mu.Lock()
			defer state.mu.Unlock()
			switch {
			case strings.Contains(sql, "INSERT INTO vai_assets"):
				id := args[0].(string)
				orgID := args[1].(string)
				state.assets[id+"::"+orgID] = assetDBRecord{
					values: []any{
						args[0],
						args[1],
						args[2],
						args[3],
						args[4],
						args[5],
						args[6],
						args[7],
						args[8],
						args[9],
						[]byte(args[10].(string)),
						args[11],
						args[12],
					},
				}
				return pgconn.NewCommandTag("INSERT 1"), nil
			case strings.Contains(sql, "INSERT INTO vai_idempotency_records"):
				key := fmt.Sprintf("%s::%s::%s::%s", args[1], args[2], args[4], args[5])
				state.idempotency[key] = assetDBIdempotency{
					requestHash: args[6].(string),
					resultJSON:  []byte(args[7].(string)),
					expiresAt:   args[9].(time.Time),
				}
				return pgconn.NewCommandTag("INSERT 1"), nil
			default:
				return pgconn.NewCommandTag("INSERT 0"), nil
			}
		},
		QueryRowFunc: func(ctx context.Context, sql string, args ...any) pgx.Row {
			state.mu.Lock()
			defer state.mu.Unlock()
			switch {
			case strings.Contains(sql, "FROM vai_assets"):
				record, ok := state.assets[args[0].(string)+"::"+args[1].(string)]
				if !ok {
					return assetTestRow{err: pgx.ErrNoRows}
				}
				return assetTestRow{values: record.values}
			case strings.Contains(sql, "FROM vai_idempotency_records"):
				key := fmt.Sprintf("%s::%s::%s::%s", args[0], args[1], args[2], args[3])
				record, ok := state.idempotency[key]
				if !ok || !record.expiresAt.After(time.Now().UTC()) {
					return assetTestRow{err: pgx.ErrNoRows}
				}
				return assetTestRow{values: []any{record.requestHash, record.resultJSON, record.expiresAt}}
			default:
				return assetTestRow{err: pgx.ErrNoRows}
			}
		},
	}
}

type assetTestRow struct {
	values []any
	err    error
}

func (r assetTestRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != len(r.values) {
		return fmt.Errorf("scan dest mismatch: got %d want %d", len(dest), len(r.values))
	}
	for i := range dest {
		if err := assignScanValue(dest[i], r.values[i]); err != nil {
			return err
		}
	}
	return nil
}

func assignScanValue(dest any, value any) error {
	switch d := dest.(type) {
	case *string:
		if value == nil {
			*d = ""
			return nil
		}
		*d = value.(string)
		return nil
	case *int64:
		*d = value.(int64)
		return nil
	case *time.Time:
		*d = value.(time.Time)
		return nil
	case **time.Time:
		if value == nil {
			*d = nil
			return nil
		}
		switch typed := value.(type) {
		case time.Time:
			v := typed
			*d = &v
		case *time.Time:
			*d = typed
		default:
			return fmt.Errorf("unsupported time pointer value %T", value)
		}
		return nil
	case *[]byte:
		switch typed := value.(type) {
		case nil:
			*d = nil
		case []byte:
			*d = append([]byte(nil), typed...)
		case string:
			*d = []byte(typed)
		default:
			return fmt.Errorf("unsupported []byte source %T", value)
		}
		return nil
	default:
		rv := reflect.ValueOf(dest)
		if rv.Kind() != reflect.Ptr || rv.IsNil() {
			return fmt.Errorf("destination must be pointer, got %T", dest)
		}
		val := reflect.ValueOf(value)
		if !val.IsValid() {
			rv.Elem().Set(reflect.Zero(rv.Elem().Type()))
			return nil
		}
		if val.Type().AssignableTo(rv.Elem().Type()) {
			rv.Elem().Set(val)
			return nil
		}
		return fmt.Errorf("unsupported scan destination %T for value %T", dest, value)
	}
}
