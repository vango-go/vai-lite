package groq

import (
	"net/http"

	"github.com/vango-go/vai-lite/pkg/core/providers/openai"
)

// Option configures the Groq provider.
type Option func(*Provider)

// WithBaseURL sets a custom base URL (for testing or proxying).
func WithBaseURL(url string) Option {
	return func(p *Provider) {
		p.baseURL = url
	}
}

// WithHTTPClient sets a custom HTTP client.
func WithHTTPClient(client *http.Client) Option {
	return func(p *Provider) {
		p.httpClient = client
	}
}

// WithMaxTokensField controls which max tokens field name is sent.
func WithMaxTokensField(field openai.MaxTokensField) Option {
	return func(p *Provider) {
		if field != openai.MaxTokensFieldMaxTokens && field != openai.MaxTokensFieldMaxCompletionTokens {
			return
		}
		p.maxTokensField = field
	}
}
