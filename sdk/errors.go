package vai

import (
	"fmt"

	"github.com/vango-go/vai-lite/pkg/core"
)

// SDK-level error type that wraps core errors
type Error = core.Error

// Error types
const (
	ErrInvalidRequest = core.ErrInvalidRequest
	ErrAuthentication = core.ErrAuthentication
	ErrPermission     = core.ErrPermission
	ErrNotFound       = core.ErrNotFound
	ErrRateLimit      = core.ErrRateLimit
	ErrAPI            = core.ErrAPI
	ErrOverloaded     = core.ErrOverloaded
	ErrProvider       = core.ErrProvider
)

// Error constructors
var (
	NewInvalidRequestError = core.NewInvalidRequestError
	NewAuthenticationError = core.NewAuthenticationError
	NewRateLimitError      = core.NewRateLimitError
	NewProviderError       = core.NewProviderError
)

// TransportError represents HTTP transport-level failures (DNS, timeouts,
// connection reset, TLS handshake, etc.) while talking to the gateway.
//
// Use errors.As(err, &TransportError{}) to distinguish transport failures
// from canonical API errors (*core.Error).
type TransportError struct {
	Op  string
	URL string
	Err error
}

func (e *TransportError) Error() string {
	switch {
	case e == nil:
		return ""
	case e.Op != "" && e.URL != "":
		return fmt.Sprintf("transport error during %s %s: %v", e.Op, e.URL, e.Err)
	case e.Op != "":
		return fmt.Sprintf("transport error during %s: %v", e.Op, e.Err)
	default:
		return fmt.Sprintf("transport error: %v", e.Err)
	}
}

func (e *TransportError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}
