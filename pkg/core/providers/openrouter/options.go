package openrouter

import "net/http"

// Option configures the OpenRouter provider.
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

// WithSiteURL sets the HTTP-Referer header for attribution.
func WithSiteURL(url string) Option {
	return func(p *Provider) {
		p.siteURL = url
	}
}

// WithSiteName sets the X-Title header for attribution.
func WithSiteName(name string) Option {
	return func(p *Provider) {
		p.siteName = name
	}
}
