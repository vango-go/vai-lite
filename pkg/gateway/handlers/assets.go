package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/vango-go/vai-lite/pkg/core"
	"github.com/vango-go/vai-lite/pkg/core/types"
	assetsvc "github.com/vango-go/vai-lite/pkg/gateway/assets"
	"github.com/vango-go/vai-lite/pkg/gateway/config"
)

type AssetsHandler struct {
	Config config.Config
	Logger *slog.Logger
	Assets *assetsvc.Service
}

func (h AssetsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/v1/assets:upload-intent":
		if r.Method != http.MethodPost {
			writeCoreErrorJSON(w, requestID(r), &core.Error{Type: core.ErrInvalidRequest, Message: "method not allowed", Code: "method_not_allowed", RequestID: requestID(r)}, http.StatusMethodNotAllowed)
			return
		}
		h.createUploadIntent(w, r)
	case r.URL.Path == "/v1/assets:claim":
		if r.Method != http.MethodPost {
			writeCoreErrorJSON(w, requestID(r), &core.Error{Type: core.ErrInvalidRequest, Message: "method not allowed", Code: "method_not_allowed", RequestID: requestID(r)}, http.StatusMethodNotAllowed)
			return
		}
		h.claimAsset(w, r)
	case strings.HasPrefix(r.URL.Path, "/v1/assets/"):
		h.serveAssetPath(w, r)
	default:
		NotFoundHandler{}.ServeHTTP(w, r)
	}
}

func (h AssetsHandler) serveAssetPath(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/assets/"), "/")
	if path == "" {
		NotFoundHandler{}.ServeHTTP(w, r)
		return
	}
	switch {
	case strings.HasSuffix(path, ":sign"):
		if r.Method != http.MethodPost {
			writeCoreErrorJSON(w, requestID(r), &core.Error{Type: core.ErrInvalidRequest, Message: "method not allowed", Code: "method_not_allowed", RequestID: requestID(r)}, http.StatusMethodNotAllowed)
			return
		}
		assetID := strings.TrimSpace(strings.TrimSuffix(path, ":sign"))
		if assetID == "" {
			NotFoundHandler{}.ServeHTTP(w, r)
			return
		}
		h.signAsset(w, r, assetID)
	default:
		if r.Method != http.MethodGet {
			writeCoreErrorJSON(w, requestID(r), &core.Error{Type: core.ErrInvalidRequest, Message: "method not allowed", Code: "method_not_allowed", RequestID: requestID(r)}, http.StatusMethodNotAllowed)
			return
		}
		h.getAsset(w, r, strings.TrimSpace(path))
	}
}

func (h AssetsHandler) createUploadIntent(w http.ResponseWriter, r *http.Request) {
	if h.Assets == nil {
		writeCoreErrorJSON(w, requestID(r), core.NewAPIError("gateway asset storage is not configured"), http.StatusServiceUnavailable)
		return
	}
	body, ok := readBoundedBody(w, r, h.Config.MaxBodyBytes)
	if !ok {
		return
	}
	var req types.AssetUploadIntentRequest
	if err := decodeAssetRequest(body, &req); err != nil {
		h.writeAssetError(w, requestID(r), err)
		return
	}
	pr := chainPrincipalFromRequest(r, h.Config)
	resp, err := h.Assets.CreateUploadIntent(r.Context(), assetsvc.Principal{
		OrgID:       pr.OrgID,
		PrincipalID: pr.PrincipalID,
	}, req, strings.TrimSpace(r.Header.Get(idempotencyKeyHeader)))
	if err != nil {
		h.writeAssetError(w, requestID(r), err)
		return
	}
	writeJSON(w, resp)
}

func (h AssetsHandler) claimAsset(w http.ResponseWriter, r *http.Request) {
	if h.Assets == nil {
		writeCoreErrorJSON(w, requestID(r), core.NewAPIError("gateway asset storage is not configured"), http.StatusServiceUnavailable)
		return
	}
	body, ok := readBoundedBody(w, r, h.Config.MaxBodyBytes)
	if !ok {
		return
	}
	var req types.AssetClaimRequest
	if err := decodeAssetRequest(body, &req); err != nil {
		h.writeAssetError(w, requestID(r), err)
		return
	}
	pr := chainPrincipalFromRequest(r, h.Config)
	resp, err := h.Assets.ClaimAsset(r.Context(), assetsvc.Principal{
		OrgID:       pr.OrgID,
		PrincipalID: pr.PrincipalID,
	}, req, strings.TrimSpace(r.Header.Get(idempotencyKeyHeader)))
	if err != nil {
		h.writeAssetError(w, requestID(r), err)
		return
	}
	writeJSON(w, resp)
}

func (h AssetsHandler) getAsset(w http.ResponseWriter, r *http.Request, assetID string) {
	if h.Assets == nil {
		writeCoreErrorJSON(w, requestID(r), core.NewAPIError("gateway asset storage is not configured"), http.StatusServiceUnavailable)
		return
	}
	pr := chainPrincipalFromRequest(r, h.Config)
	resp, err := h.Assets.GetAsset(r.Context(), pr.OrgID, assetID)
	if err != nil {
		h.writeAssetError(w, requestID(r), err)
		return
	}
	writeJSON(w, resp)
}

func (h AssetsHandler) signAsset(w http.ResponseWriter, r *http.Request, assetID string) {
	if h.Assets == nil {
		writeCoreErrorJSON(w, requestID(r), core.NewAPIError("gateway asset storage is not configured"), http.StatusServiceUnavailable)
		return
	}
	pr := chainPrincipalFromRequest(r, h.Config)
	resp, err := h.Assets.SignAsset(r.Context(), pr.OrgID, assetID)
	if err != nil {
		h.writeAssetError(w, requestID(r), err)
		return
	}
	writeJSON(w, resp)
}

func (h AssetsHandler) writeAssetError(w http.ResponseWriter, reqID string, err error) {
	if err == nil {
		err = core.NewAPIError("internal error")
	}
	var coreErr *core.Error
	if errors.As(err, &coreErr) {
		status := http.StatusInternalServerError
		switch coreErr.Type {
		case core.ErrInvalidRequest:
			status = http.StatusBadRequest
			if coreErr.Code == string(types.ErrorCodeAssetPayloadTooLarge) {
				status = http.StatusRequestEntityTooLarge
			}
		case core.ErrAuthentication:
			status = http.StatusUnauthorized
		case core.ErrPermission:
			status = http.StatusForbidden
		case core.ErrNotFound:
			status = http.StatusNotFound
		}
		writeCoreErrorJSON(w, reqID, coreErr, status)
		return
	}
	if h.Logger != nil {
		h.Logger.Error("asset handler error", "request_id", reqID, "error", err)
	}
	coreErr, status := coreErrorFrom(err, reqID)
	writeCoreErrorJSON(w, reqID, coreErr, status)
}

func decodeAssetRequest(body []byte, target any) error {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return core.NewInvalidRequestError("invalid request body")
	}
	return nil
}
