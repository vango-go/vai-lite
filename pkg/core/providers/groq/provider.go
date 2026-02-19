// Package groq implements the Groq API provider.
// Groq uses an OpenAI-compatible API, so this provider wraps the OpenAI provider
// with a different base URL and adjusted capabilities.
package groq

import (
	"context"
	"net/http"

	"github.com/vango-go/vai-lite/pkg/core/providers/openai"
	"github.com/vango-go/vai-lite/pkg/core/types"
)

const (
	// DefaultBaseURL is the Groq API endpoint.
	DefaultBaseURL = "https://api.groq.com/openai/v1"
)

// ProviderCapabilities describes what a provider supports.
type ProviderCapabilities struct {
	Vision           bool
	AudioInput       bool
	AudioOutput      bool
	Video            bool
	Tools            bool
	ToolStreaming    bool
	Thinking         bool
	StructuredOutput bool
	NativeTools      []string
}

// EventStream is an iterator over streaming events.
type EventStream interface {
	Next() (types.StreamEvent, error)
	Close() error
}

// Provider implements the Groq API using OpenAI-compatible interface.
type Provider struct {
	apiKey         string
	baseURL        string
	httpClient     *http.Client
	maxTokensField openai.MaxTokensField
	inner          *openai.Provider
}

// New creates a new Groq provider.
func New(apiKey string, opts ...Option) *Provider {
	p := &Provider{
		apiKey:         apiKey,
		baseURL:        DefaultBaseURL,
		httpClient:     &http.Client{},
		maxTokensField: openai.MaxTokensFieldMaxTokens,
	}
	for _, opt := range opts {
		opt(p)
	}

	// Create inner OpenAI provider with configured settings
	p.inner = openai.New(apiKey,
		openai.WithBaseURL(p.baseURL),
		openai.WithHTTPClient(p.httpClient),
		openai.WithResponseModelPrefix("groq"),
		openai.WithMaxTokensField(p.maxTokensField),
	)

	return p
}

// Name returns the provider identifier.
func (p *Provider) Name() string {
	return "groq"
}

// Capabilities returns what this provider supports.
func (p *Provider) Capabilities() ProviderCapabilities {
	return ProviderCapabilities{
		Vision:           true, // Some Groq models support vision (llama-3.2-90b-vision)
		AudioInput:       false,
		AudioOutput:      false,
		Video:            false,
		Tools:            true,
		ToolStreaming:    true,
		Thinking:         false,
		StructuredOutput: true,
		NativeTools:      []string{}, // No native tools
	}
}

// CreateMessage sends a non-streaming request to Groq.
func (p *Provider) CreateMessage(ctx context.Context, req *types.MessageRequest) (*types.MessageResponse, error) {
	return p.inner.CreateMessage(ctx, req)
}

// StreamMessage sends a streaming request to Groq.
func (p *Provider) StreamMessage(ctx context.Context, req *types.MessageRequest) (EventStream, error) {
	return p.inner.StreamMessage(ctx, req)
}
