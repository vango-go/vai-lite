package vai

import (
	"errors"
	"strings"
	"testing"

	"github.com/vango-go/vai-lite/pkg/core"
)

func TestFormatError_IncludesTransportContext(t *testing.T) {
	err := &TransportError{
		Op:  "POST",
		URL: "https://gateway.test/v1/messages",
		Err: errors.New("dial tcp timeout"),
	}

	got := FormatError(err)
	if !strings.Contains(got, "op=POST") {
		t.Fatalf("missing op in %q", got)
	}
	if !strings.Contains(got, "url=https://gateway.test/v1/messages") {
		t.Fatalf("missing url in %q", got)
	}
}

func TestFormatError_CoreErrorParity(t *testing.T) {
	retryAfter := 3
	err := &core.Error{
		Type:          core.ErrRateLimit,
		Message:       "too many requests",
		Param:         "messages[0]",
		Code:          "RATE_LIMIT",
		RequestID:     "req_1",
		RetryAfter:    &retryAfter,
		ProviderError: map[string]any{"status": "RESOURCE_EXHAUSTED"},
	}

	got := FormatError(err)
	wantParts := []string{
		"rate_limit_error: too many requests",
		"param=messages[0]",
		"code=RATE_LIMIT",
		"request_id=req_1",
		"retry_after=3s",
		`provider_error={"status":"RESOURCE_EXHAUSTED"}`,
	}
	for _, want := range wantParts {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
}
