package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/vango-go/vai-lite/pkg/core"
	"github.com/vango-go/vai-lite/pkg/core/types"
	"github.com/vango-go/vai-lite/pkg/gateway/apierror"
)

func coreErrorFrom(err error, reqID string) (*core.Error, int) {
	return apierror.FromError(err, reqID)
}

func toTypesError(coreErr *core.Error) types.Error {
	if coreErr == nil {
		return types.Error{Type: string(core.ErrAPI), Message: "internal error"}
	}
	return types.Error{
		Type:          string(coreErr.Type),
		Message:       coreErr.Message,
		Param:         coreErr.Param,
		Code:          coreErr.Code,
		RequestID:     coreErr.RequestID,
		ProviderError: coreErr.ProviderError,
		RetryAfter:    coreErr.RetryAfter,
		CompatIssues:  compatIssuesToTypes(coreErr.CompatIssues),
	}
}

func writeCoreErrorJSON(w http.ResponseWriter, reqID string, coreErr *core.Error, status int) {
	if coreErr != nil && coreErr.RequestID == "" {
		coreErr.RequestID = reqID
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(apierror.Envelope{Error: coreErr})
}

func compatIssuesToTypes(issues []core.CompatibilityIssue) []types.CompatibilityIssue {
	if len(issues) == 0 {
		return nil
	}
	out := make([]types.CompatibilityIssue, len(issues))
	for i := range issues {
		out[i] = types.CompatibilityIssue{
			Severity: issues[i].Severity,
			Param:    issues[i].Param,
			Code:     issues[i].Code,
			Message:  issues[i].Message,
		}
	}
	return out
}
