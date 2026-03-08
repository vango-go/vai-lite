package settings

import (
	"github.com/vango-go/vai-lite/app/components"
	"github.com/vango-go/vango"
	. "github.com/vango-go/vango/el"
)

func KeysPage(ctx vango.Ctx) *vango.VNode {
	actor, ok := components.CurrentActor(ctx)
	if !ok {
		return components.AuthRedirectPage()
	}
	return Fragment(components.KeysPage(components.SettingsPageProps{Actor: actor}))
}
