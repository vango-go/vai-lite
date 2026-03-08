package settings

import (
	"github.com/vango-go/vai-lite/app/components"
	"github.com/vango-go/vango"
)

func Layout(ctx vango.Ctx, children vango.Slot) *vango.VNode {
	actor, ok := components.CurrentActor(ctx)
	if !ok {
		return components.AuthRedirectPage()
	}
	return components.SettingsLayout(ctx, actor, ctx.Path(), children)
}
