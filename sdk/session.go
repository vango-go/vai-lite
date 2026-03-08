package vai

import (
	"context"
	"strings"
)

const gatewaySessionIDHeader = "X-VAI-Session-ID"

type ctxKeySessionID struct{}

// WithSessionID associates a gateway session id with proxy-mode requests.
// The session id is never forwarded upstream to model providers.
func WithSessionID(ctx context.Context, id string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, ctxKeySessionID{}, id)
}

func sessionIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx.Value(ctxKeySessionID{}).(string)
	return strings.TrimSpace(value)
}
