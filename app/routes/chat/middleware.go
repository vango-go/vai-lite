package chat

import (
	"github.com/vango-go/vai-lite/app/middleware"
	"github.com/vango-go/vango"
)

func Middleware() []vango.RouteMiddleware {
	return []vango.RouteMiddleware{middleware.RequireActor()}
}
