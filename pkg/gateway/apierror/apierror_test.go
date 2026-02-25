package apierror

import (
	"context"
	"testing"

	"github.com/vango-go/vai-lite/pkg/core"
)

func TestFromError_ContextCanceled_Is408Cancelled(t *testing.T) {
	ce, status := FromError(context.Canceled, "req_test")
	if status != 408 {
		t.Fatalf("status=%d", status)
	}
	if ce.Type != core.ErrAPI {
		t.Fatalf("type=%q", ce.Type)
	}
	if ce.Code != "cancelled" {
		t.Fatalf("code=%q", ce.Code)
	}
	if ce.RequestID != "req_test" {
		t.Fatalf("request_id=%q", ce.RequestID)
	}
}

func TestFromError_Overloaded_Is529(t *testing.T) {
	ce, status := FromError(&core.Error{Type: core.ErrOverloaded, Message: "overloaded"}, "req_test")
	if status != 529 {
		t.Fatalf("status=%d", status)
	}
	if ce.Type != core.ErrOverloaded {
		t.Fatalf("type=%q", ce.Type)
	}
}
