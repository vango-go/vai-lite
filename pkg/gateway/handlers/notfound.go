package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/vango-go/vai-lite/pkg/core"
	"github.com/vango-go/vai-lite/pkg/gateway/apierror"
	"github.com/vango-go/vai-lite/pkg/gateway/mw"
)

type NotFoundHandler struct{}

func (h NotFoundHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	reqID, _ := mw.RequestIDFrom(r.Context())
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	_ = json.NewEncoder(w).Encode(apierror.Envelope{Error: &core.Error{
		Type:      core.ErrNotFound,
		Message:   "not found",
		RequestID: reqID,
	}})
}
