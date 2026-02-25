package mw

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAPIVersion_NoHeaderDefaultsToV1(t *testing.T) {
	h := APIVersion(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil).WithContext(WithRequestID(context.Background(), "req_test"))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
	}
}

func TestAPIVersion_SupportedHeaderAccepted(t *testing.T) {
	h := APIVersion(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil).WithContext(WithRequestID(context.Background(), "req_test"))
	req.Header.Set(apiVersionHeader, supportedAPIVersion)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
	}
}

func TestAPIVersion_WhitespaceAndDuplicatesAccepted(t *testing.T) {
	h := APIVersion(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil).WithContext(WithRequestID(context.Background(), "req_test"))
	req.Header.Add(apiVersionHeader, " 1 ")
	req.Header.Add(apiVersionHeader, "1, 1")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
	}
}

func TestAPIVersion_UnsupportedVersionRejected(t *testing.T) {
	h := APIVersion(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil).WithContext(WithRequestID(context.Background(), "req_abc123"))
	req.Header.Set(apiVersionHeader, "2")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, `"type":"invalid_request_error"`) {
		t.Fatalf("missing invalid_request_error type: %q", body)
	}
	if !strings.Contains(body, `"code":"unsupported_version"`) {
		t.Fatalf("missing unsupported_version code: %q", body)
	}
	if !strings.Contains(body, `"param":"X-VAI-Version"`) {
		t.Fatalf("missing X-VAI-Version param: %q", body)
	}
	if !strings.Contains(body, `"request_id":"req_abc123"`) {
		t.Fatalf("missing request_id: %q", body)
	}
}

func TestAPIVersion_MixedVersionsRejected(t *testing.T) {
	h := APIVersion(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil).WithContext(WithRequestID(context.Background(), "req_test"))
	req.Header.Set(apiVersionHeader, "1,2")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"code":"unsupported_version"`) {
		t.Fatalf("missing unsupported_version code: %q", rr.Body.String())
	}
}

func TestAPIVersion_NonAPIV1PathBypass(t *testing.T) {
	h := APIVersion(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil).WithContext(WithRequestID(context.Background(), "req_test"))
	req.Header.Set(apiVersionHeader, "2")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
	}
}

func TestAPIVersion_WebSocketUpgradeBypass(t *testing.T) {
	h := APIVersion(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/live", nil).WithContext(WithRequestID(context.Background(), "req_test"))
	req.Header.Set(apiVersionHeader, "2")
	req.Header.Set("Connection", "keep-alive, Upgrade")
	req.Header.Set("Upgrade", "websocket")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
	}
}

func TestAPIVersion_OptionsBypass(t *testing.T) {
	h := APIVersion(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodOptions, "/v1/messages", nil).WithContext(WithRequestID(context.Background(), "req_test"))
	req.Header.Set(apiVersionHeader, "2")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
	}
}
