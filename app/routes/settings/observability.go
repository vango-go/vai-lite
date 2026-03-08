package settings

import (
	"github.com/vango-go/vai-lite/app/components"
	"github.com/vango-go/vango"
	. "github.com/vango-go/vango/el"
)

func ObservabilityPage(ctx vango.Ctx) *vango.VNode {
	actor, ok := components.CurrentActor(ctx)
	if !ok {
		return components.AuthRedirectPage()
	}
	return Fragment(components.ObservabilityPage(components.ObservabilityPageProps{Actor: actor}))
}
