package settings

import (
	"github.com/vango-go/vai-lite/app/components"
	"github.com/vango-go/vango"
	. "github.com/vango-go/vango/el"
)

func DevelopersPage(ctx vango.Ctx) *vango.VNode {
	actor, ok := components.CurrentActor(ctx)
	if !ok {
		return components.AuthRedirectPage()
	}
	return Fragment(components.DevelopersPage(components.SettingsPageProps{Actor: actor}))
}
