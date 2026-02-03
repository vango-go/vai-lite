package vai

import "log/slog"

// ClientOption configures a Client.
type ClientOption func(*Client)

// WithProviderKey sets an API key for a specific provider.
// Used to override environment variables in direct mode.
func WithProviderKey(provider, key string) ClientOption {
	return func(c *Client) {
		if c.providerKeys == nil {
			c.providerKeys = make(map[string]string)
		}
		c.providerKeys[provider] = key
	}
}

// WithLogger sets the logger for the client.
func WithLogger(l *slog.Logger) ClientOption {
	return func(c *Client) {
		if l != nil {
			c.logger = l
		}
	}
}
