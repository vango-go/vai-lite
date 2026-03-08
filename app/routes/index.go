package routes

import (
	"github.com/vango-go/vai-lite/app/components"
	"github.com/vango-go/vango"
	. "github.com/vango-go/vango/el"
)

func IndexPage(ctx vango.Ctx) *vango.VNode {
	actor, ok := components.CurrentActor(ctx)
	if !ok {
		return components.LandingPage()
	}
	return Fragment(components.HomePage(components.HomePageProps{Actor: actor}))
}
