package vai

import (
	"context"

	"github.com/vango-go/vai-lite/pkg/core/types"
)

type toolExecutionClientContextKey struct{}
type serverToolExecutionContextKey struct{}

func contextWithToolExecutionClient(ctx context.Context, client *Client) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if client == nil {
		return ctx
	}
	return context.WithValue(ctx, toolExecutionClientContextKey{}, client)
}

func toolExecutionClientFromContext(ctx context.Context) *Client {
	if ctx == nil {
		return nil
	}
	client, _ := ctx.Value(toolExecutionClientContextKey{}).(*Client)
	return client
}

func contextWithServerToolExecutionContext(ctx context.Context, execCtx *types.ServerToolExecutionContext) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if execCtx == nil {
		return ctx
	}
	return context.WithValue(ctx, serverToolExecutionContextKey{}, execCtx)
}

func serverToolExecutionContextFromContext(ctx context.Context) *types.ServerToolExecutionContext {
	if ctx == nil {
		return nil
	}
	execCtx, _ := ctx.Value(serverToolExecutionContextKey{}).(*types.ServerToolExecutionContext)
	return execCtx
}
