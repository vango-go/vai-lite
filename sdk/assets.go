package vai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"

	"github.com/vango-go/vai-lite/pkg/core"
	"github.com/vango-go/vai-lite/pkg/core/types"
)

type AssetsService struct {
	client *Client
}

func (s *AssetsService) CreateUploadIntent(ctx context.Context, req *types.AssetUploadIntentRequest) (*types.AssetUploadIntentResponse, error) {
	if s == nil || s.client == nil {
		return nil, core.NewInvalidRequestError("client is not configured")
	}
	if req == nil {
		return nil, core.NewInvalidRequestError("request is required")
	}
	headers := s.client.buildChainHeaders()
	headers.Set(chainSDKIdempotencyHeader, newIdempotencyKey("asset_upload"))
	resp, endpoint, err := s.client.postGatewayJSON(ctx, "/v1/assets:upload-intent", req, headers)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, decodeGatewayErrorResponse(resp, endpoint, http.MethodPost)
	}
	var out types.AssetUploadIntentResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, core.NewAPIError("failed to decode asset upload intent response")
	}
	return &out, nil
}

func (s *AssetsService) Claim(ctx context.Context, req *types.AssetClaimRequest) (*types.AssetRecord, error) {
	if s == nil || s.client == nil {
		return nil, core.NewInvalidRequestError("client is not configured")
	}
	if req == nil {
		return nil, core.NewInvalidRequestError("request is required")
	}
	headers := s.client.buildChainHeaders()
	headers.Set(chainSDKIdempotencyHeader, newIdempotencyKey("asset_claim"))
	resp, endpoint, err := s.client.postGatewayJSON(ctx, "/v1/assets:claim", req, headers)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, decodeGatewayErrorResponse(resp, endpoint, http.MethodPost)
	}
	var out types.AssetRecord
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, core.NewAPIError("failed to decode asset claim response")
	}
	return &out, nil
}

func (s *AssetsService) Get(ctx context.Context, assetID string) (*types.AssetRecord, error) {
	if s == nil || s.client == nil {
		return nil, core.NewInvalidRequestError("client is not configured")
	}
	endpoint, err := s.client.gatewayEndpoint("/v1/assets/" + url.PathEscape(assetID))
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, &TransportError{Op: http.MethodGet, URL: endpoint, Err: err}
	}
	for key, values := range s.client.buildChainHeaders() {
		for _, value := range values {
			httpReq.Header.Add(key, value)
		}
	}
	resp, err := s.client.httpClient.Do(httpReq)
	if err != nil {
		return nil, &TransportError{Op: http.MethodGet, URL: endpoint, Err: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, decodeGatewayErrorResponse(resp, endpoint, http.MethodGet)
	}
	var out types.AssetRecord
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, core.NewAPIError("failed to decode asset record")
	}
	return &out, nil
}

func (s *AssetsService) Sign(ctx context.Context, assetID string) (*types.AssetSignResponse, error) {
	if s == nil || s.client == nil {
		return nil, core.NewInvalidRequestError("client is not configured")
	}
	endpoint, err := s.client.gatewayEndpoint("/v1/assets/" + url.PathEscape(assetID) + ":sign")
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return nil, &TransportError{Op: http.MethodPost, URL: endpoint, Err: err}
	}
	for key, values := range s.client.buildChainHeaders() {
		for _, value := range values {
			httpReq.Header.Add(key, value)
		}
	}
	httpReq.Header.Set(chainSDKIdempotencyHeader, newIdempotencyKey("asset_sign"))
	resp, err := s.client.httpClient.Do(httpReq)
	if err != nil {
		return nil, &TransportError{Op: http.MethodPost, URL: endpoint, Err: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, decodeGatewayErrorResponse(resp, endpoint, http.MethodPost)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, &TransportError{Op: http.MethodPost, URL: endpoint, Err: err}
	}
	var out types.AssetSignResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, core.NewAPIError("failed to decode asset sign response")
	}
	return &out, nil
}
