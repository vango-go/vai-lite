package chatruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/vango-go/vai-lite/internal/services"
	"github.com/vango-go/vai-lite/pkg/core/types"
	"github.com/vango-go/vai-lite/pkg/gateway/tools/servertools"
	s3store "github.com/vango-go/vango-s3"
)

type SSEEvent struct {
	Event string
	Data  string
}

type GatewayError struct {
	StatusCode int
	Message    string
	Body       []byte
}

func (e *GatewayError) Error() string {
	if e == nil {
		return "gateway request failed"
	}
	message := strings.TrimSpace(e.Message)
	if message == "" {
		message = strings.TrimSpace(http.StatusText(e.StatusCode))
	}
	if message == "" {
		message = "gateway request failed"
	}
	if e.StatusCode <= 0 {
		return message
	}
	return fmt.Sprintf("%s (%d)", message, e.StatusCode)
}

type StreamRunResult struct {
	StatusCode int
	Raw        []byte
	Result     *types.RunResult
}

func BuildConversationRunRequest(ctx context.Context, blobStore s3store.Store, detail *services.ConversationDetail, resolvedHeaders http.Header) (*types.RunRequest, error) {
	messages := make([]types.Message, 0, len(detail.Messages))
	for _, msg := range detail.Messages {
		blocks := make([]types.ContentBlock, 0, len(msg.Attachments)+1)
		if strings.TrimSpace(msg.BodyText) != "" {
			blocks = append(blocks, types.TextBlock{Type: "text", Text: msg.BodyText})
		}
		for _, att := range msg.Attachments {
			signedURL := ""
			if blobStore != nil {
				url, err := blobStore.PresignGet(ctx, att.BlobRef.Key, 30*time.Minute)
				if err != nil {
					return nil, err
				}
				signedURL = url
			}
			if signedURL != "" {
				blocks = append(blocks, types.ImageBlock{
					Type: "image",
					Source: types.ImageSource{
						Type: "url",
						URL:  signedURL,
					},
				})
			}
		}

		switch {
		case len(blocks) == 0:
			messages = append(messages, types.Message{Role: msg.Role, Content: msg.BodyText})
		case len(blocks) == 1 && len(msg.Attachments) == 0:
			if text, ok := blocks[0].(types.TextBlock); ok {
				messages = append(messages, types.Message{Role: msg.Role, Content: text.Text})
				continue
			}
			messages = append(messages, types.Message{Role: msg.Role, Content: blocks})
		default:
			messages = append(messages, types.Message{Role: msg.Role, Content: blocks})
		}
	}

	serverToolsEnabled, serverToolConfig := ConversationServerTools(resolvedHeaders)

	return &types.RunRequest{
		Request: types.MessageRequest{
			Model:     detail.Conversation.Model,
			Messages:  messages,
			MaxTokens: 8192,
		},
		Run: types.RunConfig{
			MaxTurns:      8,
			MaxToolCalls:  8,
			TimeoutMS:     5 * 60 * 1000,
			ParallelTools: true,
			ToolTimeoutMS: 30 * 1000,
		},
		ServerTools:      serverToolsEnabled,
		ServerToolConfig: serverToolConfig,
	}, nil
}

func ConversationServerTools(headers http.Header) ([]string, map[string]any) {
	if headers == nil {
		return nil, nil
	}

	tools := make([]string, 0, 2)
	config := make(map[string]any, 2)

	if provider := preferredWebSearchProvider(headers); provider != "" {
		tools = append(tools, servertools.ToolWebSearch)
		config[servertools.ToolWebSearch] = map[string]any{
			"provider": provider,
		}
	}
	if provider := preferredWebFetchProvider(headers); provider != "" {
		tools = append(tools, servertools.ToolWebFetch)
		config[servertools.ToolWebFetch] = map[string]any{
			"provider": provider,
			"format":   "markdown",
		}
	}

	if len(tools) == 0 {
		return nil, nil
	}
	return tools, config
}

func StreamConversationRun(ctx context.Context, gateway http.Handler, runReq *types.RunRequest, headers http.Header, onEvent func(SSEEvent)) (*StreamRunResult, error) {
	if gateway == nil {
		return nil, errors.New("gateway handler is not configured")
	}
	body, err := json.Marshal(runReq)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "/v1/runs:stream", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header = cloneHeaders(headers)
	req.Header.Set("Content-Type", "application/json")

	writer := newStreamCaptureWriter(onEvent)
	gateway.ServeHTTP(writer, req)

	raw := append([]byte(nil), writer.Body()...)
	if writer.StatusCode() >= http.StatusBadRequest {
		return nil, &GatewayError{
			StatusCode: writer.StatusCode(),
			Message:    ExtractGatewayErrorMessage(raw),
			Body:       raw,
		}
	}

	result, err := ExtractRunResult(raw)
	if err != nil {
		return nil, err
	}
	return &StreamRunResult{
		StatusCode: writer.StatusCode(),
		Raw:        raw,
		Result:     result,
	}, nil
}

func ExtractRunResult(raw []byte) (*types.RunResult, error) {
	events := ParseSSE(raw)
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Event != "run_complete" {
			continue
		}
		ev, err := types.UnmarshalRunStreamEvent([]byte(events[i].Data))
		if err != nil {
			return nil, err
		}
		complete, ok := ev.(types.RunCompleteEvent)
		if ok {
			return complete.Result, nil
		}
	}
	return nil, nil
}

func ExtractRunErrorMessage(raw []byte) (string, error) {
	events := ParseSSE(raw)
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Event != "error" {
			continue
		}
		ev, err := types.UnmarshalRunStreamEvent([]byte(events[i].Data))
		if err != nil {
			return "", err
		}
		runErr, ok := ev.(types.RunErrorEvent)
		if ok {
			return strings.TrimSpace(runErr.Error.Message), nil
		}
	}
	return "", nil
}

func ParseSSE(raw []byte) []SSEEvent {
	chunks := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n\n")
	events := make([]SSEEvent, 0, len(chunks))
	for _, chunk := range chunks {
		chunk = strings.TrimSpace(chunk)
		if chunk == "" {
			continue
		}
		if event, ok := parseSSEChunk(chunk); ok {
			events = append(events, event)
		}
	}
	return events
}

func ExtractGatewayErrorMessage(raw []byte) string {
	var env struct {
		Error any `json:"error"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return ""
	}
	switch value := env.Error.(type) {
	case map[string]any:
		if message, _ := value["message"].(string); strings.TrimSpace(message) != "" {
			return strings.TrimSpace(message)
		}
	case string:
		return strings.TrimSpace(value)
	}
	return ""
}

func LastUserMessageID(messages []services.ConversationMessage) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			return messages[i].ID
		}
	}
	return ""
}

func preferredWebSearchProvider(headers http.Header) string {
	switch {
	case strings.TrimSpace(headers.Get(servertools.HeaderProviderKeyTavily)) != "":
		return servertools.ProviderTavily
	case strings.TrimSpace(headers.Get(servertools.HeaderProviderKeyExa)) != "":
		return servertools.ProviderExa
	default:
		return ""
	}
}

func preferredWebFetchProvider(headers http.Header) string {
	switch {
	case strings.TrimSpace(headers.Get(servertools.HeaderProviderKeyFirecrawl)) != "":
		return servertools.ProviderFirecrawl
	case strings.TrimSpace(headers.Get(servertools.HeaderProviderKeyTavily)) != "":
		return servertools.ProviderTavily
	default:
		return ""
	}
}

func parseSSEChunk(chunk string) (SSEEvent, bool) {
	var eventName string
	var dataLines []string
	for _, line := range strings.Split(chunk, "\n") {
		switch {
		case strings.HasPrefix(line, "event:"):
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if eventName == "" {
		return SSEEvent{}, false
	}
	return SSEEvent{Event: eventName, Data: strings.Join(dataLines, "\n")}, true
}

func cloneHeaders(in http.Header) http.Header {
	out := make(http.Header, len(in))
	for key, vals := range in {
		out[key] = append([]string(nil), vals...)
	}
	return out
}

type streamCaptureWriter struct {
	header  http.Header
	status  int
	body    bytes.Buffer
	pending string
	onEvent func(SSEEvent)
}

func newStreamCaptureWriter(onEvent func(SSEEvent)) *streamCaptureWriter {
	return &streamCaptureWriter{
		header:  make(http.Header),
		status:  http.StatusOK,
		onEvent: onEvent,
	}
}

func (w *streamCaptureWriter) Header() http.Header {
	return w.header
}

func (w *streamCaptureWriter) WriteHeader(status int) {
	w.status = status
}

func (w *streamCaptureWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	_, _ = w.body.Write(p)
	if w.onEvent != nil {
		w.pending += string(p)
		w.emitCompleteEvents()
	}
	return len(p), nil
}

func (w *streamCaptureWriter) Flush() {}

func (w *streamCaptureWriter) Body() []byte {
	return w.body.Bytes()
}

func (w *streamCaptureWriter) StatusCode() int {
	return w.status
}

func (w *streamCaptureWriter) emitCompleteEvents() {
	normalized := strings.ReplaceAll(w.pending, "\r\n", "\n")
	parts := strings.Split(normalized, "\n\n")
	if len(parts) == 0 {
		return
	}

	lastIndex := len(parts) - 1
	if strings.HasSuffix(normalized, "\n\n") {
		w.pending = ""
	} else {
		w.pending = parts[lastIndex]
		parts = parts[:lastIndex]
	}

	for _, chunk := range parts {
		chunk = strings.TrimSpace(chunk)
		if chunk == "" {
			continue
		}
		if event, ok := parseSSEChunk(chunk); ok {
			w.onEvent(event)
		}
	}
}
