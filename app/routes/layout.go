package routes

import (
	"github.com/vango-go/vai-lite/app/components"
	"github.com/vango-go/vango"
)

func Layout(ctx vango.Ctx, children vango.Slot) *vango.VNode {
	return components.RootLayout(ctx, children)
}
