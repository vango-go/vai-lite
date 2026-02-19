// Package openrouter implements the OpenRouter API provider.
// OpenRouter is an OpenAI-compatible API that routes across many model providers.
package openrouter

import (
	"context"
	"net/http"

	"github.com/vango-go/vai-lite/pkg/core/providers/openai"
	"github.com/vango-go/vai-lite/pkg/core/types"
)

const (
	// DefaultBaseURL is the OpenRouter API endpoint.
	DefaultBaseURL = "https://openrouter.ai/api/v1"
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

// Provider implements the OpenRouter API using an OpenAI-compatible interface.
type Provider struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
	siteURL    string
	siteName   string
	inner      *openai.Provider
}

// New creates a new OpenRouter provider.
func New(apiKey string, opts ...Option) *Provider {
	p := &Provider{
		apiKey:     apiKey,
		baseURL:    DefaultBaseURL,
		httpClient: &http.Client{},
	}
	for _, opt := range opts {
		opt(p)
	}

	openaiOpts := []openai.Option{
		openai.WithBaseURL(p.baseURL),
		openai.WithHTTPClient(p.httpClient),
		openai.WithResponseModelPrefix("openrouter"),
		openai.WithMaxTokensField(openai.MaxTokensFieldMaxTokens),
	}
	if p.siteURL != "" {
		openaiOpts = append(openaiOpts, openai.WithExtraHeader("HTTP-Referer", p.siteURL))
	}
	if p.siteName != "" {
		openaiOpts = append(openaiOpts, openai.WithExtraHeader("X-Title", p.siteName))
	}

	p.inner = openai.New(apiKey, openaiOpts...)
	return p
}

// Name returns the provider identifier.
func (p *Provider) Name() string {
	return "openrouter"
}

// Capabilities returns what this provider supports.
func (p *Provider) Capabilities() ProviderCapabilities {
	return ProviderCapabilities{
		Vision:           true,
		AudioInput:       false,
		AudioOutput:      false,
		Video:            false,
		Tools:            true,
		ToolStreaming:    true,
		Thinking:         true,
		StructuredOutput: true,
		NativeTools:      []string{},
	}
}

// CreateMessage sends a non-streaming request to OpenRouter.
func (p *Provider) CreateMessage(ctx context.Context, req *types.MessageRequest) (*types.MessageResponse, error) {
	return p.inner.CreateMessage(ctx, req)
}

// StreamMessage sends a streaming request to OpenRouter.
func (p *Provider) StreamMessage(ctx context.Context, req *types.MessageRequest) (EventStream, error) {
	return p.inner.StreamMessage(ctx, req)
}
