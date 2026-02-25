package mw

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vango-go/vai-lite/pkg/gateway/sse"
)

type testBaseWriter struct {
	header      http.Header
	status      int
	wroteHeader bool
	body        bytes.Buffer
}

func newTestBaseWriter() *testBaseWriter {
	return &testBaseWriter{header: make(http.Header)}
}

func (w *testBaseWriter) Header() http.Header {
	return w.header
}

func (w *testBaseWriter) WriteHeader(code int) {
	if w.wroteHeader {
		return
	}
	w.status = code
	w.wroteHeader = true
}

func (w *testBaseWriter) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.body.Write(p)
}

type testFlusherWriter struct {
	*testBaseWriter
	flushed bool
}

func (w *testFlusherWriter) Flush() {
	w.flushed = true
}

type testHijackerWriter struct {
	*testBaseWriter
	hijacked bool
}

func (w *testHijackerWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.hijacked = true
	return nil, nil, nil
}

type testFlusherHijackerWriter struct {
	*testBaseWriter
	flushed  bool
	hijacked bool
}

func (w *testFlusherHijackerWriter) Flush() {
	w.flushed = true
}

func (w *testFlusherHijackerWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.hijacked = true
	return nil, nil, nil
}

func newTestLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(buf, nil))
}

func parseSingleLogRecord(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	line := strings.TrimSpace(buf.String())
	if line == "" {
		t.Fatal("expected log output")
	}
	var rec map[string]any
	if err := json.Unmarshal([]byte(line), &rec); err != nil {
		t.Fatalf("unmarshal log: %v", err)
	}
	return rec
}

func TestAccessLog_PreservesFlusher(t *testing.T) {
	writer := &testFlusherWriter{testBaseWriter: newTestBaseWriter()}
	loggerOut := &bytes.Buffer{}

	h := AccessLog(newTestLogger(loggerOut), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatalf("expected http.Flusher to be preserved")
		}
		flusher.Flush()
		_, _ = w.Write([]byte("ok"))
	}))

	h.ServeHTTP(writer, httptest.NewRequest(http.MethodGet, "/v1/messages", nil).WithContext(WithRequestID(context.Background(), "req_test")))

	if !writer.flushed {
		t.Fatalf("expected underlying flusher to be invoked")
	}
}

func TestAccessLog_PreservesHijacker(t *testing.T) {
	writer := &testHijackerWriter{testBaseWriter: newTestBaseWriter()}
	loggerOut := &bytes.Buffer{}

	h := AccessLog(newTestLogger(loggerOut), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatalf("expected http.Hijacker to be preserved")
		}
		_, _, err := hj.Hijack()
		if err != nil {
			t.Fatalf("hijack failed: %v", err)
		}
	}))

	h.ServeHTTP(writer, httptest.NewRequest(http.MethodGet, "/v1/live", nil).WithContext(WithRequestID(context.Background(), "req_test")))

	if !writer.hijacked {
		t.Fatalf("expected underlying hijacker to be invoked")
	}
}

func TestAccessLog_PreservesFlusherAndHijacker(t *testing.T) {
	writer := &testFlusherHijackerWriter{testBaseWriter: newTestBaseWriter()}
	loggerOut := &bytes.Buffer{}

	h := AccessLog(newTestLogger(loggerOut), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatalf("expected http.Flusher to be preserved")
		}
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatalf("expected http.Hijacker to be preserved")
		}
		flusher.Flush()
		_, _, err := hj.Hijack()
		if err != nil {
			t.Fatalf("hijack failed: %v", err)
		}
	}))

	h.ServeHTTP(writer, httptest.NewRequest(http.MethodGet, "/v1/live", nil).WithContext(WithRequestID(context.Background(), "req_test")))

	if !writer.flushed {
		t.Fatalf("expected flush to be delegated")
	}
	if !writer.hijacked {
		t.Fatalf("expected hijack to be delegated")
	}
}

func TestAccessLog_DoesNotAdvertiseUnsupportedInterfaces(t *testing.T) {
	writer := newTestBaseWriter()
	loggerOut := &bytes.Buffer{}

	h := AccessLog(newTestLogger(loggerOut), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := w.(http.Flusher); ok {
			t.Fatalf("did not expect http.Flusher to be advertised")
		}
		if _, ok := w.(http.Hijacker); ok {
			t.Fatalf("did not expect http.Hijacker to be advertised")
		}
		_, _ = w.Write([]byte("ok"))
	}))

	h.ServeHTTP(writer, httptest.NewRequest(http.MethodGet, "/v1/messages", nil).WithContext(WithRequestID(context.Background(), "req_test")))
}

func TestAccessLog_StatusLogging_ExplicitWriteHeader(t *testing.T) {
	writer := newTestBaseWriter()
	loggerOut := &bytes.Buffer{}

	h := AccessLog(newTestLogger(loggerOut), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))

	h.ServeHTTP(writer, httptest.NewRequest(http.MethodGet, "/healthz", nil).WithContext(WithRequestID(context.Background(), "req_test")))

	rec := parseSingleLogRecord(t, loggerOut)
	if got, ok := rec["status"].(float64); !ok || int(got) != http.StatusCreated {
		t.Fatalf("logged status=%v (type %T), want %d", rec["status"], rec["status"], http.StatusCreated)
	}
}

func TestAccessLog_StatusLogging_ImplicitWriteIs200(t *testing.T) {
	writer := newTestBaseWriter()
	loggerOut := &bytes.Buffer{}

	h := AccessLog(newTestLogger(loggerOut), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))

	h.ServeHTTP(writer, httptest.NewRequest(http.MethodGet, "/healthz", nil).WithContext(WithRequestID(context.Background(), "req_test")))

	rec := parseSingleLogRecord(t, loggerOut)
	if got, ok := rec["status"].(float64); !ok || int(got) != http.StatusOK {
		t.Fatalf("logged status=%v (type %T), want %d", rec["status"], rec["status"], http.StatusOK)
	}
}

func TestAccessLog_SSEMiddlewareRegression(t *testing.T) {
	writer := &testFlusherWriter{testBaseWriter: newTestBaseWriter()}
	loggerOut := &bytes.Buffer{}

	h := AccessLog(newTestLogger(loggerOut), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw, err := sse.New(w)
		if err != nil {
			t.Fatalf("expected sse.New to succeed behind AccessLog middleware: %v", err)
		}
		if err := sw.Send("ping", map[string]string{"type": "ping"}); err != nil {
			t.Fatalf("sse send failed: %v", err)
		}
	}))

	h.ServeHTTP(writer, httptest.NewRequest(http.MethodGet, "/v1/messages", nil).WithContext(WithRequestID(context.Background(), "req_test")))

	body := writer.body.String()
	if !strings.Contains(body, "event: ping\n") {
		t.Fatalf("expected SSE event line in output, got %q", body)
	}
	if !strings.Contains(body, "\"type\":\"ping\"") {
		t.Fatalf("expected SSE JSON payload in output, got %q", body)
	}
	if !writer.flushed {
		t.Fatalf("expected SSE send to flush underlying writer")
	}
}
