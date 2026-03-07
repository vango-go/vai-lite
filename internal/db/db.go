package db

import (
	"context"
	"errors"
	"os"
	"strings"

	neon "github.com/vango-go/vango-neon"
	"github.com/vango-go/vango"
)

// ConnectPool creates the shared Neon-backed application pool.
func ConnectPool(ctx context.Context) (*neon.Pool, error) {
	pooled := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if pooled == "" {
		return nil, errors.New("DATABASE_URL is required")
	}

	direct := strings.TrimSpace(os.Getenv("DATABASE_URL_DIRECT"))
	return neon.Connect(ctx, neon.Config{
		ConnectionString: pooled,
		DirectURL:        direct,
	})
}

type ctxKey struct{}

func Inject(ctx vango.Ctx, database neon.DB) {
	ctx.SetValue(ctxKey{}, database)
}

func FromCtx(ctx vango.Ctx) (neon.DB, bool) {
	val := ctx.Value(ctxKey{})
	if val == nil {
		return nil, false
	}
	database, ok := val.(neon.DB)
	return database, ok
}
