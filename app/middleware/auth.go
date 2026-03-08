package middleware

import (
	"net/http"
	"net/url"

	"github.com/vango-go/vai-lite/internal/appruntime"
	"github.com/vango-go/vai-lite/internal/services"
	"github.com/vango-go/vango"
	workos "github.com/vango-go/vango-workos"
)

func RequireActor() vango.RouteMiddleware {
	return vango.RouteMiddlewareFunc(func(ctx vango.Ctx, next func() error) error {
		if !appruntime.Get().Config.WorkOSEnabled {
			return next()
		}

		if err := workos.RequireAuth.Handle(ctx, func() error { return nil }); err != nil {
			ctx.Redirect("/auth/signin?return_to="+url.QueryEscape(ctx.Path()), http.StatusSeeOther)
			return nil
		}

		identity, ok := workos.CurrentIdentity(ctx)
		if !ok {
			ctx.Redirect("/auth/signin?return_to="+url.QueryEscape(ctx.Path()), http.StatusSeeOther)
			return nil
		}

		if _, err := services.ActorFromIdentity(identity); err != nil {
			appruntime.Get().Logger.Error("actor projection failed", "error", err)
			ctx.Redirect("/auth/signin?return_to="+url.QueryEscape(ctx.Path()), http.StatusSeeOther)
			return nil
		}

		return next()
	})
}
