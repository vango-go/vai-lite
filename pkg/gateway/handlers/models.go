package handlers

import (
	"encoding/json"
	"net/http"
	"sort"

	"github.com/vango-go/vai-lite/pkg/core"
	"github.com/vango-go/vai-lite/pkg/gateway/apierror"
	"github.com/vango-go/vai-lite/pkg/gateway/compat"
	"github.com/vango-go/vai-lite/pkg/gateway/config"
	"github.com/vango-go/vai-lite/pkg/gateway/mw"
)

type ModelsHandler struct {
	Config config.Config
}

type modelsResponse struct {
	Models []modelInfo `json:"models"`
}

type modelInfo struct {
	ID           string             `json:"id"`
	Provider     string             `json:"provider"`
	Name         string             `json:"name"`
	Auth         *modelAuth         `json:"auth,omitempty"`
	Capabilities *modelCapabilities `json:"capabilities,omitempty"`
}

type modelAuth struct {
	RequiresBYOKHeader string `json:"requires_byok_header,omitempty"`
}

type modelCapabilities struct {
	Streaming        *bool `json:"streaming,omitempty"`
	Tools            *bool `json:"tools,omitempty"`
	NativeWebSearch  *bool `json:"native_web_search,omitempty"`
	NativeCodeExec   *bool `json:"native_code_execution,omitempty"`
	Vision           *bool `json:"vision,omitempty"`
	Documents        *bool `json:"documents,omitempty"`
	StructuredOutput *bool `json:"structured_output,omitempty"`
	Thinking         *bool `json:"thinking,omitempty"`
}

func (h ModelsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		reqID, _ := mw.RequestIDFrom(r.Context())
		h.writeErrorJSON(w, reqID, &core.Error{
			Type:      core.ErrInvalidRequest,
			Message:   "method not allowed",
			Code:      "method_not_allowed",
			RequestID: reqID,
		}, http.StatusMethodNotAllowed)
		return
	}

	if len(h.Config.ModelAllowlist) > 0 {
		w.Header().Set("Cache-Control", "private, max-age=300")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=300")
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	_ = json.NewEncoder(w).Encode(modelsResponse{Models: h.listModels()})
}

func (h ModelsHandler) listModels() []modelInfo {
	if len(h.Config.ModelAllowlist) == 0 {
		catalog := compat.CatalogModels()
		out := make([]modelInfo, 0, len(catalog))
		for _, model := range catalog {
			out = append(out, catalogModelToResponse(model))
		}
		return out
	}

	keys := make([]string, 0, len(h.Config.ModelAllowlist))
	for id := range h.Config.ModelAllowlist {
		keys = append(keys, id)
	}
	sort.Strings(keys)

	out := make([]modelInfo, 0, len(keys))
	for _, id := range keys {
		if model, ok := compat.LookupModel(id); ok {
			out = append(out, catalogModelToResponse(model))
			continue
		}

		provider, name, ok := compat.SplitModelID(id)
		if !ok {
			provider = id
			name = id
		}

		entry := modelInfo{
			ID:           id,
			Provider:     provider,
			Name:         name,
			Capabilities: &modelCapabilities{},
		}
		if header, ok := compat.ProviderKeyHeader(provider); ok {
			entry.Auth = &modelAuth{RequiresBYOKHeader: header}
		}
		out = append(out, entry)
	}

	return out
}

func catalogModelToResponse(model compat.CatalogModel) modelInfo {
	entry := modelInfo{
		ID:           model.ID,
		Provider:     model.Provider,
		Name:         model.Name,
		Capabilities: &modelCapabilities{},
	}
	if header, ok := compat.ProviderKeyHeader(model.Provider); ok {
		entry.Auth = &modelAuth{RequiresBYOKHeader: header}
	}

	if v, ok := supportToBool(model.Capabilities.Streaming); ok {
		entry.Capabilities.Streaming = &v
	}
	if v, ok := supportToBool(model.Capabilities.Tools); ok {
		entry.Capabilities.Tools = &v
	}
	if v, ok := supportToBool(model.Capabilities.NativeWebSearch); ok {
		entry.Capabilities.NativeWebSearch = &v
	}
	if v, ok := supportToBool(model.Capabilities.NativeCodeExec); ok {
		entry.Capabilities.NativeCodeExec = &v
	}
	if v, ok := supportToBool(model.Capabilities.Vision); ok {
		entry.Capabilities.Vision = &v
	}
	if v, ok := supportToBool(model.Capabilities.Documents); ok {
		entry.Capabilities.Documents = &v
	}
	if v, ok := supportToBool(model.Capabilities.StructuredOutput); ok {
		entry.Capabilities.StructuredOutput = &v
	}
	if v, ok := supportToBool(model.Capabilities.Thinking); ok {
		entry.Capabilities.Thinking = &v
	}

	return entry
}

func supportToBool(v compat.Support) (bool, bool) {
	switch v {
	case compat.SupportSupported:
		return true, true
	case compat.SupportUnsupported:
		return false, true
	default:
		return false, false
	}
}

func (h ModelsHandler) writeErrorJSON(w http.ResponseWriter, reqID string, coreErr *core.Error, status int) {
	if coreErr != nil && coreErr.RequestID == "" {
		coreErr.RequestID = reqID
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(apierror.Envelope{Error: coreErr})
}
