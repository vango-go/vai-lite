package errorfmt

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/vango-go/vai-lite/pkg/core"
)

type transportContext interface {
	TransportOp() string
	TransportURL() string
}

// Format returns a rich human-readable string for errors returned by the SDK
// and gateway APIs while preserving the canonical error.Error() text.
func Format(err error) string {
	if err == nil {
		return ""
	}

	message := err.Error()
	details := make([]string, 0, 7)

	var transportErr transportContext
	if errors.As(err, &transportErr) && transportErr != nil {
		if op := strings.TrimSpace(transportErr.TransportOp()); op != "" {
			details = append(details, "op="+op)
		}
		if rawURL := strings.TrimSpace(transportErr.TransportURL()); rawURL != "" {
			details = append(details, "url="+rawURL)
		}
	}

	var coreErr *core.Error
	if errors.As(err, &coreErr) && coreErr != nil {
		if coreErr.Param != "" {
			details = append(details, "param="+coreErr.Param)
		}
		if coreErr.Code != "" {
			details = append(details, "code="+coreErr.Code)
		}
		if coreErr.RequestID != "" {
			details = append(details, "request_id="+coreErr.RequestID)
		}
		if coreErr.RetryAfter != nil {
			details = append(details, fmt.Sprintf("retry_after=%ds", *coreErr.RetryAfter))
		}
		if coreErr.ProviderError != nil {
			details = append(details, "provider_error="+compactJSON(coreErr.ProviderError))
		}
	}

	if len(details) > 0 {
		message += " [" + strings.Join(details, " ") + "]"
	}

	seen := map[string]struct{}{err.Error(): {}}
	for cause := errors.Unwrap(err); cause != nil; cause = errors.Unwrap(cause) {
		causeText := cause.Error()
		if _, exists := seen[causeText]; exists {
			continue
		}
		seen[causeText] = struct{}{}
		message += "\ncaused by: " + causeText
	}

	return message
}

func compactJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}
