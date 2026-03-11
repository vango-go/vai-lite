package components

import (
	"github.com/vango-go/vai-lite/internal/services"
	"github.com/vango-go/vango"
	. "github.com/vango-go/vango/el"
)

type HomePageProps struct {
	Actor services.UserIdentity
}

func HomePage(p HomePageProps) vango.Component {
	return vango.Func(func() *vango.VNode {
		ctx := vango.UseCtx()
		return AppShell(ctx, p.Actor,
			EmptyState(
				"Your workspace is ready",
				"Open the demo chat, manage shared workspace BYOK keys, inspect observability, or top up VAI-hosted credits.",
				vango.Children(
					LinkPrefetch("/demo", Class("btn btn-primary"), Text("Open demo")),
					Link("/settings/keys", Class("btn btn-secondary"), Text("Manage keys")),
					Link("/settings/billing", Class("btn btn-secondary"), Text("Billing")),
				),
			),
		)
	})
}
