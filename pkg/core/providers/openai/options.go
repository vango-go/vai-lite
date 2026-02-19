package openai

import (
	"net/http"
	"strings"
)

// Option configures the OpenAI provider.
type Option func(*Provider)

// MaxTokensField controls which max tokens field is sent for chat completions.
type MaxTokensField string

const (
	// MaxTokensFieldMaxTokens uses "max_tokens".
	MaxTokensFieldMaxTokens MaxTokensField = "max_tokens"
	// MaxTokensFieldMaxCompletionTokens uses "max_completion_tokens".
	MaxTokensFieldMaxCompletionTokens MaxTokensField = "max_completion_tokens"
)

// AuthConfig configures authentication header behavior.
type AuthConfig struct {
	Header string
	Prefix string
	Value  string
}

// WithBaseURL sets a custom base URL (for testing or proxying).
func WithBaseURL(url string) Option {
	return func(p *Provider) {
		p.baseURL = url
	}
}

// WithChatCompletionsPath sets a custom chat completions path.
func WithChatCompletionsPath(path string) Option {
	return func(p *Provider) {
		if path == "" {
			return
		}
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		p.chatCompletionsPath = path
	}
}

// WithHTTPClient sets a custom HTTP client.
func WithHTTPClient(client *http.Client) Option {
	return func(p *Provider) {
		p.httpClient = client
	}
}

// WithResponseModelPrefix sets the model prefix in normalized responses/events.
func WithResponseModelPrefix(prefix string) Option {
	return func(p *Provider) {
		if prefix == "" {
			return
		}
		p.modelPrefix = prefix
	}
}

// WithMaxTokensField sets which max tokens field name to emit.
func WithMaxTokensField(field MaxTokensField) Option {
	return func(p *Provider) {
		if field != MaxTokensFieldMaxTokens && field != MaxTokensFieldMaxCompletionTokens {
			return
		}
		p.maxTokensField = field
	}
}

// WithStreamIncludeUsage controls stream_options.include_usage emission.
func WithStreamIncludeUsage(include bool) Option {
	return func(p *Provider) {
		p.streamIncludeUsage = include
	}
}

// WithAuth sets custom auth header behavior.
func WithAuth(auth AuthConfig) Option {
	return func(p *Provider) {
		if auth.Header == "" {
			auth.Header = p.auth.Header
		}

		// If caller does not provide an explicit value/prefix:
		// - preserve default prefix for Authorization-like headers
		// - use raw API key for non-Authorization headers (common X-API-Key style)
		if auth.Value == "" && auth.Prefix == "" {
			if strings.EqualFold(auth.Header, p.auth.Header) {
				auth.Prefix = p.auth.Prefix
			}
		}
		p.auth = auth
	}
}

// WithExtraHeaders sets additional request headers.
func WithExtraHeaders(headers map[string]string) Option {
	return func(p *Provider) {
		if headers == nil {
			p.extraHeaders = make(map[string]string)
			return
		}
		p.extraHeaders = make(map[string]string, len(headers))
		for key, value := range headers {
			p.extraHeaders[key] = value
		}
	}
}

// WithExtraHeader sets one additional request header.
func WithExtraHeader(key, value string) Option {
	return func(p *Provider) {
		if key == "" {
			return
		}
		if p.extraHeaders == nil {
			p.extraHeaders = make(map[string]string)
		}
		p.extraHeaders[key] = value
	}
}
