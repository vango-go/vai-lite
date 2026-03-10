package types

import (
	"fmt"
	"net/http"
)

// ErrorCode is the transport-independent canonical error code for chain APIs.
type ErrorCode string

const (
	ErrorCodeAuthCredentialScopeDenied     ErrorCode = "auth.credential_scope_denied"
	ErrorCodeAuthResumeTokenInvalid        ErrorCode = "auth.resume_token_invalid"
	ErrorCodeAuthActorScopeDenied          ErrorCode = "auth.actor_scope_denied"
	ErrorCodeAuthChainAccessDenied         ErrorCode = "auth.chain_access_denied"
	ErrorCodeChainAttachConflict           ErrorCode = "chain.attach_conflict"
	ErrorCodeChainReplayUnavailable        ErrorCode = "chain.replay_unavailable"
	ErrorCodeChainExpired                  ErrorCode = "chain.expired"
	ErrorCodeToolTimeout                   ErrorCode = "tool.timeout"
	ErrorCodeToolResultConflict            ErrorCode = "tool.result_conflict"
	ErrorCodeTransportClientToolsRequireWS ErrorCode = "transport.client_tools_require_websocket"
	ErrorCodeTransportBackpressureExceeded ErrorCode = "transport.backpressure_exceeded"
	ErrorCodeProtocolUnsupportedCapability ErrorCode = "protocol.unsupported_capability"
	ErrorCodeProtocolUnknownFrame          ErrorCode = "protocol.unknown_frame_type"
	ErrorCodeProtocolUnknownEvent          ErrorCode = "protocol.unknown_event_type"
	ErrorCodeQuotaRateLimited              ErrorCode = "quota.rate_limited"
	ErrorCodeQuotaLimitExceeded            ErrorCode = "quota.limit_exceeded"
	ErrorCodeAssetPayloadTooLarge          ErrorCode = "asset.payload_too_large"
)

type SuggestedAction string

const (
	SuggestedActionReconnectWithCredentials SuggestedAction = "reconnect_with_credentials"
	SuggestedActionRetryWithTakeover        SuggestedAction = "retry_with_takeover"
	SuggestedActionRecoverFromHistory       SuggestedAction = "recover_from_history"
	SuggestedActionSwitchToWebSocket        SuggestedAction = "switch_to_websocket"
	SuggestedActionStartNewChain            SuggestedAction = "start_new_chain"
	SuggestedActionRetryLater               SuggestedAction = "retry_later"
	SuggestedActionReducePayload            SuggestedAction = "reduce_payload"
	SuggestedActionReduceRate               SuggestedAction = "reduce_rate"
)

// CanonicalError is the stable chain/runtime error envelope shared across HTTP and WS.
type CanonicalError struct {
	Code            ErrorCode       `json:"code"`
	Message         string          `json:"message"`
	Retryable       bool            `json:"retryable"`
	Fatal           bool            `json:"fatal"`
	SuggestedAction SuggestedAction `json:"suggested_action,omitempty"`
	ChainID         string          `json:"chain_id,omitempty"`
	RunID           string          `json:"run_id,omitempty"`
	ExecutionID     string          `json:"execution_id,omitempty"`
	RetryAfter      *int            `json:"retry_after,omitempty"`
	Details         map[string]any  `json:"details,omitempty"`
}

func (e *CanonicalError) Error() string {
	if e == nil {
		return ""
	}
	if e.Code == "" {
		return e.Message
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

type CanonicalErrorEnvelope struct {
	Error *CanonicalError `json:"error"`
}

func NewCanonicalError(code ErrorCode, message string) *CanonicalError {
	return &CanonicalError{
		Code:      code,
		Message:   message,
		Retryable: false,
		Fatal:     false,
	}
}

func (e *CanonicalError) WithChain(chainID string) *CanonicalError {
	if e == nil {
		return nil
	}
	clone := *e
	clone.ChainID = chainID
	return &clone
}

func (e *CanonicalError) WithRun(runID string) *CanonicalError {
	if e == nil {
		return nil
	}
	clone := *e
	clone.RunID = runID
	return &clone
}

func (e *CanonicalError) WithExecution(executionID string) *CanonicalError {
	if e == nil {
		return nil
	}
	clone := *e
	clone.ExecutionID = executionID
	return &clone
}

func HTTPStatusForCanonicalError(code ErrorCode) int {
	switch code {
	case ErrorCodeAuthCredentialScopeDenied:
		return http.StatusForbidden
	case ErrorCodeAuthResumeTokenInvalid:
		return http.StatusUnauthorized
	case ErrorCodeAuthActorScopeDenied, ErrorCodeAuthChainAccessDenied:
		return http.StatusForbidden
	case ErrorCodeChainAttachConflict:
		return http.StatusConflict
	case ErrorCodeChainReplayUnavailable:
		return http.StatusPreconditionFailed
	case ErrorCodeChainExpired:
		return http.StatusGone
	case ErrorCodeToolTimeout:
		return http.StatusGatewayTimeout
	case ErrorCodeToolResultConflict:
		return http.StatusConflict
	case ErrorCodeTransportClientToolsRequireWS, ErrorCodeProtocolUnsupportedCapability:
		return http.StatusUnprocessableEntity
	case ErrorCodeQuotaRateLimited, ErrorCodeQuotaLimitExceeded:
		return http.StatusTooManyRequests
	case ErrorCodeAssetPayloadTooLarge:
		return http.StatusRequestEntityTooLarge
	default:
		return http.StatusBadRequest
	}
}
