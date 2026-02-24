package auth

import (
	"context"
	"net/http"
	"strings"
)

type Principal struct {
	APIKey string
}

type ctxKey struct{}

func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, ctxKey{}, p)
}

func PrincipalFrom(ctx context.Context) (*Principal, bool) {
	p, ok := ctx.Value(ctxKey{}).(*Principal)
	return p, ok && p != nil
}

func ParseBearer(r *http.Request) (string, bool) {
	authz := strings.TrimSpace(r.Header.Get("Authorization"))
	if authz == "" {
		return "", false
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(authz, prefix) {
		return "", false
	}
	token := strings.TrimSpace(strings.TrimPrefix(authz, prefix))
	if token == "" {
		return "", false
	}
	return token, true
}
