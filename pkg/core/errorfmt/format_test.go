package errorfmt

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/vango-go/vai-lite/pkg/core"
)

type transportStub struct {
	op  string
	url string
	err error
}

func (e *transportStub) Error() string {
	if e.err == nil {
		return "transport error"
	}
	return fmt.Sprintf("transport error: %v", e.err)
}

func (e *transportStub) Unwrap() error { return e.err }
func (e *transportStub) TransportOp() string {
	return e.op
}
func (e *transportStub) TransportURL() string {
	return e.url
}

func TestFormat_Nil(t *testing.T) {
	if got := Format(nil); got != "" {
		t.Fatalf("Format(nil)=%q, want empty string", got)
	}
}

func TestFormat_CoreErrorFields(t *testing.T) {
	retryAfter := 7
	err := &core.Error{
		Type:          core.ErrAPI,
		Message:       "internal error",
		Param:         "messages[0]",
		Code:          "INTERNAL",
		RequestID:     "req_123",
		RetryAfter:    &retryAfter,
		ProviderError: map[string]any{"status": "RESOURCE_EXHAUSTED", "message": "quota exceeded"},
	}

	got := Format(err)
	wantParts := []string{
		"api_error: internal error",
		"param=messages[0]",
		"code=INTERNAL",
		"request_id=req_123",
		"retry_after=7s",
		`provider_error={"message":"quota exceeded","status":"RESOURCE_EXHAUSTED"}`,
	}
	for _, want := range wantParts {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
}

func TestFormat_UnwrapChainAndDedupe(t *testing.T) {
	root := errors.New("root cause")
	wrapped := fmt.Errorf("layer 1: %w", root)
	err := fmt.Errorf("top level: %w", wrapped)

	got := Format(err)
	if !strings.Contains(got, "top level: layer 1: root cause") {
		t.Fatalf("missing top-level text, got=%q", got)
	}
	if !strings.Contains(got, "caused by: layer 1: root cause") {
		t.Fatalf("missing first cause, got=%q", got)
	}
	if !strings.Contains(got, "caused by: root cause") {
		t.Fatalf("missing root cause, got=%q", got)
	}
	if strings.Count(got, "caused by: root cause") != 1 {
		t.Fatalf("root cause duplicated, got=%q", got)
	}
}

func TestFormat_TransportContext(t *testing.T) {
	err := &transportStub{
		op:  "POST",
		url: "https://example.test/v1/messages",
		err: errors.New("dial tcp timeout"),
	}

	got := Format(err)
	if !strings.Contains(got, "op=POST") {
		t.Fatalf("missing op in %q", got)
	}
	if !strings.Contains(got, "url=https://example.test/v1/messages") {
		t.Fatalf("missing url in %q", got)
	}
}
